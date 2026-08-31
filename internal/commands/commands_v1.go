package commands

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/e6qu/zzira/internal/adf"
	"github.com/e6qu/zzira/internal/authz"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

// UpdateIssue applies a partial update. nil pointers = unchanged.
type UpdateIssueInput struct {
	ActorID      string
	WorkspaceID  string
	IssueIDOrKey string
	Summary      *string
	Description  json.RawMessage // ADF; nil = unchanged
	PriorityID   *string
	AssigneeID   *string
	StatusID     *string // transitions only

	SecurityLevelID *string                    // "" = public, nil = unchanged
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
	issue, action, err := s.Store.UpdateIssue(ctx, in.ActorID, in.WorkspaceID, issue.ID, store.IssueUpdate{
		Summary:         in.Summary,
		Description:     in.Description,
		PriorityID:      in.PriorityID,
		AssigneeID:      in.AssigneeID,
		StatusID:        in.StatusID,
		SecurityLevelID: in.SecurityLevelID,
		Fields:          in.Fields,
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
	issue, err := s.visibleIssue(ctx, actorID, workspaceID, issueIDOrKey)
	if err != nil {
		return nil, nil, fmt.Errorf("issue %q not found", issueIDOrKey)
	}
	wf, err := s.Store.WorkflowForProject(ctx, issue.ProjectID)
	if err != nil {
		return nil, nil, err
	}
	t, ok := wf.Validate(transitionID, issue.Status.ID)
	if !ok {
		return nil, nil, fmt.Errorf("transition %q is not valid from status %q", transitionID, issue.Status.Name)
	}
	newStatus := t.To
	return s.Store.UpdateIssue(ctx, actorID, workspaceID, issue.ID, store.IssueUpdate{StatusID: &newStatus})
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
