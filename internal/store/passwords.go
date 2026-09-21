package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// A password belongs to the person who signs in with it: they are the only
// one who replaces it, and replacing it ends every other session they left
// open.

// ErrPasswordInactive is an account that cannot hold a password any more.
var ErrPasswordInactive = errors.New("that account is not active")

// UserPasswordHash is what one person's password is checked against.
func (s *Store) UserPasswordHash(ctx context.Context, userID string) (string, error) {
	var hash string
	if err := s.Pool.QueryRow(ctx, `SELECT password_hash FROM users WHERE id=$1 AND active`, userID).Scan(&hash); err != nil {
		return "", err
	}
	return hash, nil
}

// SetUserPassword replaces one person's password and ends every session but
// the one they changed it from, so a session opened with the old password on
// another device does not outlive it. An empty keepTokenHash ends them all,
// which is what a password set from outside a session does.
func (s *Store) SetUserPassword(ctx context.Context, userID, hash, keepTokenHash string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	command, err := tx.Exec(ctx, `UPDATE users SET password_hash=$2 WHERE id=$1 AND active`, userID, hash)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return fmt.Errorf("%w: %s", ErrPasswordInactive, userID)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1 AND token_hash<>$2`, userID, keepTokenHash); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ErrPasswordLinkInvalid is a sign-in link that has been used, has expired, or
// was never issued.
var ErrPasswordLinkInvalid = errors.New("that sign-in link is no longer valid")

// PasswordLinkTTL is how long a sign-in link lasts. It is long enough to reach
// someone who reads their email the next morning and short enough that a link
// left in a mailbox is not a standing key.
const PasswordLinkTTL = 24 * time.Hour

// CreatePasswordLink issues one person's sign-in link, replacing the live one
// they already have so only the newest works.
func (s *Store) CreatePasswordLink(ctx context.Context, userID, tokenHash string) (time.Time, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return time.Time{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var active bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1 AND active)`, userID).Scan(&active); err != nil {
		return time.Time{}, err
	}
	if !active {
		return time.Time{}, fmt.Errorf("%w: %s", ErrPasswordInactive, userID)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM password_setup_links WHERE user_id=$1 AND used_at IS NULL`, userID); err != nil {
		return time.Time{}, err
	}
	var expires time.Time
	if err := tx.QueryRow(ctx, `
		INSERT INTO password_setup_links(token_hash,user_id,expires_at) VALUES($1,$2,now()+$3::interval)
		RETURNING expires_at`, tokenHash, userID, PasswordLinkTTL.String()).Scan(&expires); err != nil {
		return time.Time{}, err
	}
	return expires, tx.Commit(ctx)
}

// PasswordLinkUser is whom a sign-in link belongs to, while it is still good
// for the one thing it does.
func (s *Store) PasswordLinkUser(ctx context.Context, tokenHash string) (string, error) {
	var userID string
	err := s.Pool.QueryRow(ctx, `
		SELECT l.user_id FROM password_setup_links l JOIN users u ON u.id=l.user_id AND u.active
		WHERE l.token_hash=$1 AND l.used_at IS NULL AND l.expires_at>now()`, tokenHash).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrPasswordLinkInvalid
	}
	return userID, err
}

// UsePasswordLink spends a sign-in link on the password it was issued for and
// ends every session that account had, because whoever set the password is the
// only one who should now be signed in.
func (s *Store) UsePasswordLink(ctx context.Context, tokenHash, passwordHash string) (string, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var userID string
	err = tx.QueryRow(ctx, `
		UPDATE password_setup_links SET used_at=now()
		WHERE token_hash=$1 AND used_at IS NULL AND expires_at>now()
		RETURNING user_id`, tokenHash).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrPasswordLinkInvalid
	}
	if err != nil {
		return "", err
	}
	command, err := tx.Exec(ctx, `UPDATE users SET password_hash=$2 WHERE id=$1 AND active`, userID, passwordHash)
	if err != nil {
		return "", err
	}
	if command.RowsAffected() == 0 {
		return "", fmt.Errorf("%w: %s", ErrPasswordInactive, userID)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1`, userID); err != nil {
		return "", err
	}
	return userID, tx.Commit(ctx)
}
