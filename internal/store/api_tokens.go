package store

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/e6qu/zzira/internal/models"
)

// ErrAPITokenValidation is a token a person asked for that cannot be made.
var ErrAPITokenValidation = fmt.Errorf("invalid API token request")

// apiTokenLimit is how many tokens one person may hold at once. Atlassian
// caps them too; the number matters less than having one, so a lost script
// looping on creation cannot fill the table.
const apiTokenLimit = 20

// APITokensForUser lists a person's tokens, newest first. The secret is not
// among them: only its hash is kept, and the plaintext is shown once when it
// is created.
func (s *Store) APITokensForUser(ctx context.Context, userID string) ([]models.APIToken, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id,user_id,label,created_at,COALESCE(to_char(expires_at,'YYYY-MM-DD"T"HH24:MI:SSOF'),'')
		FROM api_tokens WHERE user_id=$1 ORDER BY created_at DESC,id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tokens := []models.APIToken{}
	for rows.Next() {
		var token models.APIToken
		var created time.Time
		if err := rows.Scan(&token.ID, &token.UserID, &token.Label, &created, &token.ExpiresAt); err != nil {
			return nil, err
		}
		token.CreatedAt = created.UTC().Format(time.RFC3339)
		tokens = append(tokens, token)
	}
	return tokens, rows.Err()
}

// CreateUserAPIToken mints a token for the person themselves and returns the
// secret, which is the only time it exists outside the caller's hands.
func (s *Store) CreateUserAPIToken(ctx context.Context, userID, label string, expiresAt *time.Time, mint func() (string, string, error)) (string, models.APIToken, error) {
	label = strings.TrimSpace(label)
	if label == "" || utf8.RuneCountInString(label) > 100 {
		return "", models.APIToken{}, fmt.Errorf("%w: a label of 1 to 100 characters says what the token is for", ErrAPITokenValidation)
	}
	if expiresAt != nil {
		if !expiresAt.After(time.Now()) {
			return "", models.APIToken{}, fmt.Errorf("%w: an expiry has to be in the future", ErrAPITokenValidation)
		}
		// A year is Atlassian's ceiling, and a token that outlives the reason
		// it was made is the one nobody remembers to revoke.
		if expiresAt.After(time.Now().AddDate(1, 0, 0)) {
			return "", models.APIToken{}, fmt.Errorf("%w: a token expires within a year", ErrAPITokenValidation)
		}
	}
	var held int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM api_tokens WHERE user_id=$1`, userID).Scan(&held); err != nil {
		return "", models.APIToken{}, err
	}
	if held >= apiTokenLimit {
		return "", models.APIToken{}, fmt.Errorf("%w: revoke one of your %d tokens before creating another", ErrAPITokenValidation, apiTokenLimit)
	}
	plain, hash, err := mint()
	if err != nil {
		return "", models.APIToken{}, err
	}
	token := models.APIToken{ID: NewID("tok"), UserID: userID, Label: label}
	var created time.Time
	if err := s.Pool.QueryRow(ctx, `INSERT INTO api_tokens (id,user_id,token_hash,label,expires_at)
		VALUES ($1,$2,$3,$4,$5) RETURNING created_at`,
		token.ID, userID, hash, label, expiresAt).Scan(&created); err != nil {
		return "", models.APIToken{}, err
	}
	token.CreatedAt = created.UTC().Format(time.RFC3339)
	if expiresAt != nil {
		token.ExpiresAt = expiresAt.UTC().Format(time.RFC3339)
	}
	return plain, token, nil
}

// DeleteUserAPIToken revokes one of the caller's own tokens. It is scoped to
// the owner, so a token id learned elsewhere revokes nothing.
func (s *Store) DeleteUserAPIToken(ctx context.Context, userID, tokenID string) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM api_tokens WHERE id=$1 AND user_id=$2`, tokenID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: that token is not yours, or is already revoked", ErrAPITokenValidation)
	}
	return nil
}
