package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// An admin key gives an administrator temporary access to restricted content.
// Holding the admin role is not enough on its own: the restriction bypass in the
// page visibility rules requires an active key as well, exactly as in
// Confluence, where an administrator without a key sees what anyone else sees.

var ErrWikiAdminKeyValidation = errors.New("invalid admin key")

// WikiAdminKey is the caller's current key.
type WikiAdminKey struct {
	AccountID      string
	ExpirationTime time.Time
}

const (
	adminKeyDefaultMinutes = 10
	adminKeyMaximumMinutes = 60
)

// wikiAdminKeyActive is true when the person in $2 is an administrator with an
// unexpired key in the workspace of the space aliased s.
const wikiAdminKeyActive = `(EXISTS (SELECT 1 FROM memberships am WHERE am.workspace_id=s.workspace_id AND am.user_id=$2 AND am.role='admin')
  AND EXISTS (SELECT 1 FROM wiki_admin_keys ak WHERE ak.workspace_id=s.workspace_id AND ak.user_id=$2 AND ak.expires_at > now()))`

// requireAdminKeyHolder refuses anyone who is not an administrator. Confluence
// answers that with a 404 rather than a 403, so it is reported as not found.
func (s *Store) requireAdminKeyHolder(ctx context.Context, ws, actor string) error {
	admin, err := s.IsAdmin(ctx, ws, actor)
	if err != nil {
		return err
	}
	if !admin {
		return pgx.ErrNoRows
	}
	return nil
}

// EnableWikiAdminKey issues a key, replacing any the caller already holds with
// a fresh expiry. Zero minutes means the ten-minute default.
func (s *Store) EnableWikiAdminKey(ctx context.Context, ws, actor string, minutes int) (WikiAdminKey, error) {
	if err := s.requireAdminKeyHolder(ctx, ws, actor); err != nil {
		return WikiAdminKey{}, err
	}
	if minutes < 0 || minutes > adminKeyMaximumMinutes {
		return WikiAdminKey{}, fmt.Errorf("%w: durationInMinutes must be between 0 and %d", ErrWikiAdminKeyValidation, adminKeyMaximumMinutes)
	}
	if minutes == 0 {
		minutes = adminKeyDefaultMinutes
	}
	key := WikiAdminKey{AccountID: actor}
	err := s.Pool.QueryRow(ctx, `INSERT INTO wiki_admin_keys(workspace_id,user_id,expires_at)
		VALUES($1,$2,now() + make_interval(mins => $3))
		ON CONFLICT (workspace_id,user_id) DO UPDATE SET expires_at=EXCLUDED.expires_at, created_at=now()
		RETURNING expires_at`, ws, actor, minutes).Scan(&key.ExpirationTime)
	return key, err
}

// WikiAdminKeyFor reports the caller's key, and not found when they hold none
// or it has expired.
func (s *Store) WikiAdminKeyFor(ctx context.Context, ws, actor string) (WikiAdminKey, error) {
	if err := s.requireAdminKeyHolder(ctx, ws, actor); err != nil {
		return WikiAdminKey{}, err
	}
	key := WikiAdminKey{AccountID: actor}
	err := s.Pool.QueryRow(ctx, `SELECT expires_at FROM wiki_admin_keys
		WHERE workspace_id=$1 AND user_id=$2 AND expires_at > now()`, ws, actor).Scan(&key.ExpirationTime)
	return key, err
}

// DisableWikiAdminKey ends the caller's key. Ending a key that is not there
// leaves the caller in the state they asked for.
func (s *Store) DisableWikiAdminKey(ctx context.Context, ws, actor string) error {
	if err := s.requireAdminKeyHolder(ctx, ws, actor); err != nil {
		return err
	}
	_, err := s.Pool.Exec(ctx, `DELETE FROM wiki_admin_keys WHERE workspace_id=$1 AND user_id=$2`, ws, actor)
	return err
}
