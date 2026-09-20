package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

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
	collector := &store.BulkNotificationCollector{}
	ctx = store.WithBulkNotifications(ctx, collector)
	if payload.OverrideScreenSecurity || payload.OverrideEditableFlag {
		ctx = WithOverrides(ctx, Overrides{ScreenSecurity: payload.OverrideScreenSecurity, EditableFlag: payload.OverrideEditableFlag})
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
			visible, visibilityErr := authz.CanSeeIssue(ctx, s.Store, task.WorkspaceID, issue.ProjectID, task.SubmittedBy, issue.ID, issue.SecurityLevelID)
			if visibilityErr != nil {
				return visibilityErr
			}
			if !visible {
				invalid++
			} else if editErr := s.applyBulkEdit(ctx, task, issue, item, payload.Operations); editErr != nil {
				failed[strconv.FormatInt(item.JiraID, 10)] = []string{editErr.Error()}
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
	if err := s.finishBulkNotifications(ctx, task, "edit", payload.SendBulkNotification, collector); err != nil {
		return err
	}
	return s.Store.CompleteAPITask(ctx, task, fmt.Sprintf("Processed %d of %d issues.", len(processed), len(payload.Issues)), result)
}

// applyBulkEdit changes one work item the way a bulk edit asks. The type comes
// first because it decides which workflow and which fields the work item then
// has, the fields follow, and the status last so the work item ends where the
// edit asked for rather than where its new type's workflow started it.
func (s *Service) applyBulkEdit(ctx context.Context, task store.APITask, issue *models.Issue, item store.BulkIssueTaskItem, operations []store.BulkIssueEditOperation) error {
	fields := make([]store.BulkIssueEditOperation, 0, len(operations))
	issueTypeID, statusID := "", ""
	for _, operation := range operations {
		switch operation.FieldID {
		case "issuetype":
			value, err := bulkStringValue(operation.Value)
			if err != nil {
				return fmt.Errorf("issuetype: %w", err)
			}
			issueTypeID = value
		case "status":
			value, err := bulkStringValue(operation.Value)
			if err != nil {
				return fmt.Errorf("status: %w", err)
			}
			statusID = value
		default:
			fields = append(fields, operation)
		}
	}
	if issueTypeID != "" && issueTypeID != issue.IssueType.ID {
		// Changing the type re-homes the work item exactly as a move within
		// its own project does: the same status mapping, the same required
		// fields, the same permission.
		move := store.BulkIssueMoveTaskItem{
			BulkIssueTaskItem: item, ProjectID: issue.ProjectID, IssueTypeID: issueTypeID,
			InferStatusDefaults: true, InferClassificationDefaults: true, InferFieldDefaults: true,
		}
		if _, err := s.bulkMoveIssue(ctx, task, issue, move); err != nil {
			return fmt.Errorf("issuetype: %w", err)
		}
		reloaded, err := s.Store.IssueByIDOrKey(ctx, task.WorkspaceID, issue.ID)
		if err != nil {
			return err
		}
		issue = reloaded
	}
	if len(fields) > 0 {
		update, err := bulkIssueUpdate(issue, task.SubmittedBy, fields)
		if err != nil {
			return err
		}
		if _, _, err := s.UpdateIssue(ctx, update); err != nil {
			return err
		}
	}
	if statusID == "" {
		return nil
	}
	return s.bulkEditStatus(ctx, task, issue.ID, statusID)
}

// bulkEditStatus runs the transition that leads to the status a bulk edit
// asks for. A status is reached through the work item's workflow, so one whose
// workflow offers no way there from where it stands fails and says so, rather
// than being written past its own workflow.
func (s *Service) bulkEditStatus(ctx context.Context, task store.APITask, issueID, statusID string) error {
	issue, err := s.Store.IssueByIDOrKey(ctx, task.WorkspaceID, issueID)
	if err != nil {
		return err
	}
	if issue.Status.ID == statusID {
		return nil
	}
	wf, err := s.Store.WorkflowForProjectAndIssueType(ctx, issue.ProjectID, issue.IssueType.ID)
	if err != nil {
		return err
	}
	evaluation, err := s.Store.IssueWorkflowEvaluation(ctx, task.WorkspaceID, task.SubmittedBy, issue)
	if err != nil {
		return err
	}
	evaluation.IsAPI = true
	screened := false
	for _, transition := range wf.AvailableFor(issue.Status.ID, evaluation) {
		if transition.To != statusID {
			continue
		}
		// A transition with a screen asks for values a bulk edit has no way
		// to supply, so it is not taken silently.
		if len(transition.ScreenFields()) > 0 {
			screened = true
			continue
		}
		_, _, err := s.TransitionIssueWithUpdateFromAPI(ctx, task.SubmittedBy, task.WorkspaceID, issue.ID, transition.ID, store.IssueUpdate{TaskID: task.ID})
		return err
	}
	if screened {
		return fmt.Errorf("status: reaching status %s asks for a transition screen, which a bulk edit cannot fill", statusID)
	}
	return fmt.Errorf("status: no transition from %s leads to status %s", issue.Status.Name, statusID)
}

// finishBulkNotifications sends the bulk change emails a task gathered when
// the request asked for them; otherwise the task's issue events send none.
func (s *Service) finishBulkNotifications(ctx context.Context, task store.APITask, operation string, send bool, collector *store.BulkNotificationCollector) error {
	if !send {
		return nil
	}
	return s.Store.WriteBulkNotificationEmails(ctx, task.WorkspaceID, task.ID, operation, collector)
}

func (s *Service) executeBulkTransitionTask(ctx context.Context, task store.APITask) error {
	var payload store.BulkIssueTransitionTaskPayload
	if err := json.Unmarshal(task.Payload, &payload); err != nil {
		return fmt.Errorf("decode bulk transition operation: %w", err)
	}
	collector := &store.BulkNotificationCollector{}
	ctx = store.WithBulkNotifications(ctx, collector)
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
				visible, visibilityErr := authz.CanSeeIssue(ctx, s.Store, task.WorkspaceID, issue.ProjectID, task.SubmittedBy, issue.ID, issue.SecurityLevelID)
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
	if err := s.finishBulkNotifications(ctx, task, "transition", payload.SendBulkNotification, collector); err != nil {
		return err
	}
	return s.Store.CompleteAPITask(ctx, task, fmt.Sprintf("Processed %d of %d issues.", len(processed), len(payload.Issues)), result)
}

func (s *Service) executeBulkMoveTask(ctx context.Context, task store.APITask) error {
	var payload store.BulkIssueMoveTaskPayload
	if err := json.Unmarshal(task.Payload, &payload); err != nil {
		return fmt.Errorf("decode bulk move operation: %w", err)
	}
	collector := &store.BulkNotificationCollector{}
	ctx = store.WithBulkNotifications(ctx, collector)
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
				visible, visibilityErr := authz.CanSeeIssue(ctx, s.Store, task.WorkspaceID, issue.ProjectID, task.SubmittedBy, issue.ID, issue.SecurityLevelID)
				if visibilityErr != nil {
					return visibilityErr
				}
				if !visible {
					invalid++
				} else if moved, moveErr := s.bulkMoveIssue(ctx, task, issue, item); moveErr != nil {
					failed[strconv.FormatInt(item.JiraID, 10)] = []string{moveErr.Error()}
					processed = append(processed, moved...)
				} else {
					processed = append(processed, moved...)
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
	if err := s.finishBulkNotifications(ctx, task, "move", payload.SendBulkNotification, collector); err != nil {
		return err
	}
	return s.Store.CompleteAPITask(ctx, task, fmt.Sprintf("Processed %d of %d issues.", len(processed), len(payload.Issues)), result)
}

// bulkMoveIssue moves one work item and, across projects, the sub-tasks that
// move with it. It returns the Jira ids moved.
func (s *Service) bulkMoveIssue(ctx context.Context, task store.APITask, issue *models.Issue, item store.BulkIssueMoveTaskItem) ([]int64, error) {
	// Jira asks for Move issues where the work item is, and Create issues
	// where it goes.
	if err := s.requirePermission(ctx, task.WorkspaceID, task.SubmittedBy, issue.ProjectID, issue.ID, "MOVE_ISSUES"); err != nil {
		return nil, err
	}
	if issue.ProjectID != item.ProjectID {
		if err := s.requirePermission(ctx, task.WorkspaceID, task.SubmittedBy, item.ProjectID, "", "CREATE_ISSUES"); err != nil {
			return nil, err
		}
	}
	children, err := s.Store.ChildIssues(ctx, task.WorkspaceID, issue.ID)
	if err != nil {
		return nil, err
	}
	crossProject := issue.ProjectID != item.ProjectID
	// Sub-tasks move with their parent, each under a sub-task type the
	// destination offers.
	childTypes := map[string]string{}
	if crossProject && len(children) > 0 {
		destinationTypes, typeErr := s.Store.ProjectIssueTypes(ctx, task.WorkspaceID, item.ProjectID, nil)
		if typeErr != nil {
			return nil, typeErr
		}
		subtaskTypes := map[string]bool{}
		firstSubtaskType := ""
		for _, issueType := range destinationTypes {
			if issueType.Subtask {
				subtaskTypes[issueType.ID] = true
				if firstSubtaskType == "" {
					firstSubtaskType = issueType.ID
				}
			}
		}
		for _, child := range children {
			switch {
			case subtaskTypes[child.IssueType.ID]:
				childTypes[child.ID] = child.IssueType.ID
			case item.InferSubtaskTypeDefault && firstSubtaskType != "":
				childTypes[child.ID] = firstSubtaskType
			default:
				return nil, fmt.Errorf("sub-task %s needs a sub-task type the destination project offers; set inferSubtaskTypeDefault", child.Key)
			}
		}
	}
	move, err := s.bulkMoveTarget(ctx, task, issue, item, item.IssueTypeID)
	if err != nil {
		return nil, err
	}
	move.ParentID, move.TaskID = item.ParentID, task.ID
	updated, action, err := s.Store.MoveIssue(ctx, task.SubmittedBy, task.WorkspaceID, issue.ID, move)
	if err != nil {
		return nil, err
	}
	if err = s.deliverIssueEvent(ctx, task.WorkspaceID, task.SubmittedBy, updated, action, 9, "issue_moved", "moved"); err != nil {
		return nil, err
	}
	moved := []int64{item.JiraID}
	if !crossProject {
		return moved, nil
	}
	for _, child := range children {
		childItem := item
		childItem.IssueTypeID, childItem.ParentID = childTypes[child.ID], updated.ID
		childMove, childErr := s.bulkMoveTarget(ctx, task, child, childItem, childItem.IssueTypeID)
		if childErr != nil {
			return moved, fmt.Errorf("sub-task %s: %w", child.Key, childErr)
		}
		childMove.ParentID, childMove.TaskID = updated.ID, task.ID
		movedChild, childAction, childErr := s.Store.MoveIssue(ctx, task.SubmittedBy, task.WorkspaceID, child.ID, childMove)
		if childErr != nil {
			return moved, fmt.Errorf("sub-task %s: %w", child.Key, childErr)
		}
		if childErr = s.deliverIssueEvent(ctx, task.WorkspaceID, task.SubmittedBy, movedChild, childAction, 9, "issue_moved", "moved"); childErr != nil {
			return moved, childErr
		}
		moved = append(moved, movedChild.JiraID)
	}
	return moved, nil
}

// bulkMoveTarget resolves a work item's destination status, classification
// and required field values.
func (s *Service) bulkMoveTarget(ctx context.Context, task store.APITask, issue *models.Issue, item store.BulkIssueMoveTaskItem, issueTypeID string) (store.IssueMove, error) {
	item.IssueTypeID = issueTypeID
	move := store.IssueMove{ProjectID: item.ProjectID, IssueTypeID: issueTypeID}
	statusID, err := s.bulkMoveStatus(ctx, issue.Status.ID, item)
	if err != nil {
		return move, err
	}
	move.StatusID = statusID

	level, err := s.Store.IssueClassificationLevel(ctx, task.WorkspaceID, issue.ID)
	if err != nil {
		return move, err
	}
	switch {
	case item.InferClassificationDefaults && level == "":
		destinationDefault, defaultErr := s.Store.ProjectDefaultClassificationLevel(ctx, task.WorkspaceID, item.ProjectID)
		if defaultErr != nil {
			return move, defaultErr
		}
		move.ClassificationLevel = &destinationDefault
	case !item.InferClassificationDefaults && level != "":
		mapped, ok := item.ClassificationMappings[level]
		if !ok {
			return move, fmt.Errorf("classification %q requires a target classification", level)
		}
		move.ClassificationLevel = &mapped
	}

	behaviour, err := s.Store.ResolveFieldBehaviour(ctx, task.WorkspaceID, item.ProjectID, issueTypeID)
	if err != nil {
		return move, err
	}
	customFields, err := s.Store.CustomFieldsForWorkspace(ctx, task.WorkspaceID)
	if err != nil {
		return move, err
	}
	fieldTypes := map[string]string{}
	for _, field := range customFields {
		fieldTypes[field.ID] = field.Type
	}
	required := make([]string, 0)
	for fieldID, rule := range behaviour {
		if rule.IsRequired {
			required = append(required, fieldID)
		}
	}
	sort.Strings(required)
	for _, fieldID := range required {
		present := bulkMoveFieldPresent(issue, fieldID)
		supplied, hasValue := item.MandatoryFields[fieldID]
		switch {
		case item.InferFieldDefaults:
			if !present {
				return move, fmt.Errorf("field %s is required in the destination and has no value", fieldID)
			}
		case present && (!hasValue || supplied.Retain):
		case !hasValue:
			return move, fmt.Errorf("field %s is required in the destination; provide it in targetMandatoryFields", fieldID)
		default:
			fieldType, custom := fieldTypes[fieldID]
			if !custom {
				return move, fmt.Errorf("field %s cannot be set by a bulk move", fieldID)
			}
			value, valueErr := bulkMoveFieldValue(fieldType, supplied)
			if valueErr != nil {
				return move, fmt.Errorf("field %s: %w", fieldID, valueErr)
			}
			if move.Fields == nil {
				move.Fields = map[string]json.RawMessage{}
			}
			move.Fields[fieldID] = value
		}
	}
	if len(move.Fields) > 0 {
		// Option values are stored by option id, as a create or edit stores them.
		if err = s.normalizeOptionFields(ctx, task.WorkspaceID, item.ProjectID, issueTypeID, move.Fields, nil); err != nil {
			return move, err
		}
		if err = s.validateCustomFields(ctx, item.ProjectID, move.Fields); err != nil {
			return move, err
		}
	}
	return move, nil
}

// bulkMoveFieldPresent reports whether a work item already holds a value for
// a field.
func bulkMoveFieldPresent(issue *models.Issue, fieldID string) bool {
	switch fieldID {
	case "summary":
		return strings.TrimSpace(issue.Summary) != ""
	case "labels":
		return len(issue.Labels) > 0
	}
	raw, ok := issue.Fields[fieldID]
	if !ok {
		return false
	}
	trimmed := strings.TrimSpace(string(raw))
	return trimmed != "" && trimmed != "null" && trimmed != `""` && trimmed != "[]" && trimmed != "{}"
}

// bulkMoveFieldValue turns a bulk move's raw values or ADF document into the
// stored value of a custom field of the given type.
func bulkMoveFieldValue(fieldType string, supplied store.MoveMandatoryField) (json.RawMessage, error) {
	if supplied.ADF {
		return supplied.Value, nil
	}
	var values []string
	if err := json.Unmarshal(supplied.Value, &values); err != nil || len(values) == 0 {
		return nil, fmt.Errorf("needs at least one value")
	}
	var value any
	switch fieldType {
	case models.CustomFieldSelect:
		value = map[string]string{"value": values[0]}
	case models.CustomFieldMultiSelect:
		options := make([]map[string]string, 0, len(values))
		for _, option := range values {
			options = append(options, map[string]string{"value": option})
		}
		value = options
	case models.CustomFieldNumber:
		number, err := strconv.ParseFloat(values[0], 64)
		if err != nil {
			return nil, fmt.Errorf("needs a number")
		}
		value = number
	case models.CustomFieldLabels, models.CustomFieldMultiUser, models.CustomFieldMultiGroup, models.CustomFieldMultiVersion:
		value = values
	default:
		value = values[0]
	}
	return json.Marshal(value)
}

func (s *Service) bulkMoveStatus(ctx context.Context, sourceStatusID string, item store.BulkIssueMoveTaskItem) (string, error) {
	wf, err := s.Store.WorkflowForProjectAndIssueType(ctx, item.ProjectID, item.IssueTypeID)
	if err != nil {
		return "", err
	}
	ordered := wf.StatusIDs()
	allowed := make(map[string]bool, len(ordered))
	for _, id := range ordered {
		allowed[id] = true
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
	if len(ordered) == 0 {
		return "", fmt.Errorf("destination workflow has no statuses")
	}
	// Inferred work starts where the destination workflow creates it.
	return wf.InitialStatus(), nil
}

func (s *Service) executeBulkDeleteTask(ctx context.Context, task store.APITask) error {
	var payload store.BulkIssueDeleteTaskPayload
	if err := json.Unmarshal(task.Payload, &payload); err != nil {
		return fmt.Errorf("decode bulk delete operation: %w", err)
	}
	collector := &store.BulkNotificationCollector{}
	ctx = store.WithBulkNotifications(ctx, collector)
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
			visible, visibilityErr := authz.CanSeeIssue(ctx, s.Store, task.WorkspaceID, issue.ProjectID, task.SubmittedBy, issue.ID, issue.SecurityLevelID)
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
	if err := s.finishBulkNotifications(ctx, task, "delete", payload.SendBulkNotification, collector); err != nil {
		return err
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
		case "timeoriginalestimate", "timeestimate":
			var seconds int64
			if err := json.Unmarshal(operation.Value, &seconds); err != nil || seconds < 0 {
				return update, fmt.Errorf("%s must be a duration in seconds", operation.FieldID)
			}
			if operation.FieldID == "timeoriginalestimate" {
				update.OriginalEstimate = &seconds
			} else {
				update.RemainingEstimate = &seconds
			}
		case "duedate":
			value, err := bulkStringValue(operation.Value)
			if err != nil {
				return update, fmt.Errorf("duedate: %w", err)
			}
			update.DueDate = &value
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
