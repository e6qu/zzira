package commands

import (
	"context"
	"fmt"

	"github.com/e6qu/zzira/internal/models"
)

// SetWatching adds or removes the actor as a watcher after applying the same
// issue-visibility check used by every other issue command.
func (s *Service) SetWatching(ctx context.Context, actorID, workspaceID, issueIDOrKey string, watching bool) (*models.Action, error) {
	issue, err := s.visibleIssue(ctx, actorID, workspaceID, issueIDOrKey)
	if err != nil {
		return nil, err
	}
	if watching {
		return s.Store.AddWatcher(ctx, actorID, workspaceID, issue.ID, actorID)
	}
	return s.Store.RemoveWatcher(ctx, actorID, workspaceID, issue.ID, actorID)
}

// LinkIssue creates an outward relationship from issueIDOrKey to otherIDOrKey.
// For example, selecting "blocks" renders "blocks ZZ-2" on the current issue.
func (s *Service) LinkIssue(ctx context.Context, actorID, workspaceID, issueIDOrKey, typeID, otherIDOrKey string) (*models.IssueLink, *models.Action, error) {
	issue, err := s.visibleIssue(ctx, actorID, workspaceID, issueIDOrKey)
	if err != nil {
		return nil, nil, err
	}
	other, err := s.visibleIssue(ctx, actorID, workspaceID, otherIDOrKey)
	if err != nil {
		return nil, nil, fmt.Errorf("linked issue %q not found", otherIDOrKey)
	}
	return s.Store.CreateIssueLink(ctx, actorID, workspaceID, typeID, other.ID, issue.ID)
}

// DeleteIssueLink removes only a link attached to an issue the actor can see;
// both linked issues must remain visible to avoid leaking restricted work.
func (s *Service) DeleteIssueLink(ctx context.Context, actorID, workspaceID, issueIDOrKey, linkID string) (*models.Action, error) {
	issue, err := s.visibleIssue(ctx, actorID, workspaceID, issueIDOrKey)
	if err != nil {
		return nil, err
	}
	link, err := s.Store.IssueLinkByID(ctx, workspaceID, linkID)
	if err != nil || (link.InwardID != issue.ID && link.OutwardID != issue.ID) {
		return nil, fmt.Errorf("issue link %q not found", linkID)
	}
	otherID := link.InwardID
	if otherID == issue.ID {
		otherID = link.OutwardID
	}
	if _, err := s.visibleIssue(ctx, actorID, workspaceID, otherID); err != nil {
		return nil, fmt.Errorf("issue link %q not found", linkID)
	}
	return s.Store.DeleteIssueLink(ctx, actorID, workspaceID, linkID)
}
