package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Two-step verification is a secret an authenticator app holds and a set of
// recovery codes that stand in for it. The secret arrives sealed -- this
// package keeps it, and never opens it.

// ErrTwoStep is a two-step enrolment that is not where the caller thinks it
// is: already confirmed, or not started.
var ErrTwoStep = errors.New("two-step verification")

// ErrTwoStepCode is a code that is not one this account accepts.
var ErrTwoStepCode = errors.New("that code is not right")

// SignInChallengeTTL is how long a sign-in waits for its code, and
// SignInChallengeAttempts is how many codes may be typed before it is thrown
// away -- six digits are guessable if the guessing is free.
const (
	SignInChallengeTTL      = 10 * time.Minute
	SignInChallengeAttempts = 5
)

// TwoStepEnrolment is what an account has, or is part way through having.
type TwoStepEnrolment struct {
	// Secret is sealed, as it was given.
	Secret    []byte
	Confirmed bool
	// RecoveryCodesLeft is how many unused recovery codes remain.
	RecoveryCodesLeft int
}

// TwoStep reads one person's enrolment, or reports that they have none.
func (s *Store) TwoStep(ctx context.Context, userID string) (TwoStepEnrolment, error) {
	var enrolment TwoStepEnrolment
	var confirmed *time.Time
	err := s.Pool.QueryRow(ctx, `SELECT secret,confirmed_at FROM user_two_step WHERE user_id=$1`, userID).Scan(&enrolment.Secret, &confirmed)
	if errors.Is(err, pgx.ErrNoRows) {
		return TwoStepEnrolment{}, nil
	}
	if err != nil {
		return TwoStepEnrolment{}, err
	}
	enrolment.Confirmed = confirmed != nil
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM user_recovery_codes WHERE user_id=$1 AND used_at IS NULL`, userID).Scan(&enrolment.RecoveryCodesLeft); err != nil {
		return TwoStepEnrolment{}, err
	}
	return enrolment, nil
}

// StartTwoStep keeps a sealed secret for a person to confirm. Starting again
// replaces an enrolment that was never finished; one already confirmed is
// left alone, because replacing it is how a stolen session would lock its
// owner out.
func (s *Store) StartTwoStep(ctx context.Context, userID string, secret []byte) error {
	command, err := s.Pool.Exec(ctx, `
		INSERT INTO user_two_step(user_id,secret) VALUES($1,$2)
		ON CONFLICT(user_id) DO UPDATE SET secret=EXCLUDED.secret,created_at=now()
		WHERE user_two_step.confirmed_at IS NULL`, userID, secret)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return fmt.Errorf("%w: this account already verifies in two steps", ErrTwoStep)
	}
	return nil
}

// ConfirmTwoStep finishes an enrolment, with the recovery codes that were
// shown to its owner. It is one transaction: the account is not left
// verifying in two steps with no way past a lost phone.
func (s *Store) ConfirmTwoStep(ctx context.Context, userID string, recoveryHashes []string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	command, err := tx.Exec(ctx, `UPDATE user_two_step SET confirmed_at=now() WHERE user_id=$1 AND confirmed_at IS NULL`, userID)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return fmt.Errorf("%w: there is no enrolment to confirm", ErrTwoStep)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM user_recovery_codes WHERE user_id=$1`, userID); err != nil {
		return err
	}
	for _, hash := range recoveryHashes {
		if _, err := tx.Exec(ctx, `INSERT INTO user_recovery_codes(user_id,code_hash) VALUES($1,$2)`, userID, hash); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET mfa_enabled=TRUE WHERE id=$1`, userID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// DisableTwoStep takes an account back to one step, secret and codes
// together.
func (s *Store) DisableTwoStep(ctx context.Context, userID string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM user_two_step WHERE user_id=$1`, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM user_recovery_codes WHERE user_id=$1`, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET mfa_enabled=FALSE WHERE id=$1`, userID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// UseRecoveryCode spends one recovery code, which is what someone whose phone
// is gone signs in with. Each code works once.
func (s *Store) UseRecoveryCode(ctx context.Context, userID, codeHash string) error {
	command, err := s.Pool.Exec(ctx, `
		UPDATE user_recovery_codes SET used_at=now()
		WHERE user_id=$1 AND code_hash=$2 AND used_at IS NULL`, userID, codeHash)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrTwoStepCode
	}
	return nil
}

// CreateSignInChallenge records a sign-in that has passed the password and is
// waiting for its code.
func (s *Store) CreateSignInChallenge(ctx context.Context, tokenHash, userID string) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO sign_in_challenges(token_hash,user_id,expires_at) VALUES($1,$2,now()+$3::interval)`,
		tokenHash, userID, SignInChallengeTTL.String())
	return err
}

// SignInChallengeUser is whose sign-in is waiting, while it still is.
func (s *Store) SignInChallengeUser(ctx context.Context, tokenHash string) (string, error) {
	var userID string
	err := s.Pool.QueryRow(ctx, `
		SELECT c.user_id FROM sign_in_challenges c JOIN users u ON u.id=c.user_id AND u.active
		WHERE c.token_hash=$1 AND c.expires_at>now()`, tokenHash).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrTwoStepCode
	}
	return userID, err
}

// FailSignInChallenge counts a wrong code and throws the challenge away once
// there have been too many, so the six digits cannot be walked through.
func (s *Store) FailSignInChallenge(ctx context.Context, tokenHash string) error {
	var attempts int
	err := s.Pool.QueryRow(ctx, `
		UPDATE sign_in_challenges SET attempts=attempts+1 WHERE token_hash=$1 RETURNING attempts`, tokenHash).Scan(&attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if attempts >= SignInChallengeAttempts {
		return s.DeleteSignInChallenge(ctx, tokenHash)
	}
	return nil
}

// DeleteSignInChallenge ends a challenge: it was answered, or it was given up
// on.
func (s *Store) DeleteSignInChallenge(ctx context.Context, tokenHash string) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM sign_in_challenges WHERE token_hash=$1`, tokenHash)
	return err
}

// ResetDirectoryUserTwoStep is an administrator taking two-step verification
// off an account whose owner has lost both the app and their recovery codes.
// It is the one way past a second step that is not the second step, so it is
// written to the organization's audit log.
func (s *Store) ResetDirectoryUserTwoStep(ctx context.Context, workspaceID, actorID, directoryID, userID string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var organizationID string
	err = tx.QueryRow(ctx, `
		SELECT d.organization_id::text FROM directory_users du
		JOIN directories d ON d.id=du.directory_id
		JOIN sites si ON si.organization_id=d.organization_id
		WHERE du.directory_id::text=$1 AND du.user_id=$2 AND si.workspace_id=$3`, directoryID, userID, workspaceID).Scan(&organizationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: that person is not in this directory", ErrAdminNotFound)
	}
	if err != nil {
		return err
	}
	command, err := tx.Exec(ctx, `DELETE FROM user_two_step WHERE user_id=$1`, userID)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return fmt.Errorf("%w: that account does not verify in two steps", ErrTwoStep)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM user_recovery_codes WHERE user_id=$1`, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET mfa_enabled=FALSE WHERE id=$1`, userID); err != nil {
		return err
	}
	// Whatever was signed in as them stays signed in only if they were: the
	// account is back to one step, and a sign-in half finished is not.
	if _, err := tx.Exec(ctx, `DELETE FROM sign_in_challenges WHERE user_id=$1`, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO organization_audit_events(organization_id,actor_id,action,target_type,target_id)
		VALUES($1::uuid,$2,'user.two-step.reset','user',$3)`, organizationID, actorID, userID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
