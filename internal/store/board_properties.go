package store

import (
	"context"
	"encoding/json"
	"errors"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

var ErrBoardPropertyValidation = errors.New("invalid board property")

func validBoardProperty(key string, value json.RawMessage) bool {
	return key != "" && utf8.ValidString(key) && utf8.RuneCountInString(key) <= 255 &&
		value != nil && len(value) <= 32768 && json.Valid(value)
}

// BoardPropertyKeys lists a board's property keys in a stable order.
func (s *Store) BoardPropertyKeys(ctx context.Context, boardID string) ([]string, error) {
	rows, err := s.Pool.Query(ctx, `SELECT key FROM board_properties WHERE board_id=$1 ORDER BY key`, boardID)
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

func (s *Store) BoardProperty(ctx context.Context, boardID, key string) (json.RawMessage, error) {
	var value []byte
	err := s.Pool.QueryRow(ctx, `SELECT value FROM board_properties WHERE board_id=$1 AND key=$2`, boardID, key).Scan(&value)
	return json.RawMessage(value), err
}

// SetBoardProperty reports true when the key was created and false when an
// existing value was replaced, which is how Jira chooses 201 over 200.
func (s *Store) SetBoardProperty(ctx context.Context, boardID, key string, value json.RawMessage) (bool, error) {
	if !validBoardProperty(key, value) {
		return false, ErrBoardPropertyValidation
	}
	tag, err := s.Pool.Exec(ctx, `INSERT INTO board_properties(board_id,key,value) VALUES($1,$2,$3)
		ON CONFLICT(board_id,key) DO NOTHING`, boardID, key, []byte(value))
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 1 {
		return true, nil
	}
	_, err = s.Pool.Exec(ctx, `UPDATE board_properties SET value=$3,updated_at=now() WHERE board_id=$1 AND key=$2`,
		boardID, key, []byte(value))
	return false, err
}

func (s *Store) DeleteBoardProperty(ctx context.Context, boardID, key string) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM board_properties WHERE board_id=$1 AND key=$2`, boardID, key)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}
