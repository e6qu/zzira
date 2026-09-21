package api3

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/e6qu/zzira/internal/models"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

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
	configuration, err := h.Store.JiraSiteConfiguration(r.Context(), workspaceID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "Could not read time tracking settings.")
		return
	}
	if err := convertBulkEstimates(parsed, configuration.TimeTracking); err != nil {
		bulkOperationError(w, http.StatusBadRequest, err.Error())
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
	if err := h.resolveBulkEditTargets(r.Context(), workspaceID, issues, operations); err != nil {
		bulkOperationError(w, http.StatusBadRequest, err.Error())
		return
	}
	items := make([]store.BulkIssueTaskItem, 0, len(issues))
	for _, issue := range issues {
		items = append(items, store.BulkIssueTaskItem{ID: issue.ID, JiraID: issue.JiraID})
	}
	task, err := h.Store.EnqueueBulkEditTask(r.Context(), workspaceID, actorID, store.BulkIssueEditTaskPayload{
		Issues: items, Operations: operations, SendBulkNotification: request.SendBulkNotification == nil || *request.SendBulkNotification})
	if errors.Is(err, store.ErrBulkTaskLimit) {
		bulkOperationError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "Could not submit the bulk edit operation.")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"taskId": task.WireID()})
}

// resolveBulkEditTargets turns the ids a client names the work type and the
// status by into the ones the task stores. Jira's ids are what a client
// holds; the task carries this product's own, as every other bulk operation's
// payload does, so the executor never has to guess which kind it was handed.
func (h *Handler) resolveBulkEditTargets(ctx context.Context, workspaceID string, issues []*models.Issue, operations []store.BulkIssueEditOperation) error {
	for index := range operations {
		var named string
		if err := json.Unmarshal(operations[index].Value, &named); err != nil {
			continue
		}
		switch operations[index].FieldID {
		case "issuetype":
			issueType, err := h.Store.IssueTypeByIDOrName(ctx, workspaceID, named)
			if err != nil {
				return fmt.Errorf("issueType.issueTypeId %q is not a work type of this site", named)
			}
			operations[index].Value = mustJSON(issueType.ID)
		case "status":
			// A status can belong to one project, so the one named has to be
			// the same status everywhere the selection reaches.
			resolved := ""
			for _, issue := range issues {
				status, err := h.Store.StatusByIDForProject(ctx, named, issue.ProjectID)
				if err != nil {
					return fmt.Errorf("status.statusId %q is not a status of every selected work item's project", named)
				}
				if resolved != "" && resolved != status.ID {
					return fmt.Errorf("status.statusId %q names a different status in each project of the selection", named)
				}
				resolved = status.ID
			}
			operations[index].Value = mustJSON(resolved)
		}
	}
	return nil
}

// mustJSON encodes a value that cannot fail to encode: a string this package
// has already read out of JSON.
func mustJSON(value string) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return encoded
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
		"cascadingSelectFields": true, "colorFields": true, "datePickerFields": true,
		"multipleGroupPickerFields": true, "multipleSelectClearableUserPickerFields": true,
		"multipleSelectFields": true, "originalEstimateField": true, "singleGroupPickerFields": true,
		"singleVersionPickerFields": true, "timeTrackingField": true, "urlFields": true,
		"issueType": true, "status": true,
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
	// A work item's type and its status are not values on a screen: changing
	// the type re-homes the work item the way a move within its project does,
	// and changing the status runs the transition that leads there. They are
	// named here because Jira's editedFieldsInput names them, and each work
	// item reports its own failure when its workflow or its project does not
	// allow the change.
	if raw := input["issueType"]; raw != nil {
		var field struct {
			IssueTypeID string `json:"issueTypeId"`
		}
		if err := decodeStrictBulkField(raw, &field); err != nil {
			return nil, fmt.Errorf("issueType: %w", err)
		}
		if strings.TrimSpace(field.IssueTypeID) == "" {
			return nil, errors.New("issueType.issueTypeId is required")
		}
		if err := appendOperation("issuetype", "SET", "issuetype", field.IssueTypeID); err != nil {
			return nil, err
		}
	}
	if raw := input["status"]; raw != nil {
		var field struct {
			StatusID string `json:"statusId"`
		}
		if err := decodeStrictBulkField(raw, &field); err != nil {
			return nil, fmt.Errorf("status: %w", err)
		}
		if strings.TrimSpace(field.StatusID) == "" {
			return nil, errors.New("status.statusId is required")
		}
		if err := appendOperation("status", "SET", "status", field.StatusID); err != nil {
			return nil, err
		}
	}
	if err := parseMoreBulkEditFields(input, appendOperation); err != nil {
		return nil, err
	}
	return parsed, nil
}

// parseMoreBulkEditFields reads the remaining Jira bulk edit field families,
// each into the value the ordinary issue update accepts for its field.
func parseMoreBulkEditFields(input map[string]json.RawMessage, appendOperation func(fieldID, action, fieldType string, value any) error) error {
	type option struct {
		OptionID json.RawMessage `json:"optionId"`
	}
	type group struct {
		GroupName string `json:"groupName"`
	}
	if raw := input["cascadingSelectFields"]; raw != nil {
		var fields []struct {
			FieldID string  `json:"fieldId"`
			Parent  option  `json:"parentOptionValue"`
			Child   *option `json:"childOptionValue"`
		}
		if err := decodeStrictBulkField(raw, &fields); err != nil {
			return fmt.Errorf("cascadingSelectFields: %w", err)
		}
		for _, field := range fields {
			parent, err := bulkJSONIdentifier(field.Parent.OptionID)
			if err != nil {
				return fmt.Errorf("cascadingSelectFields %s: %w", field.FieldID, err)
			}
			value := map[string]string{"parent": parent}
			if field.Child != nil {
				child, err := bulkJSONIdentifier(field.Child.OptionID)
				if err != nil {
					return fmt.Errorf("cascadingSelectFields %s child: %w", field.FieldID, err)
				}
				value["child"] = child
			}
			if err := appendOperation(field.FieldID, "SET", "cascadingSelect", value); err != nil {
				return err
			}
		}
	}
	if raw := input["colorFields"]; raw != nil {
		var fields []struct {
			FieldID string `json:"fieldId"`
			Color   struct {
				Name string `json:"name"`
			} `json:"color"`
		}
		if err := decodeStrictBulkField(raw, &fields); err != nil {
			return fmt.Errorf("colorFields: %w", err)
		}
		for _, field := range fields {
			if field.Color.Name == "" {
				return fmt.Errorf("colorFields %s requires color.name", field.FieldID)
			}
			if err := appendOperation(field.FieldID, "SET", "color", field.Color.Name); err != nil {
				return err
			}
		}
	}
	if raw := input["datePickerFields"]; raw != nil {
		var fields []struct {
			FieldID string `json:"fieldId"`
			Date    *struct {
				Formatted string `json:"formattedDate"`
			} `json:"date"`
		}
		if err := decodeStrictBulkField(raw, &fields); err != nil {
			return fmt.Errorf("datePickerFields: %w", err)
		}
		for _, field := range fields {
			value := any(nil)
			if field.Date != nil {
				if _, err := time.Parse("2006-01-02", field.Date.Formatted); err != nil {
					return fmt.Errorf("datePickerFields %s requires formattedDate as yyyy-MM-dd", field.FieldID)
				}
				value = field.Date.Formatted
			}
			if err := appendOperation(field.FieldID, "SET", "datePicker", value); err != nil {
				return err
			}
		}
	}
	if raw := input["multipleGroupPickerFields"]; raw != nil {
		var fields []struct {
			FieldID string  `json:"fieldId"`
			Groups  []group `json:"groups"`
		}
		if err := decodeStrictBulkField(raw, &fields); err != nil {
			return fmt.Errorf("multipleGroupPickerFields: %w", err)
		}
		for _, field := range fields {
			values := make([]map[string]string, 0, len(field.Groups))
			for _, picked := range field.Groups {
				if picked.GroupName == "" {
					return fmt.Errorf("multipleGroupPickerFields %s requires groupName", field.FieldID)
				}
				values = append(values, map[string]string{"name": picked.GroupName})
			}
			if err := appendOperation(field.FieldID, "SET", "multiGroup", values); err != nil {
				return err
			}
		}
	}
	if raw := input["singleGroupPickerFields"]; raw != nil {
		var fields []struct {
			FieldID string `json:"fieldId"`
			Group   group  `json:"group"`
		}
		if err := decodeStrictBulkField(raw, &fields); err != nil {
			return fmt.Errorf("singleGroupPickerFields: %w", err)
		}
		for _, field := range fields {
			if field.Group.GroupName == "" {
				return fmt.Errorf("singleGroupPickerFields %s requires group.groupName", field.FieldID)
			}
			if err := appendOperation(field.FieldID, "SET", "singleGroup", map[string]string{"name": field.Group.GroupName}); err != nil {
				return err
			}
		}
	}
	if raw := input["multipleSelectClearableUserPickerFields"]; raw != nil {
		var fields []struct {
			FieldID string `json:"fieldId"`
			Users   []struct {
				AccountID string `json:"accountId"`
			} `json:"users"`
		}
		if err := decodeStrictBulkField(raw, &fields); err != nil {
			return fmt.Errorf("multipleSelectClearableUserPickerFields: %w", err)
		}
		for _, field := range fields {
			values := make([]map[string]string, 0, len(field.Users))
			for _, user := range field.Users {
				if user.AccountID == "" {
					return fmt.Errorf("multipleSelectClearableUserPickerFields %s requires accountId", field.FieldID)
				}
				values = append(values, map[string]string{"accountId": user.AccountID})
			}
			if err := appendOperation(field.FieldID, "SET", "multiUser", values); err != nil {
				return err
			}
		}
	}
	if raw := input["multipleSelectFields"]; raw != nil {
		var fields []struct {
			FieldID string   `json:"fieldId"`
			Options []option `json:"options"`
		}
		if err := decodeStrictBulkField(raw, &fields); err != nil {
			return fmt.Errorf("multipleSelectFields: %w", err)
		}
		for _, field := range fields {
			values := make([]map[string]string, 0, len(field.Options))
			for _, picked := range field.Options {
				id, err := bulkJSONIdentifier(picked.OptionID)
				if err != nil {
					return fmt.Errorf("multipleSelectFields %s: %w", field.FieldID, err)
				}
				values = append(values, map[string]string{"id": id})
			}
			if err := appendOperation(field.FieldID, "SET", "multiSelect", values); err != nil {
				return err
			}
		}
	}
	if raw := input["singleVersionPickerFields"]; raw != nil {
		var fields []struct {
			FieldID string `json:"fieldId"`
			Version struct {
				VersionID string `json:"versionId"`
			} `json:"version"`
		}
		if err := decodeStrictBulkField(raw, &fields); err != nil {
			return fmt.Errorf("singleVersionPickerFields: %w", err)
		}
		for _, field := range fields {
			if field.Version.VersionID == "" {
				return fmt.Errorf("singleVersionPickerFields %s requires version.versionId", field.FieldID)
			}
			if err := appendOperation(field.FieldID, "SET", "singleVersion", map[string]string{"id": field.Version.VersionID}); err != nil {
				return err
			}
		}
	}
	if raw := input["urlFields"]; raw != nil {
		var fields []struct {
			FieldID string `json:"fieldId"`
			URL     string `json:"url"`
		}
		if err := decodeStrictBulkField(raw, &fields); err != nil {
			return fmt.Errorf("urlFields: %w", err)
		}
		for _, field := range fields {
			if err := appendOperation(field.FieldID, "SET", "url", field.URL); err != nil {
				return err
			}
		}
	}
	// Estimates carry Jira duration text; the submission converts it to
	// seconds with the site's time tracking settings.
	if raw := input["originalEstimateField"]; raw != nil {
		var field struct {
			Estimate string `json:"originalEstimateField"`
		}
		if err := decodeStrictBulkField(raw, &field); err != nil || strings.TrimSpace(field.Estimate) == "" {
			return errors.New("originalEstimateField.originalEstimateField is required")
		}
		if err := appendOperation("timeoriginalestimate", "SET", "originalEstimate", field.Estimate); err != nil {
			return err
		}
	}
	if raw := input["timeTrackingField"]; raw != nil {
		var field struct {
			TimeRemaining string `json:"timeRemaining"`
		}
		if err := decodeStrictBulkField(raw, &field); err != nil || strings.TrimSpace(field.TimeRemaining) == "" {
			return errors.New("timeTrackingField.timeRemaining is required")
		}
		if err := appendOperation("timeestimate", "SET", "timeTracking", field.TimeRemaining); err != nil {
			return err
		}
	}
	return nil
}

// convertBulkEstimates turns the duration text of estimate operations into
// seconds with the site's time tracking settings.
func convertBulkEstimates(parsed []parsedBulkEditOperation, cfg models.TimeTrackingConfiguration) error {
	for index, item := range parsed {
		if item.fieldType != "originalEstimate" && item.fieldType != "timeTracking" {
			continue
		}
		var text string
		if err := json.Unmarshal(item.operation.Value, &text); err != nil {
			return fmt.Errorf("%s requires a duration", item.operation.FieldID)
		}
		seconds, err := models.ParseJiraDuration(text, cfg)
		if err != nil {
			return fmt.Errorf("%s: %w", item.operation.FieldID, err)
		}
		parsed[index].operation.Value, _ = json.Marshal(seconds)
	}
	return nil
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
		// The type and the status are not on the editable field list: they
		// are not fields on a screen, and whether one can change is the
		// work item's own workflow and its project's work types to answer.
		if fieldID != "issuetype" && fieldID != "status" {
			if available[fieldID] == "" {
				return nil, fmt.Errorf("field %s is not editable for every selected issue", fieldID)
			}
			if available[fieldID] != item.fieldType {
				return nil, fmt.Errorf("field %s must use the %s bulk input", fieldID, available[fieldID])
			}
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
