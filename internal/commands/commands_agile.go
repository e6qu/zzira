package commands

import (
	"context"
	"fmt"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

// UpdateBoardConfiguration is the shared administrative command for board
// layout, quick filters, card fields, and column constraints.
func (s *Service) UpdateBoardConfiguration(ctx context.Context, actorID, workspaceID, boardID string, input store.BoardConfigurationUpdate) (*models.Board, error) {
	if _, err := s.administrableBoard(ctx, actorID, workspaceID, boardID); err != nil {
		return nil, err
	}
	board, _, err := s.Store.UpdateBoardConfiguration(ctx, actorID, workspaceID, boardID, input)
	return board, err
}

// administrableBoard resolves a board the actor may configure. Jira Software
// lets a board's own administrators configure it, alongside the administrators
// of the project the board is located in.
func (s *Service) administrableBoard(ctx context.Context, actorID, workspaceID, boardID string) (*models.Board, error) {
	board, err := s.Store.BoardByIDInWorkspace(ctx, workspaceID, boardID)
	if err != nil {
		return nil, err
	}
	allowed, err := s.Store.CanAdministerBoard(ctx, workspaceID, actorID, board)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, fmt.Errorf("board administrator permission is required")
	}
	return board, nil
}

// AddBoardAdmin gives a person or a group administration rights over a board.
func (s *Service) AddBoardAdmin(ctx context.Context, actorID, workspaceID, boardID string, input store.BoardAdminInput) error {
	if _, err := s.administrableBoard(ctx, actorID, workspaceID, boardID); err != nil {
		return err
	}
	_, err := s.Store.AddBoardAdmin(ctx, actorID, workspaceID, boardID, input)
	return err
}

// DeleteBoardAdmin takes those rights away again.
func (s *Service) DeleteBoardAdmin(ctx context.Context, actorID, workspaceID, boardID string, adminID int64) error {
	if _, err := s.administrableBoard(ctx, actorID, workspaceID, boardID); err != nil {
		return err
	}
	_, err := s.Store.DeleteBoardAdmin(ctx, actorID, workspaceID, boardID, adminID)
	return err
}

// CreateSprint adds a sprint to a board. Jira requires Manage sprints in the
// board's project.
func (s *Service) CreateSprint(ctx context.Context, actorID, workspaceID, boardID, name, goal string) (*models.Sprint, error) {
	board, err := s.Store.BoardByIDInWorkspace(ctx, workspaceID, boardID)
	if err != nil {
		return nil, fmt.Errorf("board does not exist")
	}
	if err := s.requirePermission(ctx, workspaceID, actorID, board.ProjectID, "", "MANAGE_SPRINTS_PERMISSION"); err != nil {
		return nil, err
	}
	sprint, _, err := s.Store.CreateSprint(ctx, actorID, workspaceID, boardID, name, goal)
	return sprint, err
}

// manageableSprint checks the actor holds Manage sprints in the project of the
// sprint's board.
func (s *Service) manageableSprint(ctx context.Context, actorID, workspaceID, sprintID string) error {
	sprint, err := s.Store.SprintByIDInWorkspace(ctx, workspaceID, sprintID)
	if err != nil {
		return err
	}
	board, err := s.Store.BoardByIDInWorkspace(ctx, workspaceID, sprint.BoardID)
	if err != nil {
		return err
	}
	return s.requirePermission(ctx, workspaceID, actorID, board.ProjectID, "", "MANAGE_SPRINTS_PERMISSION")
}

// UpdateSprint edits, starts or completes a sprint, which needs Manage sprints
// in its board's project.
func (s *Service) UpdateSprint(ctx context.Context, actorID, workspaceID, sprintID string, input store.SprintUpdate) (*models.Sprint, error) {
	if err := s.manageableSprint(ctx, actorID, workspaceID, sprintID); err != nil {
		return nil, err
	}
	sprint, _, err := s.Store.UpdateSprint(ctx, actorID, workspaceID, sprintID, input)
	return sprint, err
}

// DeleteSprint removes a future sprint.
func (s *Service) DeleteSprint(ctx context.Context, actorID, workspaceID, sprintID string) error {
	if err := s.manageableSprint(ctx, actorID, workspaceID, sprintID); err != nil {
		return err
	}
	return s.Store.DeleteSprint(ctx, actorID, workspaceID, sprintID)
}

// SwapSprints exchanges two sprints' positions; both must be manageable.
func (s *Service) SwapSprints(ctx context.Context, actorID, workspaceID, sprintID, otherID string) error {
	for _, id := range []string{sprintID, otherID} {
		if err := s.manageableSprint(ctx, actorID, workspaceID, id); err != nil {
			return err
		}
	}
	return s.Store.SwapSprints(ctx, actorID, workspaceID, sprintID, otherID)
}

// SetSprintProperty stores a sprint's entity property, reporting whether it
// was new.
func (s *Service) SetSprintProperty(ctx context.Context, actorID, workspaceID, sprintID, key string, value []byte) (bool, error) {
	if err := s.manageableSprint(ctx, actorID, workspaceID, sprintID); err != nil {
		return false, err
	}
	return s.Store.SetSprintProperty(ctx, sprintID, key, value)
}

// DeleteSprintProperty removes a sprint's entity property.
func (s *Service) DeleteSprintProperty(ctx context.Context, actorID, workspaceID, sprintID, key string) error {
	if err := s.manageableSprint(ctx, actorID, workspaceID, sprintID); err != nil {
		return err
	}
	return s.Store.DeleteSprintProperty(ctx, sprintID, key)
}

// MoveIssueToSprint puts a work item at the end of an open sprint, or in the
// backlog when sprintID is empty, from any board.
func (s *Service) MoveIssueToSprint(ctx context.Context, actorID, workspaceID, issueIDOrKey, sprintID string) error {
	issue, err := s.planningIssue(ctx, actorID, workspaceID, issueIDOrKey)
	if err != nil {
		return err
	}
	if sprintID == "" {
		return s.Store.RemoveIssueFromPlanning(ctx, actorID, workspaceID, issue.ID)
	}
	rank, err := s.Store.NextSprintRank(ctx, sprintID)
	if err != nil {
		return err
	}
	_, err = s.Store.AddIssueToSprint(ctx, actorID, workspaceID, sprintID, issue.ID, rank)
	return err
}

// planningIssue resolves a work item the actor may move between sprints and
// the backlog: Jira asks for Edit issues and Schedule issues.
func (s *Service) planningIssue(ctx context.Context, actorID, workspaceID, issueIDOrKey string) (*models.Issue, error) {
	issue, err := s.visibleIssue(ctx, actorID, workspaceID, issueIDOrKey)
	if err != nil {
		return nil, err
	}
	if err := s.requirePermissions(ctx, workspaceID, actorID, issue.ProjectID, issue.ID, "EDIT_ISSUES", "SCHEDULE_ISSUES"); err != nil {
		return nil, err
	}
	return issue, nil
}

// MoveIssueToBacklog removes open-sprint membership without changing the
// issue's existing global rank. Closed sprint membership remains historical.
func (s *Service) MoveIssueToBacklog(ctx context.Context, actorID, workspaceID, issueIDOrKey string) error {
	issue, err := s.planningIssue(ctx, actorID, workspaceID, issueIDOrKey)
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
	issue, err := s.planningIssue(ctx, actorID, workspaceID, issueIDOrKey)
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
	_, err = s.Store.SetIssueRank(ctx, actorID, workspaceID, issue.ID, rank)
	return err
}

// UpdateEpicDetails changes an epic's name, color or done flag, which are
// edits of the epic: Jira asks for Edit issues.
func (s *Service) UpdateEpicDetails(ctx context.Context, actorID, workspaceID, issueIDOrKey string, update store.EpicUpdate) error {
	issue, err := s.visibleIssue(ctx, actorID, workspaceID, issueIDOrKey)
	if err != nil {
		return err
	}
	if err := s.requirePermission(ctx, workspaceID, actorID, issue.ProjectID, issue.ID, "EDIT_ISSUES"); err != nil {
		return err
	}
	return s.Store.UpdateEpicDetails(ctx, workspaceID, issue.ID, update)
}
