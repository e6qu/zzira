package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
)

// A person holds a bounded number of tokens, so a script looping on creation
// cannot fill the table.
func TestUserAPITokensAreCapped(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	st, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if err := Migrate(ctx, st.Pool); err != nil {
		t.Fatal(err)
	}
	userID := NewID("usr")
	if _, err := st.Pool.Exec(ctx, `INSERT INTO users(id,email,password_hash,display_name)
		VALUES($1,$2,'test','Token holder')`, userID, userID+"@example.invalid"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM api_tokens WHERE user_id=$1`, userID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, userID)
	})
	minted := 0
	mint := func() (string, string, error) {
		minted++
		plain := fmt.Sprintf("zzira_capped_%d", minted)
		return plain, HashToken(plain), nil
	}
	for i := 0; i < apiTokenLimit; i++ {
		if _, _, err := st.CreateUserAPIToken(ctx, userID, fmt.Sprintf("Token %d", i), "", nil, mint); err != nil {
			t.Fatalf("token %d: %v", i, err)
		}
	}
	_, _, err = st.CreateUserAPIToken(ctx, userID, "One too many", "", nil, mint)
	if !errors.Is(err, ErrAPITokenValidation) {
		t.Fatalf("the %dth token = %v, want a validation refusal", apiTokenLimit+1, err)
	}
	if minted != apiTokenLimit {
		t.Fatalf("minted %d secrets for %d tokens: a refused request must not mint one", minted, apiTokenLimit)
	}
	tokens, err := st.APITokensForUser(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tokens) != apiTokenLimit {
		t.Fatalf("held %d tokens, want %d", len(tokens), apiTokenLimit)
	}
	// Revoking one makes room for another, so the cap is a ceiling and not a
	// lifetime allowance.
	if err := st.DeleteUserAPIToken(ctx, userID, tokens[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.CreateUserAPIToken(ctx, userID, "Room again", "", nil, mint); err != nil {
		t.Fatalf("after revoking one: %v", err)
	}
}

// The page that creates a token renders the secret in its own response, so a
// reload replays the request that made it. The request id makes that second
// arrival create nothing.
func TestUserAPITokenCreationIsIdempotentPerRequest(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	st, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if err := Migrate(ctx, st.Pool); err != nil {
		t.Fatal(err)
	}
	userID := NewID("usr")
	if _, err := st.Pool.Exec(ctx, `INSERT INTO users(id,email,password_hash,display_name)
		VALUES($1,$2,'test','Reloading person')`, userID, userID+"@example.invalid"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM api_tokens WHERE user_id=$1`, userID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, userID)
	})
	minted := 0
	mint := func() (string, string, error) {
		minted++
		plain := fmt.Sprintf("zzira_replay_%d", minted)
		return plain, HashToken(plain), nil
	}
	requestID := NewID("tkreq")
	if _, _, err := st.CreateUserAPIToken(ctx, userID, "Release script", requestID, nil, mint); err != nil {
		t.Fatal(err)
	}
	_, _, err = st.CreateUserAPIToken(ctx, userID, "Release script", requestID, nil, mint)
	if !errors.Is(err, ErrAPITokenAlreadyCreated) {
		t.Fatalf("the replayed creation = %v, want ErrAPITokenAlreadyCreated", err)
	}
	if minted != 1 {
		t.Fatalf("minted %d secrets for one creation", minted)
	}
	tokens, err := st.APITokensForUser(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 1 {
		t.Fatalf("held %d tokens after a replayed creation, want 1", len(tokens))
	}
	// A different request from the same person is a different token.
	if _, _, err := st.CreateUserAPIToken(ctx, userID, "Release script", NewID("tkreq"), nil, mint); err != nil {
		t.Fatalf("a fresh request: %v", err)
	}
}
