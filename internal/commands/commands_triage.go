package commands

import (
	"context"
	"errors"
	"fmt"

	"github.com/e6qu/zzira/internal/authz"

	"github.com/e6qu/zzira/internal/models"
)

// SetWatching adds or removes the actor as a watcher after applying the same
// issue-visibility check used by every other issue command.
func (s *Service) SetWatching(ctx context.Context, actorID, workspaceID, issueIDOrKey string, watching bool) (*models.Action, error) {
	issue, err := s.visibleIssue(ctx, actorID, workspaceID, issueIDOrKey)
	if err != nil {
		return nil, err
	}
	configuration, err := s.jiraSiteConfiguration(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	if watching && !configuration.WatchingEnabled {
		return nil, fmt.Errorf("watching is disabled for this site")
	}
	if watching {
		return s.Store.AddWatcher(ctx, actorID, workspaceID, issue.ID, actorID)
	}
	return s.Store.RemoveWatcher(ctx, actorID, workspaceID, issue.ID, actorID)
}

// SetWatcher adds or removes someone else as a watcher, which needs Manage
// watchers; the watcher must be able to see the work item to be added.
func (s *Service) SetWatcher(ctx context.Context, actorID, workspaceID, issueIDOrKey, accountID string, watching bool) (*models.Action, error) {
	if accountID == actorID {
		return s.SetWatching(ctx, actorID, workspaceID, issueIDOrKey, watching)
	}
	issue, err := s.visibleIssue(ctx, actorID, workspaceID, issueIDOrKey)
	if err != nil {
		return nil, err
	}
	if err := s.requirePermission(ctx, workspaceID, actorID, issue.ProjectID, issue.ID, "MANAGE_WATCHERS"); err != nil {
		return nil, err
	}
	if !watching {
		return s.Store.RemoveWatcher(ctx, actorID, workspaceID, issue.ID, accountID)
	}
	configuration, err := s.jiraSiteConfiguration(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	if !configuration.WatchingEnabled {
		return nil, fmt.Errorf("watching is disabled for this site")
	}
	visible, err := authz.CanSeeIssue(ctx, s.Store, workspaceID, issue.ProjectID, accountID, issue.ID, issue.SecurityLevelID)
	if err != nil {
		return nil, err
	}
	if !visible {
		return nil, ErrWatcherCannotSee
	}
	return s.Store.AddWatcher(ctx, actorID, workspaceID, issue.ID, accountID)
}

// ErrWatcherCannotSee refuses a watcher who cannot see the work item.
var ErrWatcherCannotSee = errors.New("the user can't see the work item")

// SetVoting records or removes the actor's vote after the ordinary issue
// visibility check. Jira votes are always self-service.
func (s *Service) SetVoting(ctx context.Context, actorID, workspaceID, issueIDOrKey string, voting bool) (*models.Action, error) {
	issue, err := s.visibleIssue(ctx, actorID, workspaceID, issueIDOrKey)
	if err != nil {
		return nil, err
	}
	configuration, err := s.jiraSiteConfiguration(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	if voting && !configuration.VotingEnabled {
		return nil, fmt.Errorf("voting is disabled for this site")
	}
	if voting {
		return s.Store.AddVote(ctx, actorID, workspaceID, issue.ID, actorID)
	}
	return s.Store.RemoveVote(ctx, actorID, workspaceID, issue.ID, actorID)
}

// LinkIssue creates an outward relationship from issueIDOrKey to otherIDOrKey.
// For example, selecting "blocks" renders "blocks ZZ-2" on the current issue.
func (s *Service) LinkIssue(ctx context.Context, actorID, workspaceID, issueIDOrKey, typeID, otherIDOrKey string) (*models.IssueLink, *models.Action, error) {
	issue, err := s.visibleIssue(ctx, actorID, workspaceID, issueIDOrKey)
	if err != nil {
		return nil, nil, err
	}
	configuration, err := s.jiraSiteConfiguration(ctx, workspaceID)
	if err != nil {
		return nil, nil, err
	}
	if !configuration.IssueLinkingEnabled {
		return nil, nil, fmt.Errorf("work item linking is disabled for this site")
	}
	// Jira asks for Link issues in the project of the outward work item.
	if err := s.requirePermission(ctx, workspaceID, actorID, issue.ProjectID, issue.ID, "LINK_ISSUES"); err != nil {
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
	if err := s.requirePermission(ctx, workspaceID, actorID, issue.ProjectID, issue.ID, "LINK_ISSUES"); err != nil {
		return nil, err
	}
	return s.Store.DeleteIssueLink(ctx, actorID, workspaceID, linkID)
}
