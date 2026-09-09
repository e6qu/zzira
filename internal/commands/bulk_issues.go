package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5"

	"github.com/e6qu/zzira/internal/authz"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

// ExecuteBulkIssueTask applies an asynchronous bulk mutation through the same
// command path as individual REST and browser edits. Each work item is one
// transaction; Jira's result distinguishes successful, inaccessible and
// accessible-but-failed items, and cancellation can stop between items.
func (s *Service) ExecuteBulkIssueTask(ctx context.Context, task store.APITask) error {
	var payload store.BulkIssueEditTaskPayload
	if err := json.Unmarshal(task.Payload, &payload); err != nil {
		return fmt.Errorf("decode bulk edit operation: %w", err)
	}
	processed := make([]int64, 0, len(payload.Issues))
	failed := map[string][]string{}
	invalid := 0
	for index, item := range payload.Issues {
		issue, err := s.Store.IssueByIDOrKey(ctx, task.WorkspaceID, item.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			invalid++
		} else if err != nil {
			return err
		} else {
			visible, visibilityErr := authz.CanSeeIssue(ctx, s.Store, task.WorkspaceID, issue.ProjectID, task.SubmittedBy, issue.SecurityLevelID)
			if visibilityErr != nil {
				return visibilityErr
			}
			if !visible {
				invalid++
			} else if update, updateErr := bulkIssueUpdate(issue, task.SubmittedBy, payload.Operations); updateErr != nil {
				failed[strconv.FormatInt(item.JiraID, 10)] = []string{updateErr.Error()}
			} else if _, _, updateErr = s.UpdateIssue(ctx, update); updateErr != nil {
				failed[strconv.FormatInt(item.JiraID, 10)] = []string{updateErr.Error()}
			} else {
				processed = append(processed, item.JiraID)
			}
		}
		progress := 5 + ((index + 1) * 90 / max(1, len(payload.Issues)))
		if err := s.Store.UpdateAPITaskProgress(ctx, task, progress, fmt.Sprintf("Processed %d of %d issues.", index+1, len(payload.Issues))); err != nil {
			return err
		}
	}
	result := map[string]any{
		"processedAccessibleIssues":       processed,
		"invalidOrInaccessibleIssueCount": invalid,
		"totalIssueCount":                 len(payload.Issues),
	}
	if len(failed) > 0 {
		result["failedAccessibleIssues"] = failed
	}
	return s.Store.CompleteAPITask(ctx, task, fmt.Sprintf("Processed %d of %d issues.", len(processed), len(payload.Issues)), result)
}

func bulkIssueUpdate(issue *models.Issue, actorID string, operations []store.BulkIssueEditOperation) (UpdateIssueInput, error) {
	update := UpdateIssueInput{
		ActorID: actorID, WorkspaceID: issue.WorkspaceID, IssueIDOrKey: issue.ID,
	}
	for _, operation := range operations {
		switch operation.FieldID {
		case "summary":
			var value string
			if err := json.Unmarshal(operation.Value, &value); err != nil {
				return update, fmt.Errorf("summary must be text")
			}
			update.Summary = &value
		case "description":
			update.Description = operation.Value
		case "priority":
			value, err := bulkStringValue(operation.Value)
			if err != nil {
				return update, fmt.Errorf("priority: %w", err)
			}
			update.PriorityID = &value
		case "assignee":
			value, err := bulkStringValue(operation.Value)
			if err != nil {
				return update, fmt.Errorf("assignee: %w", err)
			}
			update.AssigneeID = &value
		case "security":
			value, err := bulkStringValue(operation.Value)
			if err != nil {
				return update, fmt.Errorf("security: %w", err)
			}
			update.SecurityLevelID = &value
		case "labels":
			var values []string
			if err := json.Unmarshal(operation.Value, &values); err != nil {
				return update, fmt.Errorf("labels must be a list")
			}
			labels, err := applyBulkStringOperation(issue.Labels, values, operation.Action)
			if err != nil {
				return update, fmt.Errorf("labels: %w", err)
			}
			update.Labels = &labels
		case "fixVersions", "versions":
			if update.VersionOperations == nil {
				update.VersionOperations = map[string][]map[string]json.RawMessage{}
			}
			versionOperations, err := bulkVersionOperations(operation)
			if err != nil {
				return update, err
			}
			update.VersionOperations[operation.FieldID] = versionOperations
		case "components":
			value, err := bulkComponentsValue(issue.Fields[operation.FieldID], operation)
			if err != nil {
				return update, err
			}
			if update.Fields == nil {
				update.Fields = map[string]json.RawMessage{}
			}
			update.Fields[operation.FieldID] = value
		default:
			if operation.Action != "SET" {
				return update, fmt.Errorf("field %q only supports SET", operation.FieldID)
			}
			if update.Fields == nil {
				update.Fields = map[string]json.RawMessage{}
			}
			update.Fields[operation.FieldID] = operation.Value
		}
	}
	return update, nil
}

func bulkStringValue(raw json.RawMessage) (string, error) {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", fmt.Errorf("must be a string")
	}
	return value, nil
}

func applyBulkStringOperation(current, values []string, action string) ([]string, error) {
	switch action {
	case "SET", "REPLACE":
		return append([]string(nil), values...), nil
	case "REMOVE_ALL":
		return []string{}, nil
	case "ADD":
		result := append([]string(nil), current...)
		seen := map[string]bool{}
		for _, value := range result {
			seen[value] = true
		}
		for _, value := range values {
			if !seen[value] {
				seen[value] = true
				result = append(result, value)
			}
		}
		return result, nil
	case "REMOVE":
		remove := map[string]bool{}
		for _, value := range values {
			remove[value] = true
		}
		result := make([]string, 0, len(current))
		for _, value := range current {
			if !remove[value] {
				result = append(result, value)
			}
		}
		return result, nil
	default:
		return nil, fmt.Errorf("unsupported action %q", action)
	}
}

func bulkVersionOperations(operation store.BulkIssueEditOperation) ([]map[string]json.RawMessage, error) {
	var ids []string
	if err := json.Unmarshal(operation.Value, &ids); err != nil {
		return nil, fmt.Errorf("%s must be a version list", operation.FieldID)
	}
	objects := make([]map[string]string, 0, len(ids))
	for _, id := range ids {
		objects = append(objects, map[string]string{"id": id})
	}
	encoded, err := json.Marshal(objects)
	if err != nil {
		return nil, err
	}
	switch operation.Action {
	case "SET", "REPLACE":
		return []map[string]json.RawMessage{{"set": encoded}}, nil
	case "REMOVE_ALL":
		return []map[string]json.RawMessage{{"set": json.RawMessage(`[]`)}}, nil
	case "ADD", "REMOVE":
		kind := "add"
		if operation.Action == "REMOVE" {
			kind = "remove"
		}
		result := make([]map[string]json.RawMessage, 0, len(ids))
		for _, object := range objects {
			value, marshalErr := json.Marshal(object)
			if marshalErr != nil {
				return nil, marshalErr
			}
			result = append(result, map[string]json.RawMessage{kind: value})
		}
		return result, nil
	default:
		return nil, fmt.Errorf("%s has unsupported action %q", operation.FieldID, operation.Action)
	}
}

func bulkComponentsValue(current json.RawMessage, operation store.BulkIssueEditOperation) (json.RawMessage, error) {
	var changes []string
	if err := json.Unmarshal(operation.Value, &changes); err != nil {
		return nil, fmt.Errorf("components must be a component list")
	}
	var existing []struct {
		ID string `json:"id"`
	}
	if len(current) > 0 && json.Unmarshal(current, &existing) != nil {
		return nil, fmt.Errorf("existing components are invalid")
	}
	currentIDs := make([]string, 0, len(existing))
	for _, component := range existing {
		currentIDs = append(currentIDs, component.ID)
	}
	ids, err := applyBulkStringOperation(currentIDs, changes, operation.Action)
	if err != nil {
		return nil, fmt.Errorf("components: %w", err)
	}
	objects := make([]map[string]string, 0, len(ids))
	for _, id := range ids {
		objects = append(objects, map[string]string{"id": id})
	}
	return json.Marshal(objects)
}
