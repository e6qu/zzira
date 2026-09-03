package commands

import (
	"context"
	"fmt"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

func (s *Service) CreateSprint(ctx context.Context, actorID, workspaceID, boardID, name, goal string) (*models.Sprint, error) {
	sprint, _, err := s.Store.CreateSprint(ctx, actorID, workspaceID, boardID, name, goal)
	return sprint, err
}

func (s *Service) UpdateSprint(ctx context.Context, actorID, workspaceID, sprintID string, input store.SprintUpdate) (*models.Sprint, error) {
	sprint, _, err := s.Store.UpdateSprint(ctx, actorID, workspaceID, sprintID, input)
	return sprint, err
}

// MoveIssueToBacklog removes open-sprint membership without changing the
// issue's existing global rank. Closed sprint membership remains historical.
func (s *Service) MoveIssueToBacklog(ctx context.Context, actorID, workspaceID, issueIDOrKey string) error {
	issue, err := s.visibleIssue(ctx, actorID, workspaceID, issueIDOrKey)
	if err != nil {
		return err
	}
	return s.Store.RemoveIssueFromPlanning(ctx, actorID, workspaceID, issue.ID)
}

// PlanIssue is the single command path for moving or ranking work in an open
// sprint or the backlog. An empty sprintID means the project backlog.
func (s *Service) PlanIssue(ctx context.Context, actorID, workspaceID, boardID, issueIDOrKey, sprintID, beforeID, afterID string) error {
	board, err := s.Store.BoardByIDInWorkspace(ctx, workspaceID, boardID)
	if err != nil {
		return fmt.Errorf("board does not exist")
	}
	issue, err := s.visibleIssue(ctx, actorID, workspaceID, issueIDOrKey)
	if err != nil {
		return err
	}
	if issue.ProjectID != board.ProjectID {
		return fmt.Errorf("issue does not belong to the board project")
	}
	if sprintID != "" {
		sprint, err := s.Store.SprintByIDInWorkspace(ctx, workspaceID, sprintID)
		if err != nil || sprint.BoardID != board.ID || sprint.State == "closed" {
			return fmt.Errorf("choose an active or future sprint")
		}
		var rank string
		if beforeID == "" && afterID == "" {
			rank, err = s.Store.NextSprintRank(ctx, sprintID)
		} else {
			rank, err = s.Store.SprintRankBetween(ctx, sprintID, beforeID, afterID, issue.ID)
		}
		if err != nil {
			return err
		}
		_, err = s.Store.AddIssueToSprint(ctx, actorID, workspaceID, sprintID, issue.ID, rank)
		return err
	}

	if beforeID == "" && afterID == "" {
		backlog, err := s.Store.BacklogIssues(ctx, boardID, actorID)
		if err != nil {
			return err
		}
		if len(backlog) > 0 && backlog[len(backlog)-1].ID != issue.ID {
			afterID = backlog[len(backlog)-1].ID
		}
	}
	rank, err := s.Store.PlanningRankBetween(ctx, workspaceID, board.ProjectID, beforeID, afterID, issue.ID)
	if err != nil {
		return err
	}
	if err := s.Store.RemoveIssueFromPlanning(ctx, actorID, workspaceID, issue.ID); err != nil {
		return err
	}
	_, err = s.Store.SetIssueRank(ctx, actorID, workspaceID, issue.ID, rank, "")
	return err
}
