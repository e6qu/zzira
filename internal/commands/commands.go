// Package commands is the single mutation layer. Both edges (web + REST) call
// into it; nothing else may write state or append actions.
package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/e6qu/zzira/internal/attachments"
	"github.com/e6qu/zzira/internal/authz"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

type Service struct {
	Store *store.Store
	Blobs attachments.Store
}

type CreateIssueInput struct {
	ActorID                   string
	ReporterID                string
	WorkspaceID               string
	ProjectIDOrKey            string
	Summary                   string
	Description               string // plain text in V0; stored as an ADF paragraph
	DescriptionADF            json.RawMessage
	IssueTypeID               string
	ParentIDOrKey             string
	PriorityID                string
	AssigneeID                string
	UseProjectDefaultAssignee bool
	SecurityLevelID           string
	Labels                    []string
	Fields                    map[string]json.RawMessage
}

// DeleteIssue removes the issue transactionally, then cleans up attachment
// blobs whose metadata was cascade-deleted with it.
func (s *Service) DeleteIssue(ctx context.Context, actorID, workspaceID, issueIDOrKey, reason string) (*models.Action, error) {
	issue, err := s.visibleIssue(ctx, actorID, workspaceID, issueIDOrKey)
	if err != nil {
		return nil, err
	}
	children, err := s.Store.ChildIssues(ctx, workspaceID, issue.ID)
	if err != nil {
		return nil, err
	}
	if len(children) > 0 {
		return nil, fmt.Errorf("move or delete sub-tasks before deleting their parent")
	}
	action, blobRefs, err := s.Store.DeleteIssue(ctx, actorID, workspaceID, issue.ID, reason)
	if err != nil {
		return nil, err
	}
	if s.Blobs == nil && len(blobRefs) > 0 {
		return action, fmt.Errorf("issue deleted but %d attachment blobs could not be cleaned up: storage not configured", len(blobRefs))
	}
	var cleanupErr error
	for _, ref := range blobRefs {
		if err := s.Blobs.Delete(ctx, ref); err != nil {
			if deferErr := s.Store.DeferAttachmentBlobDeletion(ctx, ref, err.Error(), 1); deferErr != nil {
				cleanupErr = errors.Join(cleanupErr, fmt.Errorf("defer attachment blob %q after %v: %w", ref, err, deferErr))
			}
			continue
		}
		if err := s.Store.CompleteAttachmentBlobDeletion(ctx, ref); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("complete attachment blob cleanup %q: %w", ref, err))
		}
	}
	return action, cleanupErr
}

func (s *Service) CreateIssue(ctx context.Context, in CreateIssueInput) (*models.Issue, *models.Action, error) {
	if in.ActorID == "" {
		return nil, nil, fmt.Errorf("no actor")
	}
	in.Summary = strings.TrimSpace(in.Summary)
	if len(in.Summary) == 0 || len(in.Summary) > 255 {
		return nil, nil, fmt.Errorf("summary is required (max 255 chars)")
	}
	if len(in.Description) > 1<<20 {
		return nil, nil, fmt.Errorf("description must be at most 1 MiB")
	}
	if in.IssueTypeID == "" {
		return nil, nil, fmt.Errorf("issue type is required")
	}
	project, err := s.Store.ProjectByIDOrKey(ctx, in.WorkspaceID, in.ProjectIDOrKey)
	if err != nil {
		return nil, nil, fmt.Errorf("project %q not found in workspace", in.ProjectIDOrKey)
	}
	issueType, err := s.Store.IssueTypeByIDOrName(ctx, in.IssueTypeID)
	if err != nil {
		return nil, nil, fmt.Errorf("issue type %q not found", in.IssueTypeID)
	}
	configuration, err := s.jiraSiteConfiguration(ctx, in.WorkspaceID)
	if err != nil {
		return nil, nil, err
	}
	if issueType.Subtask && !configuration.SubTasksEnabled {
		return nil, nil, fmt.Errorf("subtasks are disabled for this site")
	}
	if err = s.enforceFieldConfiguration(ctx, in, project.ID, issueType.ID); err != nil {
		return nil, nil, err
	}
	parentID := ""
	if issueType.Subtask {
		if strings.TrimSpace(in.ParentIDOrKey) == "" {
			return nil, nil, fmt.Errorf("parent is required for a sub-task")
		}
		parent, err := s.visibleIssue(ctx, in.ActorID, in.WorkspaceID, in.ParentIDOrKey)
		if err != nil || parent.ProjectID != project.ID {
			return nil, nil, fmt.Errorf("parent must be a visible work item in this project")
		}
		if parent.IssueType.Subtask {
			return nil, nil, fmt.Errorf("a sub-task cannot be the parent of another sub-task")
		}
		parentID = parent.ID
	} else if strings.TrimSpace(in.ParentIDOrKey) != "" {
		return nil, nil, fmt.Errorf("parent is only available for sub-tasks")
	}
	priorityID := ""
	if in.PriorityID != "" {
		priority, err := s.Store.PriorityByIDOrName(ctx, in.PriorityID)
		if err != nil {
			return nil, nil, fmt.Errorf("priority %q not found", in.PriorityID)
		}
		priorityID = priority.ID
	}
	if in.UseProjectDefaultAssignee && in.AssigneeID == "-1" {
		in.AssigneeID = ""
	}
	if in.UseProjectDefaultAssignee && in.Fields != nil {
		if raw, provided := in.Fields["components"]; provided {
			componentAssignee, selected, err := s.Store.ComponentDefaultAssignee(ctx, in.WorkspaceID, project.ID, raw)
			if err != nil {
				return nil, nil, err
			}
			if selected {
				in.AssigneeID = componentAssignee
				in.UseProjectDefaultAssignee = false
			}
		}
	}
	if in.AssigneeID != "" {
		if _, err := s.Store.MemberByID(ctx, in.WorkspaceID, in.AssigneeID); err != nil {
			return nil, nil, fmt.Errorf("assignee is not an active workspace member")
		}
	}
	if in.UseProjectDefaultAssignee && project.AssigneeType == "PROJECT_LEAD" {
		if _, err := s.Store.MemberByID(ctx, in.WorkspaceID, project.LeadAccountID); err != nil {
			return nil, nil, fmt.Errorf("project lead is not an active workspace member")
		}
		in.AssigneeID = project.LeadAccountID
	}
	if in.AssigneeID == "" && !configuration.UnassignedIssuesAllowed {
		return nil, nil, fmt.Errorf("unassigned work items are disabled for this site")
	}
	if in.SecurityLevelID != "" {
		scheme, err := s.Store.SecuritySchemeForProject(ctx, project.ID)
		if err != nil {
			return nil, nil, err
		}
		valid := false
		if scheme != nil {
			for _, level := range scheme.Levels {
				if level.ID == in.SecurityLevelID {
					valid = true
					break
				}
			}
		}
		if !valid {
			return nil, nil, fmt.Errorf("security level is not available for this project")
		}
		visible, err := authz.CanSeeIssue(ctx, s.Store, in.WorkspaceID, project.ID, in.ActorID, "", in.SecurityLevelID)
		if err != nil {
			return nil, nil, err
		}
		if !visible {
			return nil, nil, fmt.Errorf("security level would hide this issue from you")
		}
	}
	if err := s.validateCustomFields(ctx, project.ID, in.Fields); err != nil {
		return nil, nil, err
	}
	labels, err := normalizeLabels(in.Labels)
	if err != nil {
		return nil, nil, err
	}
	description := plainTextToADF(in.Description)
	if in.DescriptionADF != nil {
		if len(in.DescriptionADF) > 1<<20 {
			return nil, nil, fmt.Errorf("description must be at most 1 MiB")
		}
		if string(in.DescriptionADF) != "null" {
			var doc struct {
				Type    string            `json:"type"`
				Version int               `json:"version"`
				Content []json.RawMessage `json:"content"`
			}
			if err := json.Unmarshal(in.DescriptionADF, &doc); err != nil || doc.Type != "doc" || doc.Version != 1 {
				return nil, nil, fmt.Errorf("description must be an ADF document with type doc and version 1")
			}
		}
		description = in.DescriptionADF
	}
	issue, action, err := s.Store.CreateIssueForReporter(ctx, in.ActorID, in.ReporterID, project.ID, in.Summary,
		description, "st_todo", issueType.ID, priorityID, in.AssigneeID, labels, in.Fields, in.SecurityLevelID, parentID)
	if err != nil {
		return nil, nil, err
	}
	if err = s.deliverIssueEvent(ctx, in.WorkspaceID, in.ActorID, issue, action, 1, "issue_created", "created"); err != nil {
		return issue, action, err
	}
	return issue, action, nil
}

func (s *Service) deliverIssueEvent(ctx context.Context, workspaceID, actorID string, issue *models.Issue, action *models.Action, eventID int64, kind, verb string) error {
	if issue == nil || action == nil {
		return nil
	}
	return s.Store.DeliverIssueNotification(ctx, workspaceID, actorID, issue.ID, action.Seq, eventID, kind, verb+" "+issue.Key)
}

// plainTextToADF wraps plain text into the minimal ADF document. Replaced by the
// real ADF edge in V1/V3.
func plainTextToADF(text string) json.RawMessage {
	doc := map[string]any{
		"type":    "doc",
		"version": 1,
	}
	if text == "" {
		b, _ := json.Marshal(doc)
		return b
	}
	doc["content"] = []map[string]any{{
		"type": "paragraph",
		"content": []map[string]any{{
			"type": "text",
			"text": text,
		}},
	}}
	b, _ := json.Marshal(doc)
	return b
}

// enforceFieldConfiguration applies the project's field configuration to a
// create request, so the command path rejects exactly what the form advertises
// rather than leaving required and hidden as cosmetic labels.
func (s *Service) enforceFieldConfiguration(ctx context.Context, in CreateIssueInput, projectID, issueTypeID string) error {
	rules, err := s.Store.ResolveFieldBehaviour(ctx, in.WorkspaceID, projectID, issueTypeID)
	if err != nil {
		return err
	}
	if len(rules) == 0 {
		return nil
	}
	supplied := map[string]bool{
		"summary":     strings.TrimSpace(in.Summary) != "",
		"description": strings.TrimSpace(in.Description) != "" || len(in.DescriptionADF) > 0,
		"assignee":    in.AssigneeID != "" && in.AssigneeID != "-1",
		"priority":    in.PriorityID != "",
		"parent":      strings.TrimSpace(in.ParentIDOrKey) != "",
		"security":    in.SecurityLevelID != "",
		// The browser create form posts an empty labels input, and
		// strings.Split("", ",") yields one empty entry, so count real values.
		"labels": hasNonEmptyValue(in.Labels),
	}
	for field, raw := range in.Fields {
		supplied[field] = supplied[field] || suppliedFieldValue(raw)
	}
	for field, rule := range rules {
		// Summary and the context fields are required by the command path
		// itself, so a configuration can neither relax nor hide them.
		if field == "summary" {
			continue
		}
		if rule.IsRequired && !supplied[field] {
			return fmt.Errorf("%s is required by the field configuration for this work type", field)
		}
		if rule.IsHidden && supplied[field] {
			return fmt.Errorf("%s is hidden by the field configuration for this work type", field)
		}
	}
	return nil
}

func hasNonEmptyValue(values []string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

func suppliedFieldValue(raw json.RawMessage) bool {
	value := strings.TrimSpace(string(raw))
	switch value {
	case "", "null", `""`, "[]", "{}":
		return false
	}
	return true
}
