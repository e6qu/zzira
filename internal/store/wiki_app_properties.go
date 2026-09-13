package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// Forge app properties are values an app keeps under its own keys. Only the
// app can reach them, so every function takes the installation the request was
// authenticated as rather than a person.

var ErrWikiAppPropertyValidation = errors.New("invalid app property")

// WikiAppProperty is one key and its value.
type WikiAppProperty struct {
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
}

// ValidWikiAppPropertyKey applies Confluence's limit: a key of at most 127
// characters.
func ValidWikiAppPropertyKey(key string) error {
	if key == "" || len([]rune(key)) > 127 {
		return fmt.Errorf("%w: a property key of 1 to 127 characters is required", ErrWikiAppPropertyValidation)
	}
	return nil
}

// WikiAppProperties lists an app's properties in key order, from after the
// given key, up to limit.
func (s *Store) WikiAppProperties(ctx context.Context, installationID, afterKey string, limit int) ([]WikiAppProperty, error) {
	rows, err := s.Pool.Query(ctx, `SELECT key, value FROM wiki_app_properties
		WHERE installation_id=$1 AND key > $2 ORDER BY key LIMIT $3`, installationID, afterKey, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	properties := []WikiAppProperty{}
	for rows.Next() {
		var property WikiAppProperty
		if err = rows.Scan(&property.Key, &property.Value); err != nil {
			return nil, err
		}
		properties = append(properties, property)
	}
	return properties, rows.Err()
}

func (s *Store) WikiAppProperty(ctx context.Context, installationID, key string) (WikiAppProperty, error) {
	if err := ValidWikiAppPropertyKey(key); err != nil {
		return WikiAppProperty{}, err
	}
	property := WikiAppProperty{Key: key}
	err := s.Pool.QueryRow(ctx, `SELECT value FROM wiki_app_properties WHERE installation_id=$1 AND key=$2`, installationID, key).Scan(&property.Value)
	return property, err
}

// PutWikiAppProperty creates or replaces a property and reports whether it was
// created, because Confluence answers 201 for a new property and 200 for a
// replaced one.
func (s *Store) PutWikiAppProperty(ctx context.Context, installationID, key string, value json.RawMessage) (bool, error) {
	if err := ValidWikiAppPropertyKey(key); err != nil {
		return false, err
	}
	if !json.Valid(value) {
		return false, fmt.Errorf("%w: the value must be valid JSON", ErrWikiAppPropertyValidation)
	}
	var created bool
	err := s.Pool.QueryRow(ctx, `INSERT INTO wiki_app_properties(installation_id,key,value) VALUES($1,$2,$3::jsonb)
		ON CONFLICT (installation_id,key) DO UPDATE SET value=EXCLUDED.value, updated_at=now()
		RETURNING (xmax = 0)`, installationID, key, value).Scan(&created)
	return created, err
}

// DeleteWikiAppProperty removes a property. Removing one that is not there
// leaves the app in the state it asked for, so it is not an error.
func (s *Store) DeleteWikiAppProperty(ctx context.Context, installationID, key string) error {
	if err := ValidWikiAppPropertyKey(key); err != nil {
		return err
	}
	_, err := s.Pool.Exec(ctx, `DELETE FROM wiki_app_properties WHERE installation_id=$1 AND key=$2`, installationID, key)
	return err
}
