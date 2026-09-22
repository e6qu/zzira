package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// A provisioning provider holds a key of its own rather than an
// administrator's personal token: it provisions one directory, it is revoked
// on its own, and losing the person who made it does not stop provisioning.

// DirectoryAPIKey is one provisioning key as a page shows it. The key itself
// is never read back -- only its hash is kept.
type DirectoryAPIKey struct {
	ID          string
	DirectoryID string
	Name        string
	CreatedBy   string
	CreatedAt   time.Time
	LastUsedAt  *time.Time
	RevokedAt   *time.Time
}

// ErrDirectoryKey is a provisioning key a site cannot issue or revoke.
var ErrDirectoryKey = errors.New("invalid provisioning key")

// DirectoryAPIKeys lists a directory's provisioning keys, newest first,
// including revoked ones so a reader can see what was withdrawn.
func (s *Store) DirectoryAPIKeys(ctx context.Context, directoryID string) ([]DirectoryAPIKey, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id::text,directory_id::text,name,COALESCE(created_by,''),created_at,last_used_at,revoked_at
		FROM directory_api_keys WHERE directory_id=$1::uuid ORDER BY created_at DESC,id`, directoryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keys := []DirectoryAPIKey{}
	for rows.Next() {
		var key DirectoryAPIKey
		if err := rows.Scan(&key.ID, &key.DirectoryID, &key.Name, &key.CreatedBy, &key.CreatedAt, &key.LastUsedAt, &key.RevokedAt); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

// CreateDirectoryAPIKey issues a provisioning key for one directory. The
// caller is handed the key once; the site keeps its hash.
func (s *Store) CreateDirectoryAPIKey(ctx context.Context, organizationID, actorID, directoryID, name, tokenHash string) (DirectoryAPIKey, error) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 255 {
		return DirectoryAPIKey{}, fmt.Errorf("%w: a key needs a name of 1 to 255 characters", ErrDirectoryKey)
	}
	var belongs bool
	if err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM directories WHERE id=$1::uuid AND organization_id=$2::uuid)`,
		directoryID, organizationID).Scan(&belongs); err != nil {
		return DirectoryAPIKey{}, err
	}
	if !belongs {
		return DirectoryAPIKey{}, fmt.Errorf("%w: that directory is not this organization's", ErrDirectoryKey)
	}
	var key DirectoryAPIKey
	if err := s.Pool.QueryRow(ctx, `INSERT INTO directory_api_keys(directory_id,name,token_hash,created_by)
		VALUES($1::uuid,$2,$3,$4) RETURNING id::text,directory_id::text,name,COALESCE(created_by,''),created_at`,
		directoryID, name, tokenHash, actorID).Scan(&key.ID, &key.DirectoryID, &key.Name, &key.CreatedBy, &key.CreatedAt); err != nil {
		return DirectoryAPIKey{}, err
	}
	return key, nil
}

// RevokeDirectoryAPIKey withdraws a key. A provider holding it is refused from
// the next request on.
func (s *Store) RevokeDirectoryAPIKey(ctx context.Context, organizationID, keyID string) error {
	command, err := s.Pool.Exec(ctx, `UPDATE directory_api_keys k SET revoked_at=now()
		FROM directories d WHERE d.id=k.directory_id AND d.organization_id=$1::uuid AND k.id=$2::uuid AND k.revoked_at IS NULL`,
		organizationID, keyID)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return fmt.Errorf("%w: that key is already gone", ErrDirectoryKey)
	}
	return nil
}

// DirectoryForAPIKey is the directory a provisioning key provisions, and the
// organization it belongs to. A revoked key answers nothing. Using a key
// records that it was used, which is what a page shows to say provisioning is
// alive.
func (s *Store) DirectoryForAPIKey(ctx context.Context, tokenHash string) (directoryID, organizationID string, err error) {
	err = s.Pool.QueryRow(ctx, `UPDATE directory_api_keys k SET last_used_at=now()
		FROM directories d WHERE d.id=k.directory_id AND k.token_hash=$1 AND k.revoked_at IS NULL
		RETURNING k.directory_id::text,d.organization_id::text`, tokenHash).Scan(&directoryID, &organizationID)
	return directoryID, organizationID, err
}

// DirectoryAPIKeyIssuer is the person who issued a key, as long as they are
// still someone this site knows. A key provisions as them, so every change it
// makes is recorded against a person rather than against nobody.
func (s *Store) DirectoryAPIKeyIssuer(ctx context.Context, directoryID, tokenHash string) (string, error) {
	var issuer string
	err := s.Pool.QueryRow(ctx, `SELECT COALESCE(k.created_by,'') FROM directory_api_keys k
		JOIN users u ON u.id=k.created_by
		WHERE k.directory_id=$1::uuid AND k.token_hash=$2 AND k.revoked_at IS NULL`, directoryID, tokenHash).Scan(&issuer)
	return issuer, err
}
