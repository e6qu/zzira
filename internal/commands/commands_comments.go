package commands

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

var (
	// ErrCommentPermission is a comment change the actor's permissions do not allow.
	ErrCommentPermission = errors.New("you do not have permission to change this comment")
	// ErrIssueArchived is a change to an archived issue, which is read-only.
	ErrIssueArchived = errors.New("the issue is archived and cannot be changed")
)

// UpdateCommentInput replaces a comment's body and, optionally, its visibility.
type UpdateCommentInput struct {
	ActorID       string
	WorkspaceID   string
	CommentID     string
	Body          json.RawMessage
	SetVisibility bool
	Visibility    store.CommentVisibility
}

// commentPermission applies Jira's pair of comment permissions: the "all"
// permission for anyone's comment, the "own" one for the actor's own comment.
func (s *Service) commentPermission(ctx context.Context, actorID, workspaceID string, issue *models.Issue, c *models.Comment, all, own string) (bool, error) {
	allowed, err := s.Store.HasProjectPermission(ctx, workspaceID, actorID, issue.ProjectID, issue.ID, all)
	if err != nil || allowed {
		return allowed, err
	}
	if c.AuthorID != actorID {
		return false, nil
	}
	return s.Store.HasProjectPermission(ctx, workspaceID, actorID, issue.ProjectID, issue.ID, own)
}

// UpdateComment edits a comment for someone with Edit all comments, or Edit own
// comments on their own comment.
func (s *Service) UpdateComment(ctx context.Context, in UpdateCommentInput) (*models.Comment, *models.Action, error) {
	c, err := s.Store.CommentByRef(ctx, in.WorkspaceID, in.CommentID)
	if err != nil {
		return nil, nil, err
	}
	issue, err := s.visibleIssue(ctx, in.ActorID, in.WorkspaceID, c.IssueID)
	if err != nil {
		return nil, nil, err
	}
	allowed, err := s.commentPermission(ctx, in.ActorID, in.WorkspaceID, issue, c, "EDIT_ALL_COMMENTS", "EDIT_OWN_COMMENTS")
	if err != nil {
		return nil, nil, err
	}
	if !allowed {
		return nil, nil, ErrCommentPermission
	}
	comment, action, err := s.Store.UpdateComment(ctx, in.ActorID, in.WorkspaceID, c.ID, in.Body, in.SetVisibility, in.Visibility)
	if err != nil {
		return nil, nil, err
	}
	if err = s.deliverIssueEvent(ctx, in.WorkspaceID, in.ActorID, issue, action, 7, "issue_comment_edited", "edited a comment on"); err != nil {
		return comment, action, err
	}
	return comment, action, nil
}
