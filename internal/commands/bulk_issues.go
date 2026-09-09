package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
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
	if task.IsBulkDeleteOperation() {
		return s.executeBulkDeleteTask(ctx, task)
	}
	if task.IsBulkMoveOperation() {
		return s.executeBulkMoveTask(ctx, task)
	}
	if task.IsBulkTransitionOperation() {
		return s.executeBulkTransitionTask(ctx, task)
	}
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

func (s *Service) executeBulkTransitionTask(ctx context.Context, task store.APITask) error {
	var payload store.BulkIssueTransitionTaskPayload
	if err := json.Unmarshal(task.Payload, &payload); err != nil {
		return fmt.Errorf("decode bulk transition operation: %w", err)
	}
	processed := make([]int64, 0, len(payload.Issues))
	failed := map[string][]string{}
	invalid := 0
	for index, item := range payload.Issues {
		if prior, err := s.Store.BulkIssueTaskItemResult(ctx, task.ID, item.ID); err == nil {
			var result struct {
				JiraID int64 `json:"jiraId"`
			}
			if json.Unmarshal(prior, &result) == nil && result.JiraID != 0 {
				processed = append(processed, result.JiraID)
			}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		} else {
			issue, lookupErr := s.Store.IssueByIDOrKey(ctx, task.WorkspaceID, item.ID)
			if errors.Is(lookupErr, pgx.ErrNoRows) {
				invalid++
			} else if lookupErr != nil {
				return lookupErr
			} else {
				visible, visibilityErr := authz.CanSeeIssue(ctx, s.Store, task.WorkspaceID, issue.ProjectID, task.SubmittedBy, issue.SecurityLevelID)
				if visibilityErr != nil {
					return visibilityErr
				}
				if !visible {
					invalid++
				} else if _, _, transitionErr := s.TransitionIssueWithUpdateFromAPI(ctx, task.SubmittedBy, task.WorkspaceID, issue.ID, item.TransitionID, store.IssueUpdate{TaskID: task.ID}); transitionErr != nil {
					failed[strconv.FormatInt(item.JiraID, 10)] = []string{transitionErr.Error()}
				} else {
					processed = append(processed, item.JiraID)
				}
			}
		}
		progress := 5 + ((index + 1) * 90 / max(1, len(payload.Issues)))
		if err := s.Store.UpdateAPITaskProgress(ctx, task, progress, fmt.Sprintf("Processed %d of %d issues.", index+1, len(payload.Issues))); err != nil {
			return err
		}
	}
	result := map[string]any{"processedAccessibleIssues": processed, "invalidOrInaccessibleIssueCount": invalid, "totalIssueCount": len(payload.Issues)}
	if len(failed) > 0 {
		result["failedAccessibleIssues"] = failed
	}
	return s.Store.CompleteAPITask(ctx, task, fmt.Sprintf("Processed %d of %d issues.", len(processed), len(payload.Issues)), result)
}

func (s *Service) executeBulkMoveTask(ctx context.Context, task store.APITask) error {
	var payload store.BulkIssueMoveTaskPayload
	if err := json.Unmarshal(task.Payload, &payload); err != nil {
		return fmt.Errorf("decode bulk move operation: %w", err)
	}
	processed := make([]int64, 0, len(payload.Issues))
	failed := map[string][]string{}
	invalid := 0
	for index, item := range payload.Issues {
		if prior, err := s.Store.BulkIssueTaskItemResult(ctx, task.ID, item.ID); err == nil {
			var result struct {
				JiraID int64 `json:"jiraId"`
			}
			if json.Unmarshal(prior, &result) == nil && result.JiraID != 0 {
				processed = append(processed, result.JiraID)
			}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		} else {
			issue, lookupErr := s.Store.IssueByIDOrKey(ctx, task.WorkspaceID, item.ID)
			if errors.Is(lookupErr, pgx.ErrNoRows) {
				invalid++
			} else if lookupErr != nil {
				return lookupErr
			} else {
				visible, visibilityErr := authz.CanSeeIssue(ctx, s.Store, task.WorkspaceID, issue.ProjectID, task.SubmittedBy, issue.SecurityLevelID)
				if visibilityErr != nil {
					return visibilityErr
				}
				if !visible {
					invalid++
				} else if children, childErr := s.Store.ChildIssues(ctx, task.WorkspaceID, issue.ID); childErr != nil {
					return childErr
				} else if len(children) > 0 && issue.ProjectID != item.ProjectID {
					failed[strconv.FormatInt(item.JiraID, 10)] = []string{"moving a parent with implicit subtasks requires an explicit subtask mapping"}
				} else {
					statusID, statusErr := s.bulkMoveStatus(ctx, issue.Status.ID, item)
					if statusErr != nil {
						failed[strconv.FormatInt(item.JiraID, 10)] = []string{statusErr.Error()}
					} else if _, _, moveErr := s.Store.MoveIssue(ctx, task.SubmittedBy, task.WorkspaceID, issue.ID, store.IssueMove{
						ProjectID: item.ProjectID, IssueTypeID: item.IssueTypeID, ParentID: item.ParentID, StatusID: statusID, TaskID: task.ID,
					}); moveErr != nil {
						failed[strconv.FormatInt(item.JiraID, 10)] = []string{moveErr.Error()}
					} else {
						processed = append(processed, item.JiraID)
					}
				}
			}
		}
		progress := 5 + ((index + 1) * 90 / max(1, len(payload.Issues)))
		if err := s.Store.UpdateAPITaskProgress(ctx, task, progress, fmt.Sprintf("Processed %d of %d issues.", index+1, len(payload.Issues))); err != nil {
			return err
		}
	}
	result := map[string]any{"processedAccessibleIssues": processed, "invalidOrInaccessibleIssueCount": invalid, "totalIssueCount": len(payload.Issues)}
	if len(failed) > 0 {
		result["failedAccessibleIssues"] = failed
	}
	return s.Store.CompleteAPITask(ctx, task, fmt.Sprintf("Processed %d of %d issues.", len(processed), len(payload.Issues)), result)
}

func (s *Service) bulkMoveStatus(ctx context.Context, sourceStatusID string, item store.BulkIssueMoveTaskItem) (string, error) {
	wf, err := s.Store.WorkflowForProjectAndIssueType(ctx, item.ProjectID, item.IssueTypeID)
	if err != nil {
		return "", err
	}
	allowed := map[string]bool{}
	ordered := make([]string, 0)
	add := func(id string) {
		if id != "" && !allowed[id] {
			allowed[id] = true
			ordered = append(ordered, id)
		}
	}
	for _, status := range wf.Statuses {
		add(status.StatusReference)
	}
	for _, transition := range wf.Transitions {
		for _, from := range transition.From {
			add(from)
		}
		add(transition.To)
	}
	if allowed[sourceStatusID] {
		return sourceStatusID, nil
	}
	if mapped := item.StatusMappings[sourceStatusID]; mapped != "" {
		if !allowed[mapped] {
			return "", fmt.Errorf("mapped destination status %q is not in the destination workflow", mapped)
		}
		return mapped, nil
	}
	if !item.InferStatusDefaults {
		return "", fmt.Errorf("source status %q requires a destination status mapping", sourceStatusID)
	}
	if allowed["st_todo"] {
		return "st_todo", nil
	}
	if len(ordered) == 0 {
		return "", fmt.Errorf("destination workflow has no statuses")
	}
	if len(wf.Statuses) == 0 {
		sort.Strings(ordered)
	}
	return ordered[0], nil
}

func (s *Service) executeBulkDeleteTask(ctx context.Context, task store.APITask) error {
	var payload store.BulkIssueDeleteTaskPayload
	if err := json.Unmarshal(task.Payload, &payload); err != nil {
		return fmt.Errorf("decode bulk delete operation: %w", err)
	}
	processed := make([]int64, 0, len(payload.Issues))
	failed := map[string][]string{}
	invalid := 0
	reason := "bulk delete task " + task.ID
	for index, item := range payload.Issues {
		issue, err := s.Store.IssueByIDOrKey(ctx, task.WorkspaceID, item.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			alreadyDeleted, lookupErr := s.Store.IssueDeletedByBulkTask(ctx, task.WorkspaceID, task.SubmittedBy, item.ID, task.ID)
			if lookupErr != nil {
				return lookupErr
			}
			if alreadyDeleted {
				processed = append(processed, item.JiraID)
			} else {
				invalid++
			}
		} else if err != nil {
			return err
		} else {
			visible, visibilityErr := authz.CanSeeIssue(ctx, s.Store, task.WorkspaceID, issue.ProjectID, task.SubmittedBy, issue.SecurityLevelID)
			if visibilityErr != nil {
				return visibilityErr
			}
			if !visible {
				invalid++
			} else if action, deleteErr := s.DeleteIssue(ctx, task.SubmittedBy, task.WorkspaceID, issue.ID, reason); action != nil {
				// Metadata deletion and the durable blob-cleanup intents already
				// committed. A cleanup error remains retryable by the outbox runner.
				processed = append(processed, item.JiraID)
			} else if deleteErr != nil {
				failed[strconv.FormatInt(item.JiraID, 10)] = []string{deleteErr.Error()}
			} else {
				failed[strconv.FormatInt(item.JiraID, 10)] = []string{"issue could not be deleted"}
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
