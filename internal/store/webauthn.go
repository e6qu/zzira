package store

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// A security key or passkey answers a sign-in beside the authenticator app.
// The site keeps what the key registered -- its id, its public key and its
// counter -- and the challenge each ceremony must answer, which is issued
// here and read once.

// WebAuthnCredential is a key an account has registered.
type WebAuthnCredential struct {
	ID           string
	CredentialID []byte
	PublicKey    []byte
	Label        string
	SignCount    uint32
	UserVerified bool
	CreatedAt    time.Time
	LastUsedAt   *time.Time
}

// WebAuthnChallengeTTL is how long a ceremony has to answer.
const WebAuthnChallengeTTL = 5 * time.Minute

// ErrWebAuthnCredential is a key this site does not know.
var ErrWebAuthnCredential = errors.New("webauthn credential not found")

// NewWebAuthnChallenge issues the challenge one ceremony must answer.
func (s *Store) NewWebAuthnChallenge(ctx context.Context, userID, purpose string) ([]byte, error) {
	if purpose != "register" && purpose != "sign-in" {
		return nil, fmt.Errorf("a challenge is for registering a key or for signing in, not %q", purpose)
	}
	challenge := make([]byte, 32)
	if _, err := rand.Read(challenge); err != nil {
		return nil, err
	}
	if _, err := s.Pool.Exec(ctx, `DELETE FROM webauthn_challenges WHERE expires_at < now()`); err != nil {
		return nil, err
	}
	if _, err := s.Pool.Exec(ctx, `INSERT INTO webauthn_challenges(challenge,user_id,purpose,expires_at)
		VALUES($1,$2,$3,now()+$4::interval)`, challenge, userID, purpose, WebAuthnChallengeTTL.String()); err != nil {
		return nil, err
	}
	return challenge, nil
}

// ConsumeWebAuthnChallenge reads and forgets a challenge, so one ceremony
// answers it once.
func (s *Store) ConsumeWebAuthnChallenge(ctx context.Context, challenge []byte, userID, purpose string) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM webauthn_challenges
		WHERE challenge=$1 AND user_id=$2 AND purpose=$3 AND expires_at > now()`, challenge, userID, purpose)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errors.New("that is not a challenge this site issued, or it has expired")
	}
	return nil
}

// SaveWebAuthnCredential keeps a key an account has just registered.
func (s *Store) SaveWebAuthnCredential(ctx context.Context, userID string, credential WebAuthnCredential) error {
	label := strings.TrimSpace(credential.Label)
	if label == "" {
		label = "Security key"
	}
	if len([]rune(label)) > 80 {
		label = string([]rune(label)[:80])
	}
	_, err := s.Pool.Exec(ctx, `INSERT INTO webauthn_credentials(user_id,credential_id,public_key,label,sign_count,user_verified)
		VALUES($1,$2,$3,$4,$5,$6)`, userID, credential.CredentialID, credential.PublicKey, label, int64(credential.SignCount), credential.UserVerified)
	if isUniqueViolation(err) {
		return errors.New("that key is already registered")
	}
	return err
}

// WebAuthnCredentials are the keys an account has registered.
func (s *Store) WebAuthnCredentials(ctx context.Context, userID string) ([]WebAuthnCredential, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id::text,credential_id,public_key,label,sign_count,user_verified,created_at,last_used_at
		FROM webauthn_credentials WHERE user_id=$1 ORDER BY created_at,id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	credentials := []WebAuthnCredential{}
	for rows.Next() {
		var credential WebAuthnCredential
		var signCount int64
		if err := rows.Scan(&credential.ID, &credential.CredentialID, &credential.PublicKey, &credential.Label,
			&signCount, &credential.UserVerified, &credential.CreatedAt, &credential.LastUsedAt); err != nil {
			return nil, err
		}
		credential.SignCount = uint32(signCount) // #nosec G115 -- the column is a counter this site wrote from a uint32.
		credentials = append(credentials, credential)
	}
	return credentials, rows.Err()
}

// WebAuthnCredentialByID is the key a sign-in named, and whose account it is.
func (s *Store) WebAuthnCredentialByID(ctx context.Context, credentialID []byte) (WebAuthnCredential, string, error) {
	var credential WebAuthnCredential
	var userID string
	var signCount int64
	err := s.Pool.QueryRow(ctx, `SELECT id::text,user_id,credential_id,public_key,label,sign_count,user_verified,created_at,last_used_at
		FROM webauthn_credentials WHERE credential_id=$1`, credentialID).Scan(&credential.ID, &userID, &credential.CredentialID,
		&credential.PublicKey, &credential.Label, &signCount, &credential.UserVerified, &credential.CreatedAt, &credential.LastUsedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return WebAuthnCredential{}, "", ErrWebAuthnCredential
	}
	credential.SignCount = uint32(signCount) // #nosec G115 -- as above.
	return credential, userID, err
}

// WebAuthnCredentialUsed records that a key answered a sign-in, with the
// counter it answered at.
func (s *Store) WebAuthnCredentialUsed(ctx context.Context, credentialID []byte, signCount uint32) error {
	_, err := s.Pool.Exec(ctx, `UPDATE webauthn_credentials SET sign_count=$2,last_used_at=now() WHERE credential_id=$1`,
		credentialID, int64(signCount))
	return err
}

// DeleteWebAuthnCredential takes a key away from an account.
func (s *Store) DeleteWebAuthnCredential(ctx context.Context, userID, id string) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM webauthn_credentials WHERE user_id=$1 AND id::text=$2`, userID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrWebAuthnCredential
	}
	return nil
}
