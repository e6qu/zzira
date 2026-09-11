package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

var ErrWorklogValidation = errors.New("invalid worklog")

// UpdateWorklog changes a worklog's comment and time, stamping it so the
// updated feed can report it.
func (s *Store) UpdateWorklog(ctx context.Context, actorID, workspaceID, worklogID string, comment json.RawMessage, seconds *int) (*models.Worklog, *models.Action, error) {
	if seconds != nil && *seconds <= 0 {
		return nil, nil, fmt.Errorf("%w: a positive timeSpentSeconds is required", ErrWorklogValidation)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	current, err := scanWorklog(tx.QueryRow(ctx, worklogJoin+`WHERE w.id=$1 AND w.workspace_id=$2`, worklogID, workspaceID))
	if err != nil {
		return nil, nil, err
	}
	spent := current.TimeSpentSeconds
	if seconds != nil {
		spent = *seconds
	}
	var commentArg any
	if len(comment) > 0 {
		commentArg = comment
	} else {
		commentArg = current.Comment
		if len(current.Comment) == 0 {
			commentArg = nil
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE worklogs SET comment=$3,time_spent_seconds=$4,updated_at=now()
		WHERE id=$1 AND workspace_id=$2`, worklogID, workspaceID, commentArg, spent); err != nil {
		return nil, nil, err
	}
	updated, err := scanWorklog(tx.QueryRow(ctx, worklogJoin+`WHERE w.id=$1`, worklogID))
	if err != nil {
		return nil, nil, err
	}
	seq, err := nextSeq(ctx, tx, workspaceID)
	if err != nil {
		return nil, nil, err
	}
	payload, err := json.Marshal(models.WorklogUpsertPayload{Worklog: *updated})
	if err != nil {
		return nil, nil, err
	}
	action := &models.Action{
		WorkspaceID: workspaceID, Seq: seq, EntityType: models.EntityWorklog, EntityID: worklogID,
		Op: models.OpUpsert, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actorID,
	}
	if err = appendAction(ctx, tx, action); err != nil {
		return nil, nil, err
	}
	return updated, action, tx.Commit(ctx)
}

// WorklogsByIDs fetches worklogs by id for Jira's bulk list endpoint, which
// returns only the ones on work items the caller may read.
func (s *Store) WorklogsByIDs(ctx context.Context, workspaceID, userID string, ids []string) ([]*models.Worklog, error) {
	query := `SELECT w.id, w.issue_id, w.author_id, COALESCE(u.display_name,''),
		COALESCE(w.comment::text,''), w.time_spent_seconds,
		to_char(w.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
		FROM worklogs w
		JOIN issues i ON i.id = w.issue_id
		LEFT JOIN users u ON u.id = w.author_id
		WHERE w.workspace_id=$1 AND w.id = ANY($3) AND ` + VisibleIssuePredicate("i", "$2") + `
		ORDER BY w.created_at, w.id`
	rows, err := s.Pool.Query(ctx, query, workspaceID, userID, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*models.Worklog{}
	for rows.Next() {
		worklog, scanErr := scanWorklog(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, worklog)
	}
	return out, rows.Err()
}

// WorklogChange is one entry in Jira's updated or deleted worklog feed.
type WorklogChange struct {
	ID          string
	IssueID     string
	AuthorID    string
	UpdatedTime time.Time
}

// WorklogsChangedSince reports worklogs updated after the given time, limited
// to work items the caller may read. Deleted reads the tombstones instead,
// which is why a delete records one; those are not filtered, because a client
// invalidating its cache needs every id it may be holding.
func (s *Store) WorklogsChangedSince(ctx context.Context, workspaceID, userID string, since time.Time, deleted bool, limit int) ([]WorklogChange, error) {
	// The feed reports times in milliseconds, so it compares in milliseconds
	// too: a client that passes the last `until` back must not be handed the
	// same entries again on the sub-millisecond remainder.
	query := `SELECT w.id,w.issue_id,w.author_id,date_trunc('milliseconds',w.updated_at) FROM worklogs w
		JOIN issues i ON i.id = w.issue_id
		WHERE w.workspace_id=$1 AND date_trunc('milliseconds',w.updated_at) > $3 AND ` + VisibleIssuePredicate("i", "$2") + `
		ORDER BY w.updated_at, w.id LIMIT $4`
	if deleted {
		query = `SELECT id,issue_id,author_id,date_trunc('milliseconds',deleted_at) FROM deleted_worklogs
			WHERE workspace_id=$1 AND $2 <> '' AND date_trunc('milliseconds',deleted_at) > $3
			ORDER BY deleted_at, id LIMIT $4`
	}
	rows, err := s.Pool.Query(ctx, query, workspaceID, userID, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	changes := []WorklogChange{}
	for rows.Next() {
		var change WorklogChange
		if err = rows.Scan(&change.ID, &change.IssueID, &change.AuthorID, &change.UpdatedTime); err != nil {
			return nil, err
		}
		changes = append(changes, change)
	}
	return changes, rows.Err()
}

// MoveWorklogs moves worklogs to another work item in the same workspace.
func (s *Store) MoveWorklogs(ctx context.Context, actorID, workspaceID, sourceIssueID, targetIssueID string, ids []string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var exists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM issues WHERE id=$1 AND workspace_id=$2)`,
		targetIssueID, workspaceID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("%w: the destination work item does not exist", ErrWorklogValidation)
	}
	if sourceIssueID == targetIssueID {
		return fmt.Errorf("%w: the destination must be a different work item", ErrWorklogValidation)
	}
	for _, id := range ids {
		command, execErr := tx.Exec(ctx, `UPDATE worklogs SET issue_id=$3,updated_at=now()
			WHERE id=$1 AND workspace_id=$2 AND issue_id=$4`, id, workspaceID, targetIssueID, sourceIssueID)
		if execErr != nil {
			return execErr
		}
		if command.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		worklog, scanErr := scanWorklog(tx.QueryRow(ctx, worklogJoin+`WHERE w.id=$1`, id))
		if scanErr != nil {
			return scanErr
		}
		seq, seqErr := nextSeq(ctx, tx, workspaceID)
		if seqErr != nil {
			return seqErr
		}
		payload, marshalErr := json.Marshal(models.WorklogUpsertPayload{Worklog: *worklog})
		if marshalErr != nil {
			return marshalErr
		}
		if err = appendAction(ctx, tx, &models.Action{
			WorkspaceID: workspaceID, Seq: seq, EntityType: models.EntityWorklog, EntityID: id,
			Op: models.OpUpsert, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actorID,
		}); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// WorklogPropertyKeys lists a worklog's property keys in a stable order.
func (s *Store) WorklogPropertyKeys(ctx context.Context, worklogID string) ([]string, error) {
	rows, err := s.Pool.Query(ctx, `SELECT key FROM worklog_properties WHERE worklog_id=$1 ORDER BY key`, worklogID)
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

func (s *Store) WorklogProperty(ctx context.Context, worklogID, key string) (json.RawMessage, error) {
	var value []byte
	err := s.Pool.QueryRow(ctx, `SELECT value FROM worklog_properties WHERE worklog_id=$1 AND key=$2`,
		worklogID, key).Scan(&value)
	return json.RawMessage(value), err
}

// SetWorklogProperty reports true when the key was created, which is how Jira
// chooses 201 over 200.
func (s *Store) SetWorklogProperty(ctx context.Context, worklogID, key string, value json.RawMessage) (bool, error) {
	if !validBoardProperty(key, value) {
		return false, ErrBoardPropertyValidation
	}
	tag, err := s.Pool.Exec(ctx, `INSERT INTO worklog_properties(worklog_id,key,value) VALUES($1,$2,$3)
		ON CONFLICT(worklog_id,key) DO NOTHING`, worklogID, key, []byte(value))
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 1 {
		return true, nil
	}
	_, err = s.Pool.Exec(ctx, `UPDATE worklog_properties SET value=$3,updated_at=now()
		WHERE worklog_id=$1 AND key=$2`, worklogID, key, []byte(value))
	return false, err
}

func (s *Store) DeleteWorklogProperty(ctx context.Context, worklogID, key string) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM worklog_properties WHERE worklog_id=$1 AND key=$2`, worklogID, key)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}
