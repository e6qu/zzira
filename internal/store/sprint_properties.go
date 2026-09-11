package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

func (s *Store) SprintPropertyKeys(ctx context.Context, sprintID string) ([]string, error) {
	rows, err := s.Pool.Query(ctx, `SELECT key FROM sprint_properties WHERE sprint_id=$1 ORDER BY key`, sprintID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keys := []string{}
	for rows.Next() {
		var key string
		if err = rows.Scan(&key); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

func (s *Store) SprintProperty(ctx context.Context, sprintID, key string) (json.RawMessage, error) {
	var value []byte
	err := s.Pool.QueryRow(ctx, `SELECT value FROM sprint_properties WHERE sprint_id=$1 AND key=$2`, sprintID, key).Scan(&value)
	return json.RawMessage(value), err
}

// SetSprintProperty reports true when the key was created, which is how Jira
// chooses 201 over 200.
func (s *Store) SetSprintProperty(ctx context.Context, sprintID, key string, value json.RawMessage) (bool, error) {
	if !validBoardProperty(key, value) {
		return false, ErrBoardPropertyValidation
	}
	tag, err := s.Pool.Exec(ctx, `INSERT INTO sprint_properties(sprint_id,key,value) VALUES($1,$2,$3)
		ON CONFLICT(sprint_id,key) DO NOTHING`, sprintID, key, []byte(value))
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 1 {
		return true, nil
	}
	_, err = s.Pool.Exec(ctx, `UPDATE sprint_properties SET value=$3,updated_at=now() WHERE sprint_id=$1 AND key=$2`,
		sprintID, key, []byte(value))
	return false, err
}

func (s *Store) DeleteSprintProperty(ctx context.Context, sprintID, key string) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM sprint_properties WHERE sprint_id=$1 AND key=$2`, sprintID, key)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// DeleteSprint removes a sprint. Its work returns to the backlog rather than
// disappearing with it, so no work item is lost with the sprint.
func (s *Store) DeleteSprint(ctx context.Context, actorID, workspaceID, sprintID string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// A board belongs to a workspace through its project, not directly.
	var state string
	if err = tx.QueryRow(ctx, `SELECT s.state FROM sprints s
		JOIN boards b ON b.id=s.board_id
		JOIN projects p ON p.id=b.project_id
		WHERE s.id=$1 AND p.workspace_id=$2 FOR UPDATE OF s`, sprintID, workspaceID).Scan(&state); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return pgx.ErrNoRows
		}
		return err
	}
	if state == "active" {
		return fmt.Errorf("%w: complete the sprint before deleting it", ErrSprintConflict)
	}
	// Sprint membership is a join table, so dropping the rows returns the work
	// to the backlog rather than deleting it with the sprint.
	if _, err = tx.Exec(ctx, `DELETE FROM sprint_issues WHERE sprint_id=$1`, sprintID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM sprints WHERE id=$1`, sprintID); err != nil {
		return err
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "sprint", sprintID, "delete",
		map[string]any{"sprintId": sprintID}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// SwapSprints exchanges the order of two sprints on the same board.
func (s *Store) SwapSprints(ctx context.Context, actorID, workspaceID, sprintID, otherID string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	type placed struct {
		board    string
		position int
	}
	read := func(id string) (placed, error) {
		var found placed
		err := tx.QueryRow(ctx, `SELECT s.board_id,s.position FROM sprints s
			JOIN boards b ON b.id=s.board_id
			JOIN projects p ON p.id=b.project_id
			WHERE s.id=$1 AND p.workspace_id=$2`, id, workspaceID).Scan(&found.board, &found.position)
		return found, err
	}
	// Report a genuine failure as one; only a missing row is a 404.
	first, err := read(sprintID)
	if err != nil {
		return err
	}
	second, err := read(otherID)
	if err != nil {
		return err
	}
	if sprintID == otherID {
		return fmt.Errorf("%w: a sprint cannot swap with itself", ErrSprintConflict)
	}
	if first.board != second.board {
		return fmt.Errorf("%w: both sprints must belong to the same board", ErrSprintConflict)
	}
	if _, err = tx.Exec(ctx, `UPDATE sprints SET position=$2 WHERE id=$1`, sprintID, second.position); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE sprints SET position=$2 WHERE id=$1`, otherID, first.position); err != nil {
		return err
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "sprint", sprintID, "upsert",
		map[string]any{"sprintId": sprintID, "swappedWith": otherID}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
