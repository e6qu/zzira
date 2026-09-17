package commands

import (
	"context"
	"errors"
	"fmt"

	"github.com/e6qu/zzira/internal/models"
)

// SetIssueRank repositions a work item on a board. Moving it to another
// column runs a workflow transition into that column's status, as Jira does;
// ranking needs Schedule issues.
func (s *Service) SetIssueRank(ctx context.Context, actorID, workspaceID, issueIDOrKey, beforeID, afterID, newStatusID string) error {
	issue, err := s.visibleIssue(ctx, actorID, workspaceID, issueIDOrKey)
	if err != nil {
		return err
	}
	if err := s.requirePermission(ctx, workspaceID, actorID, issue.ProjectID, issue.ID, "SCHEDULE_ISSUES"); err != nil {
		return err
	}
	statusID := issue.Status.ID
	if newStatusID != "" && newStatusID != statusID {
		if err := s.transitionToStatus(ctx, actorID, workspaceID, issue, newStatusID); err != nil {
			return err
		}
		statusID = newStatusID
	}
	if beforeID != "" && beforeID == issue.ID {
		beforeID = "" // dragging onto itself is a no-op position
	}
	if afterID != "" && afterID == issue.ID {
		afterID = ""
	}
	rank, err := s.Store.RankBetween(ctx, workspaceID, issue.ProjectID, statusID, beforeID, afterID)
	if err != nil {
		return err
	}
	_, err = s.Store.SetIssueRank(ctx, actorID, workspaceID, issue.ID, rank)
	return err
}

// ErrNoTransitionToStatus refuses a board move no workflow transition makes.
var ErrNoTransitionToStatus = errors.New("no workflow transition moves this work item to that status")

// transitionToStatus runs the first transition from the work item's status
// into statusID that the actor may take.
func (s *Service) transitionToStatus(ctx context.Context, actorID, workspaceID string, issue *models.Issue, statusID string) error {
	wf, err := s.Store.WorkflowForProjectAndIssueType(ctx, issue.ProjectID, issue.IssueType.ID)
	if err != nil {
		return err
	}
	var refusal error
	for _, transition := range wf.Available(issue.Status.ID) {
		if transition.To != statusID {
			continue
		}
		_, _, err := s.TransitionIssue(ctx, actorID, workspaceID, issue.ID, transition.ID)
		if err == nil {
			return nil
		}
		if errors.Is(err, ErrPermission) {
			return err
		}
		refusal = err
	}
	if refusal != nil {
		return refusal
	}
	return fmt.Errorf("%s: %w", issue.Key, ErrNoTransitionToStatus)
}

// RankAround ranks a work item just before or after another, for the Jira
// Software rank APIs. Ranking needs Schedule issues.
func (s *Service) RankAround(ctx context.Context, actorID, workspaceID, issueIDOrKey, beforeID, afterID string) error {
	issue, err := s.visibleIssue(ctx, actorID, workspaceID, issueIDOrKey)
	if err != nil {
		return err
	}
	if err := s.requirePermission(ctx, workspaceID, actorID, issue.ProjectID, issue.ID, "SCHEDULE_ISSUES"); err != nil {
		return err
	}
	rank, err := s.Store.RankAround(ctx, workspaceID, issue.ID, beforeID, afterID)
	if err != nil {
		return err
	}
	_, err = s.Store.SetIssueRank(ctx, actorID, workspaceID, issue.ID, rank)
	return err
}
