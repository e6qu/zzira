package store

import (
	"context"
	"encoding/json"
	"errors"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
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
// existing value was replaced.
func (s *Store) SetIssueProperty(ctx context.Context, issueID, key string, value json.RawMessage) (bool, error) {
	if !validIssueProperty(key, value) {
		return false, ErrIssuePropertyValidation
	}
	tag, err := s.Pool.Exec(ctx, `
		INSERT INTO issue_properties(issue_id,key,value) VALUES($1,$2,$3)
		ON CONFLICT(issue_id,key) DO NOTHING`, issueID, key, []byte(value))
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 1 {
		return true, nil
	}
	_, err = s.Pool.Exec(ctx, `UPDATE issue_properties SET value=$3,updated_at=now() WHERE issue_id=$1 AND key=$2`, issueID, key, []byte(value))
	return false, err
}

func (s *Store) DeleteIssueProperty(ctx context.Context, issueID, key string) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM issue_properties WHERE issue_id=$1 AND key=$2`, issueID, key)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}
