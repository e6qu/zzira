package api3

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/store"
)

type bulkEditRequest struct {
	EditedFieldsInput      map[string]json.RawMessage `json:"editedFieldsInput"`
	SelectedActions        []string                   `json:"selectedActions"`
	SelectedIssueIDsOrKeys []string                   `json:"selectedIssueIdsOrKeys"`
	SendBulkNotification   *bool                      `json:"sendBulkNotification,omitempty"`
}

type parsedBulkEditOperation struct {
	operation store.BulkIssueEditOperation
	fieldType string
}

func (h *Handler) submitBulkEdit(w http.ResponseWriter, r *http.Request) {
	workspaceID, actorID, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	var request bulkEditRequest
	if !decodeBulkOperationBody(w, r, &request) {
		return
	}
	issues, selectionErr := h.resolveBulkIssues(r, workspaceID, request.SelectedIssueIDsOrKeys)
	if selectionErr != nil {
		bulkOperationError(w, http.StatusBadRequest, selectionErr.Error())
		return
	}
	parsed, parseErr := parseBulkEditOperations(request.EditedFieldsInput)
	if parseErr != nil {
		bulkOperationError(w, http.StatusBadRequest, parseErr.Error())
		return
	}
	metadata, err := h.Store.IssueCreateMetadata(r.Context(), workspaceID, actorID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "Could not validate bulk editable fields.")
		return
	}
	available := map[string]string{}
	for _, field := range commonBulkEditableFields(metadata, issues, h.BaseURL) {
		available[field.ID] = field.Type
	}
	operations, validationErr := validateBulkEditActions(request.SelectedActions, parsed, available)
	if validationErr != nil {
		bulkOperationError(w, http.StatusBadRequest, validationErr.Error())
		return
	}
	items := make([]store.BulkIssueTaskItem, 0, len(issues))
	for _, issue := range issues {
		items = append(items, store.BulkIssueTaskItem{ID: issue.ID, JiraID: issue.JiraID})
	}
	task, err := h.Store.EnqueueBulkEditTask(r.Context(), workspaceID, actorID, items, operations)
	if errors.Is(err, store.ErrBulkTaskLimit) {
		bulkOperationError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "Could not submit the bulk edit operation.")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"taskId": task.ID})
}

func parseBulkEditOperations(input map[string]json.RawMessage) ([]parsedBulkEditOperation, error) {
	if input == nil {
		return nil, errors.New("editedFieldsInput is required")
	}
	allowed := map[string]bool{
		"singleLineTextFields": true, "clearableNumberFields": true,
		"dateTimePickerFields": true, "richTextFields": true,
		"singleSelectClearableUserPickerFields": true, "singleSelectFields": true,
		"labelsFields": true, "multipleVersionPickerFields": true,
		"multiselectComponents": true, "priority": true,
	}
	for key := range input {
		if !allowed[key] {
			return nil, fmt.Errorf("editedFieldsInput.%s is not supported", key)
		}
	}
	parsed := make([]parsedBulkEditOperation, 0)
	appendOperation := func(fieldID, action, fieldType string, value any) error {
		if strings.TrimSpace(fieldID) == "" {
			return errors.New("every edited field requires a fieldId")
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return err
		}
		parsed = append(parsed, parsedBulkEditOperation{operation: store.BulkIssueEditOperation{FieldID: fieldID, Action: action, Value: encoded}, fieldType: fieldType})
		return nil
	}
	if raw := input["singleLineTextFields"]; raw != nil {
		var fields []struct {
			FieldID string `json:"fieldId"`
			Text    string `json:"text"`
		}
		if err := decodeStrictBulkField(raw, &fields); err != nil {
			return nil, fmt.Errorf("singleLineTextFields: %w", err)
		}
		for _, field := range fields {
			if err := appendOperation(field.FieldID, "SET", "singleLineText", field.Text); err != nil {
				return nil, err
			}
		}
	}
	if raw := input["clearableNumberFields"]; raw != nil {
		var fields []struct {
			FieldID string          `json:"fieldId"`
			Value   json.RawMessage `json:"value"`
		}
		if err := decodeStrictBulkField(raw, &fields); err != nil {
			return nil, fmt.Errorf("clearableNumberFields: %w", err)
		}
		for _, field := range fields {
			value := field.Value
			if value == nil {
				value = json.RawMessage(`null`)
			}
			parsed = append(parsed, parsedBulkEditOperation{operation: store.BulkIssueEditOperation{FieldID: field.FieldID, Action: "SET", Value: value}, fieldType: "number"})
		}
	}
	if raw := input["dateTimePickerFields"]; raw != nil {
		var fields []struct {
			FieldID  string `json:"fieldId"`
			DateTime struct {
				Formatted string `json:"formattedDateTime"`
			} `json:"dateTime"`
		}
		if err := decodeStrictBulkField(raw, &fields); err != nil {
			return nil, fmt.Errorf("dateTimePickerFields: %w", err)
		}
		for _, field := range fields {
			if err := appendOperation(field.FieldID, "SET", "dateTime", field.DateTime.Formatted); err != nil {
				return nil, err
			}
		}
	}
	if raw := input["richTextFields"]; raw != nil {
		var fields []struct {
			FieldID  string `json:"fieldId"`
			RichText struct {
				ADFValue json.RawMessage `json:"adfValue"`
			} `json:"richText"`
		}
		if err := decodeStrictBulkField(raw, &fields); err != nil {
			return nil, fmt.Errorf("richTextFields: %w", err)
		}
		for _, field := range fields {
			value := field.RichText.ADFValue
			if value == nil {
				value = json.RawMessage(`{"type":"doc","version":1,"content":[]}`)
			}
			var document struct {
				Type    string `json:"type"`
				Version int    `json:"version"`
			}
			if json.Unmarshal(value, &document) != nil || document.Type != "doc" || document.Version != 1 {
				return nil, fmt.Errorf("richTextFields %s requires an ADF version 1 document", field.FieldID)
			}
			parsed = append(parsed, parsedBulkEditOperation{operation: store.BulkIssueEditOperation{FieldID: field.FieldID, Action: "SET", Value: value}, fieldType: "richText"})
		}
	}
	if raw := input["singleSelectClearableUserPickerFields"]; raw != nil {
		var fields []struct {
			FieldID string `json:"fieldId"`
			User    *struct {
				AccountID string `json:"accountId"`
			} `json:"user"`
		}
		if err := decodeStrictBulkField(raw, &fields); err != nil {
			return nil, fmt.Errorf("singleSelectClearableUserPickerFields: %w", err)
		}
		for _, field := range fields {
			accountID := ""
			if field.User != nil {
				accountID = field.User.AccountID
				if accountID == "" {
					return nil, fmt.Errorf("singleSelectClearableUserPickerFields %s requires user.accountId", field.FieldID)
				}
			}
			if err := appendOperation(field.FieldID, "SET", "assignee", accountID); err != nil {
				return nil, err
			}
		}
	}
	if raw := input["singleSelectFields"]; raw != nil {
		var fields []struct {
			FieldID string `json:"fieldId"`
			Option  struct {
				OptionID json.RawMessage `json:"optionId"`
			} `json:"option"`
		}
		if err := decodeStrictBulkField(raw, &fields); err != nil {
			return nil, fmt.Errorf("singleSelectFields: %w", err)
		}
		for _, field := range fields {
			optionID, err := bulkJSONIdentifier(field.Option.OptionID)
			if err != nil {
				return nil, fmt.Errorf("singleSelectFields %s: %w", field.FieldID, err)
			}
			if optionID == "-1" {
				optionID = ""
			}
			if err := appendOperation(field.FieldID, "SET", "singleSelect", optionID); err != nil {
				return nil, err
			}
		}
	}
	if raw := input["labelsFields"]; raw != nil {
		var fields []struct {
			FieldID string `json:"fieldId"`
			Action  string `json:"bulkEditMultiSelectFieldOption"`
			Labels  []struct {
				Name string `json:"name"`
			} `json:"labels"`
			Properties []map[string]any `json:"labelProperties,omitempty"`
		}
		if err := decodeStrictBulkField(raw, &fields); err != nil {
			return nil, fmt.Errorf("labelsFields: %w", err)
		}
		for _, field := range fields {
			values := make([]string, 0, len(field.Labels))
			for _, label := range field.Labels {
				if label.Name == "" {
					return nil, fmt.Errorf("labelsFields %s requires label names", field.FieldID)
				}
				values = append(values, label.Name)
			}
			if err := appendOperation(field.FieldID, field.Action, "labels", values); err != nil {
				return nil, err
			}
		}
	}
	if raw := input["multipleVersionPickerFields"]; raw != nil {
		var fields []struct {
			FieldID  string `json:"fieldId"`
			Action   string `json:"bulkEditMultiSelectFieldOption"`
			Versions []struct {
				VersionID string `json:"versionId"`
			} `json:"versions"`
		}
		if err := decodeStrictBulkField(raw, &fields); err != nil {
			return nil, fmt.Errorf("multipleVersionPickerFields: %w", err)
		}
		for _, field := range fields {
			values := make([]string, 0, len(field.Versions))
			for _, version := range field.Versions {
				if version.VersionID == "" {
					return nil, fmt.Errorf("multipleVersionPickerFields %s requires versionId", field.FieldID)
				}
				values = append(values, version.VersionID)
			}
			if err := appendOperation(field.FieldID, field.Action, "versions", values); err != nil {
				return nil, err
			}
		}
	}
	if raw := input["multiselectComponents"]; raw != nil {
		var field struct {
			FieldID    string `json:"fieldId"`
			Action     string `json:"bulkEditMultiSelectFieldOption"`
			Components []struct {
				ComponentID json.RawMessage `json:"componentId"`
			} `json:"components"`
		}
		if err := decodeStrictBulkField(raw, &field); err != nil {
			return nil, fmt.Errorf("multiselectComponents: %w", err)
		}
		values := make([]string, 0, len(field.Components))
		for _, component := range field.Components {
			componentID, err := bulkJSONIdentifier(component.ComponentID)
			if err != nil {
				return nil, fmt.Errorf("multiselectComponents: %w", err)
			}
			values = append(values, componentID)
		}
		if err := appendOperation(field.FieldID, field.Action, "components", values); err != nil {
			return nil, err
		}
	}
	if raw := input["priority"]; raw != nil {
		var field struct {
			PriorityID string `json:"priorityId"`
		}
		if err := decodeStrictBulkField(raw, &field); err != nil {
			return nil, fmt.Errorf("priority: %w", err)
		}
		if field.PriorityID == "" {
			return nil, errors.New("priority.priorityId is required")
		}
		if err := appendOperation("priority", "SET", "priority", field.PriorityID); err != nil {
			return nil, err
		}
	}
	return parsed, nil
}

func decodeStrictBulkField(raw json.RawMessage, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("expected one JSON value")
	}
	return nil
}

func bulkJSONIdentifier(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", errors.New("identifier is required")
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text, nil
	}
	var number json.Number
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&number); err != nil {
		return "", errors.New("identifier must be a string or integer")
	}
	if _, err := strconv.ParseInt(number.String(), 10, 64); err != nil {
		return "", errors.New("identifier must be a string or integer")
	}
	return number.String(), nil
}

func validateBulkEditActions(selected []string, parsed []parsedBulkEditOperation, available map[string]string) ([]store.BulkIssueEditOperation, error) {
	if len(selected) < 1 || len(selected) > 200 {
		return nil, errors.New("selectedActions must contain between 1 and 200 fields")
	}
	selectedSet := make(map[string]bool, len(selected))
	for _, fieldID := range selected {
		fieldID = strings.TrimSpace(fieldID)
		if fieldID == "" || selectedSet[fieldID] {
			return nil, errors.New("selectedActions must contain unique non-empty field IDs")
		}
		selectedSet[fieldID] = true
	}
	if len(parsed) != len(selectedSet) {
		return nil, errors.New("every selectedActions field must appear exactly once in editedFieldsInput")
	}
	operations := make([]store.BulkIssueEditOperation, 0, len(parsed))
	seen := map[string]bool{}
	for _, item := range parsed {
		fieldID := item.operation.FieldID
		if seen[fieldID] {
			return nil, fmt.Errorf("field %s appears more than once in editedFieldsInput", fieldID)
		}
		seen[fieldID] = true
		if !selectedSet[fieldID] {
			return nil, fmt.Errorf("edited field %s is not listed in selectedActions", fieldID)
		}
		if available[fieldID] == "" {
			return nil, fmt.Errorf("field %s is not editable for every selected issue", fieldID)
		}
		if available[fieldID] != item.fieldType {
			return nil, fmt.Errorf("field %s must use the %s bulk input", fieldID, available[fieldID])
		}
		if !validBulkMultiAction(item.operation.Action) {
			return nil, fmt.Errorf("field %s has invalid bulk edit action %q", fieldID, item.operation.Action)
		}
		operations = append(operations, item.operation)
	}
	return operations, nil
}

func validBulkMultiAction(action string) bool {
	switch action {
	case "SET", "ADD", "REMOVE", "REPLACE", "REMOVE_ALL":
		return true
	default:
		return false
	}
}
