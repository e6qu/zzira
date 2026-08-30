package commands

import (
	"context"
	"fmt"
)

// SetIssueRank repositions an issue in a board column, optionally changing status.
func (s *Service) SetIssueRank(ctx context.Context, actorID, workspaceID, issueIDOrKey, beforeID, afterID, newStatusID string) error {
	issue, err := s.Store.IssueByIDOrKey(ctx, workspaceID, issueIDOrKey)
	if err != nil {
		return fmt.Errorf("issue %q not found", issueIDOrKey)
	}
	statusID := newStatusID
	if statusID == "" {
		statusID = issue.Status.ID
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
	_, err = s.Store.SetIssueRank(ctx, actorID, workspaceID, issue.ID, rank, newStatusID)
	return err
}
