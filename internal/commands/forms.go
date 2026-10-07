package commands

import (
	"context"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

// ChangeIssueForm applies the same edit gate to the browser and Forms API.
func (s *Service) ChangeIssueForm(ctx context.Context, actorID, workspaceID, issueIDOrKey, formID string, in store.IssueFormChange) (*models.IssueForm, error) {
	issue, err := s.visibleIssue(ctx, actorID, workspaceID, issueIDOrKey)
	if err != nil {
		return nil, err
	}
	if err := s.requirePermission(ctx, workspaceID, actorID, issue.ProjectID, issue.ID, "EDIT_ISSUES"); err != nil {
		return nil, err
	}
	if err := s.requireEditable(ctx, issue); err != nil {
		return nil, err
	}
	return s.Store.ChangeIssueForm(ctx, actorID, workspaceID, issue.ID, formID, in)
}
