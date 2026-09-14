package store

import (
	"context"
	"encoding/json"
	"errors"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/e6qu/zzira/internal/models"
)

var ErrIssuePropertyValidation = errors.New("invalid issue property")

func validIssueProperty(key string, value json.RawMessage) bool {
	return key != "" && utf8.ValidString(key) && utf8.RuneCountInString(key) <= 255 &&
		value != nil && len(value) <= 32768 && json.Valid(value)
}

func (s *Store) IssueProperties(ctx context.Context, issueID string) (map[string]json.RawMessage, error) {
	rows, err := s.Pool.Query(ctx, `SELECT key,value FROM issue_properties WHERE issue_id=$1 ORDER BY key`, issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	properties := map[string]json.RawMessage{}
	for rows.Next() {
		var key string
		var value []byte
		if err = rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		properties[key] = json.RawMessage(value)
	}
	return properties, rows.Err()
}

func (s *Store) IssueProperty(ctx context.Context, issueID, key string) (json.RawMessage, error) {
	var value []byte
	err := s.Pool.QueryRow(ctx, `SELECT value FROM issue_properties WHERE issue_id=$1 AND key=$2`, issueID, key).Scan(&value)
	return json.RawMessage(value), err
}

// SetIssueProperty returns true when the key was created and false when an
// existing value was replaced. The change is recorded as an issue property
// action so webhooks can report it.
func (s *Store) SetIssueProperty(ctx context.Context, actorID, issueID, key string, value json.RawMessage) (bool, error) {
	if !validIssueProperty(key, value) {
		return false, ErrIssuePropertyValidation
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `
		INSERT INTO issue_properties(issue_id,key,value) VALUES($1,$2,$3)
		ON CONFLICT(issue_id,key) DO NOTHING`, issueID, key, []byte(value))
	if err != nil {
		return false, err
	}
	created := tag.RowsAffected() == 1
	if !created {
		tag, err = tx.Exec(ctx, `UPDATE issue_properties SET value=$3,updated_at=now() WHERE issue_id=$1 AND key=$2 AND value IS DISTINCT FROM $3::jsonb`, issueID, key, []byte(value))
		if err != nil {
			return false, err
		}
	}
	if tag.RowsAffected() == 1 {
		if err = issuePropertyAction(ctx, tx, actorID, issueID, key, value, models.OpUpsert); err != nil {
			return false, err
		}
	}
	return created, tx.Commit(ctx)
}

func (s *Store) DeleteIssueProperty(ctx context.Context, actorID, issueID, key string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `DELETE FROM issue_properties WHERE issue_id=$1 AND key=$2`, issueID, key)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	if err = issuePropertyAction(ctx, tx, actorID, issueID, key, nil, models.OpDelete); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func issuePropertyAction(ctx context.Context, tx pgx.Tx, actorID, issueID, key string, value json.RawMessage, op string) error {
	var workspaceID, issueKey string
	if err := tx.QueryRow(ctx, `SELECT workspace_id,key FROM issues WHERE id=$1`, issueID).Scan(&workspaceID, &issueKey); err != nil {
		return err
	}
	seq, err := nextSeq(ctx, tx, workspaceID)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(models.IssuePropertyPayload{IssueID: issueID, IssueKey: issueKey, Key: key, Value: value})
	if err != nil {
		return err
	}
	return appendAction(ctx, tx, &models.Action{
		WorkspaceID: workspaceID, Seq: seq, EntityType: models.EntityIssueProperty, EntityID: issueID + "/" + key,
		Op: op, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actorID,
	})
}
