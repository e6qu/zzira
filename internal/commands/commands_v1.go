package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/e6qu/zzira/internal/adf"
	"github.com/e6qu/zzira/internal/authz"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
	"github.com/e6qu/zzira/internal/workflow"
)

// UpdateIssue applies a partial update. nil pointers = unchanged.
type UpdateIssueInput struct {
	VersionOperations map[string][]map[string]json.RawMessage
	ActorID           string
	WorkspaceID       string
	IssueIDOrKey      string
	Summary           *string
	Description       json.RawMessage // ADF; nil = unchanged
	PriorityID        *string
	AssigneeID        *string
	StatusID          *string // transitions only

	SecurityLevelID *string                    // "" = public, nil = unchanged
	Labels          *[]string                  // empty = clear, nil = unchanged
	Fields          map[string]json.RawMessage // custom fields
}

func (s *Service) visibleIssue(ctx context.Context, actorID, workspaceID, issueIDOrKey string) (*models.Issue, error) {
	issue, err := s.Store.IssueByIDOrKey(ctx, workspaceID, issueIDOrKey)
	if err != nil {
		return nil, fmt.Errorf("issue %q not found", issueIDOrKey)
	}
	visible, err := authz.CanSeeIssue(ctx, s.Store, workspaceID, issue.ProjectID, actorID, issue.SecurityLevelID)
	if err != nil {
		return nil, err
	}
	if !visible {
		return nil, fmt.Errorf("issue %q not found", issueIDOrKey)
	}
	return issue, nil
}

func (s *Service) UpdateIssue(ctx context.Context, in UpdateIssueInput) (*models.Issue, *models.Action, error) {
	issue, err := s.visibleIssue(ctx, in.ActorID, in.WorkspaceID, in.IssueIDOrKey)
	if err != nil {
		return nil, nil, err
	}
	if in.Summary != nil {
		sum := *in.Summary
		if len(sum) == 0 || len(sum) > 255 {
			return nil, nil, fmt.Errorf("summary is required (max 255 chars)")
		}
	}
	if in.AssigneeID != nil && *in.AssigneeID != "" {
		if _, err := s.Store.MemberByID(ctx, in.WorkspaceID, *in.AssigneeID); err != nil {
			return nil, nil, fmt.Errorf("assignee is not an active workspace member")
		}
	}
	if in.SecurityLevelID != nil && *in.SecurityLevelID != "" {
		scheme, err := s.Store.SecuritySchemeForProject(ctx, issue.ProjectID)
		if err != nil {
			return nil, nil, err
		}
		valid := false
		if scheme != nil {
			for _, level := range scheme.Levels {
				if level.ID == *in.SecurityLevelID {
					valid = true
					break
				}
			}
		}
		if !valid {
			return nil, nil, fmt.Errorf("security level is not available for this project")
		}
		visible, err := authz.CanSeeIssue(ctx, s.Store, in.WorkspaceID, issue.ProjectID, in.ActorID, *in.SecurityLevelID)
		if err != nil {
			return nil, nil, err
		}
		if !visible {
			return nil, nil, fmt.Errorf("security level would hide this issue from you")
		}
	}
	if err := s.validateCustomFields(ctx, issue.ProjectID, in.Fields); err != nil {
		return nil, nil, err
	}
	if in.Labels != nil {
		labels, err := normalizeLabels(*in.Labels)
		if err != nil {
			return nil, nil, err
		}
		in.Labels = &labels
	}
	issue, action, err := s.Store.UpdateIssue(ctx, in.ActorID, in.WorkspaceID, issue.ID, store.IssueUpdate{
		Summary:           in.Summary,
		Description:       in.Description,
		PriorityID:        in.PriorityID,
		AssigneeID:        in.AssigneeID,
		StatusID:          in.StatusID,
		SecurityLevelID:   in.SecurityLevelID,
		Labels:            in.Labels,
		Fields:            in.Fields,
		VersionOperations: in.VersionOperations,
	})
	if err != nil {
		return nil, nil, err
	}
	if in.SecurityLevelID != nil {
		excluded, err := authz.ExcludedMembersForLevel(ctx, s.Store, in.WorkspaceID, issue.ProjectID, *in.SecurityLevelID)
		if err != nil {
			return nil, nil, err
		}
		for _, userID := range excluded {
			if _, err := s.Store.EmitTombstone(ctx, in.WorkspaceID, issue.ID, userID, "security level applied"); err != nil {
				return nil, nil, err
			}
		}
	}
	if err := s.notifyAssignee(ctx, in, issue); err != nil {
		return nil, nil, err
	}
	return issue, action, nil
}

func (s *Service) validateCustomFields(ctx context.Context, projectID string, values map[string]json.RawMessage) error {
	if values == nil {
		return nil
	}
	fields, err := s.Store.CustomFieldsForProject(ctx, projectID)
	if err != nil {
		return err
	}
	valid := make(map[string]*models.CustomField, len(fields))
	for _, field := range fields {
		valid[field.ID] = field
	}
	for id, raw := range values {
		if id == "fixVersions" || id == "versions" {
			continue
		} // Validated transactionally by the store.
		field, ok := valid[id]
		if !ok {
			return fmt.Errorf("custom field %q is not available for this project", id)
		}
		if len(raw) > 64<<10 {
			return fmt.Errorf("custom field %q exceeds 64 KiB", id)
		}
		if string(raw) == "null" {
			continue
		}
		switch field.Type {
		case models.CustomFieldText:
			var value string
			if err := json.Unmarshal(raw, &value); err != nil {
				return fmt.Errorf("custom field %q must be text", id)
			}
		case models.CustomFieldNumber:
			var number json.Number
			if err := json.Unmarshal(raw, &number); err != nil {
				return fmt.Errorf("custom field %q must be a number", id)
			}
			value, err := strconv.ParseFloat(number.String(), 64)
			if err != nil || math.IsInf(value, 0) || math.IsNaN(value) {
				return fmt.Errorf("custom field %q must be a finite number", id)
			}
		case models.CustomFieldDatetime:
			var value string
			if err := json.Unmarshal(raw, &value); err != nil || !validCreateDatetime(value) {
				return fmt.Errorf("custom field %q must be an RFC 3339 or local date-time", id)
			}
		default:
			return fmt.Errorf("custom field %q has unsupported type %q", id, field.Type)
		}
	}
	return nil
}

func validCreateDatetime(value string) bool {
	if value == "" {
		return true
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04", "2006-01-02T15:04:05"} {
		if _, err := time.Parse(layout, value); err == nil {
			return true
		}
	}
	return false
}

func normalizeLabels(values []string) ([]string, error) {
	labels := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		label := strings.TrimSpace(value)
		if label == "" {
			continue
		}
		if len(label) > 255 || strings.IndexFunc(label, unicode.IsSpace) >= 0 {
			return nil, fmt.Errorf("labels must be at most 255 characters and contain no spaces")
		}
		if _, ok := seen[label]; ok {
			continue
		}
		seen[label] = struct{}{}
		labels = append(labels, label)
	}
	slices.Sort(labels)
	return labels, nil
}

// notifyAssignee emits a per-user notification action when an issue is
// assigned to someone other than the actor.
func (s *Service) notifyAssignee(ctx context.Context, in UpdateIssueInput, issue *models.Issue) error {
	if issue.Assignee == nil || issue.Assignee.ID == in.ActorID {
		return nil
	}
	actor, err := s.Store.UserByID(ctx, in.ActorID)
	if err != nil {
		return fmt.Errorf("actor lookup: %w", err)
	}
	_, err = s.Store.CreateNotification(ctx, in.WorkspaceID, &models.Notification{
		ID:          store.NewID("ntf"),
		WorkspaceID: in.WorkspaceID,
		TargetUser:  issue.Assignee.ID,
		ActorID:     in.ActorID,
		ActorName:   actor.DisplayName,
		Kind:        "assigned",
		EntityType:  models.EntityIssue,
		EntityID:    issue.ID,
		Message:     "assigned you " + issue.Key,
	})
	return err
}

// TransitionIssue validates and applies a workflow transition using the
// issue's project workflow (Default when unassigned).
func (s *Service) TransitionIssue(ctx context.Context, actorID, workspaceID, issueIDOrKey, transitionID string) (*models.Issue, *models.Action, error) {
	return s.transitionIssueWithUpdate(ctx, actorID, workspaceID, issueIDOrKey, transitionID, store.IssueUpdate{}, false)
}

func (s *Service) TransitionIssueWithUpdate(ctx context.Context, actorID, workspaceID, issueIDOrKey, transitionID string, update store.IssueUpdate) (*models.Issue, *models.Action, error) {
	return s.transitionIssueWithUpdate(ctx, actorID, workspaceID, issueIDOrKey, transitionID, update, false)
}

// TransitionIssueWithUpdateFromAPI preserves Jira's distinction between rules
// that block people in the UI and rules that also block REST transitions.
func (s *Service) TransitionIssueWithUpdateFromAPI(ctx context.Context, actorID, workspaceID, issueIDOrKey, transitionID string, update store.IssueUpdate) (*models.Issue, *models.Action, error) {
	return s.transitionIssueWithUpdate(ctx, actorID, workspaceID, issueIDOrKey, transitionID, update, true)
}

func (s *Service) transitionIssueWithUpdate(ctx context.Context, actorID, workspaceID, issueIDOrKey, transitionID string, update store.IssueUpdate, isAPI bool) (*models.Issue, *models.Action, error) {
	issue, err := s.visibleIssue(ctx, actorID, workspaceID, issueIDOrKey)
	if err != nil {
		return nil, nil, fmt.Errorf("issue %q not found", issueIDOrKey)
	}
	if update.Summary != nil && (len(*update.Summary) == 0 || len(*update.Summary) > 255) {
		return nil, nil, fmt.Errorf("summary is required (max 255 chars)")
	}
	if len(update.Description) > 1<<20 {
		return nil, nil, fmt.Errorf("description must be at most 1 MiB")
	}
	if err := s.validateCustomFields(ctx, issue.ProjectID, update.Fields); err != nil {
		return nil, nil, err
	}
	if update.Labels != nil {
		labels, err := normalizeLabels(*update.Labels)
		if err != nil {
			return nil, nil, err
		}
		update.Labels = &labels
	}
	wf, err := s.Store.WorkflowForProjectAndIssueType(ctx, issue.ProjectID, issue.IssueType.ID)
	if err != nil {
		return nil, nil, err
	}
	t, ok := wf.Validate(transitionID, issue.Status.ID)
	if !ok {
		return nil, nil, fmt.Errorf("transition %q is not valid from status %q", transitionID, issue.Status.Name)
	}
	context := workflow.ContextForIssue(actorID, issue)
	context.IsAPI = isAPI
	if !t.ConditionsAllow(context) {
		return nil, nil, fmt.Errorf("transition %q is not available to this user", transitionID)
	}
	requestedFields := make(map[string]bool)
	for field, changed := range map[string]bool{
		"summary": update.Summary != nil, "description": update.Description != nil,
		"priority": update.PriorityID != nil, "assignee": update.AssigneeID != nil,
		"labels": update.Labels != nil,
	} {
		if changed {
			requestedFields[field] = true
		}
	}
	for field := range update.Fields {
		requestedFields[field] = true
	}
	allowed := make(map[string]bool)
	for _, field := range t.ScreenFields() {
		allowed[field] = true
	}
	for field := range requestedFields {
		if !allowed[field] {
			return nil, nil, fmt.Errorf("field %q is not available on transition %q", field, transitionID)
		}
	}
	if update.Summary != nil {
		context.FieldPresent["summary"] = strings.TrimSpace(*update.Summary) != ""
	}
	if update.Description != nil {
		context.FieldPresent["description"] = strings.TrimSpace(adf.PlainText(update.Description)) != ""
	}
	if update.PriorityID != nil {
		context.FieldPresent["priority"] = *update.PriorityID != ""
	}
	if update.AssigneeID != nil {
		context.FieldPresent["assignee"] = *update.AssigneeID != ""
		if *update.AssigneeID != "" {
			if _, err := s.Store.MemberByID(ctx, workspaceID, *update.AssigneeID); err != nil {
				return nil, nil, fmt.Errorf("assignee is not an active workspace member")
			}
		}
	}
	if update.Labels != nil {
		context.FieldPresent["labels"] = len(*update.Labels) > 0
	}
	for field, value := range update.Fields {
		context.FieldPresent[field] = workflow.FieldValuePresent(value)
	}
	if err := t.ValidateRules(context); err != nil {
		return nil, nil, err
	}
	assigneeID, changeAssignee, err := t.AssigneeEffect(context)
	if err != nil {
		return nil, nil, err
	}
	if changeAssignee && assigneeID != "" {
		if _, err := s.Store.MemberByID(ctx, workspaceID, assigneeID); err != nil {
			return nil, nil, fmt.Errorf("workflow assignee is not an active workspace member")
		}
	}
	newStatus := t.To
	update.StatusID = &newStatus
	update.ExpectedUpdatedSeq = &issue.UpdatedSeq
	if changeAssignee {
		update.AssigneeID = &assigneeID
	}
	fieldEffects, err := t.FieldUpdateEffects()
	if err != nil {
		return nil, nil, err
	}
	for _, effect := range fieldEffects {
		if err := applyWorkflowFieldUpdate(issue, &update, effect); err != nil {
			return nil, nil, err
		}
	}
	if update.Summary != nil && (len(*update.Summary) == 0 || len(*update.Summary) > 255) {
		return nil, nil, fmt.Errorf("workflow summary update must be between 1 and 255 characters")
	}
	if update.AssigneeID != nil && *update.AssigneeID != "" {
		if _, err := s.Store.MemberByID(ctx, workspaceID, *update.AssigneeID); err != nil {
			return nil, nil, fmt.Errorf("workflow assignee is not an active workspace member")
		}
	}
	if err := s.validateCustomFields(ctx, issue.ProjectID, update.Fields); err != nil {
		return nil, nil, err
	}
	return s.Store.UpdateIssue(ctx, actorID, workspaceID, issue.ID, update)
}

func applyWorkflowFieldUpdate(issue *models.Issue, update *store.IssueUpdate, effect workflow.FieldUpdateEffect) error {
	appendText := func(current string) string {
		if effect.Mode == "replace" {
			return effect.Value
		}
		return current + effect.Value
	}
	switch effect.Field {
	case "summary":
		current := issue.Summary
		if update.Summary != nil {
			current = *update.Summary
		}
		value := appendText(current)
		update.Summary = &value
	case "description":
		current := adf.PlainText(issue.Description)
		if update.Description != nil {
			current = adf.PlainText(update.Description)
		}
		update.Description = adf.ParagraphDoc(appendText(current))
	case "labels":
		labels := append([]string(nil), issue.Labels...)
		if update.Labels != nil {
			labels = append([]string(nil), (*update.Labels)...)
		}
		values := strings.Split(effect.Value, ",")
		if strings.TrimSpace(effect.Value) == "" {
			values = []string{}
		}
		if effect.Mode == "replace" {
			labels = values
		} else {
			labels = append(labels, values...)
		}
		normalized, err := normalizeLabels(labels)
		if err != nil {
			return fmt.Errorf("workflow label update: %w", err)
		}
		update.Labels = &normalized
	case "priority":
		value := effect.Value
		update.PriorityID = &value
	default:
		if update.Fields == nil {
			update.Fields = make(map[string]json.RawMessage)
		}
		incoming := json.RawMessage(effect.Value)
		if !json.Valid(incoming) {
			incoming, _ = json.Marshal(effect.Value)
		}
		if effect.Mode == "replace" {
			update.Fields[effect.Field] = incoming
			break
		}
		current := issue.Fields[effect.Field]
		if changed, ok := update.Fields[effect.Field]; ok {
			current = changed
		}
		var currentText, incomingText string
		if json.Unmarshal(current, &currentText) != nil || json.Unmarshal(incoming, &incomingText) != nil {
			return fmt.Errorf("workflow field %q only supports append for text values", effect.Field)
		}
		update.Fields[effect.Field], _ = json.Marshal(currentText + incomingText)
	}
	return nil
}

type AddCommentInput struct {
	ActorID      string
	WorkspaceID  string
	IssueIDOrKey string
	Body         json.RawMessage // ADF; empty → empty paragraph
	PlainText    string          // form path: wrapped into an ADF paragraph
}

func (s *Service) AddComment(ctx context.Context, in AddCommentInput) (*models.Comment, *models.Action, error) {
	issue, err := s.visibleIssue(ctx, in.ActorID, in.WorkspaceID, in.IssueIDOrKey)
	if err != nil {
		return nil, nil, err
	}
	body := in.Body
	if len(body) == 0 && in.PlainText != "" {
		body = adf.ParagraphDoc(in.PlainText)
	}
	if len(body) == 0 {
		body = adf.Doc(adf.Paragraph())
	}
	return s.Store.CreateComment(ctx, in.ActorID, in.WorkspaceID, issue.ID, body)
}

func (s *Service) DeleteComment(ctx context.Context, actorID, workspaceID, commentID string) (*models.Action, error) {
	c, err := s.Store.CommentByID(ctx, workspaceID, commentID)
	if err != nil {
		return nil, fmt.Errorf("comment %q not found", commentID)
	}
	if c.AuthorID != actorID {
		return nil, fmt.Errorf("only the author may delete a comment")
	}
	return s.Store.DeleteComment(ctx, actorID, workspaceID, commentID)
}
