package store

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestIdentityProviderStateIsBoundToProvider(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	st, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := Migrate(ctx, st.Pool); err != nil {
		t.Fatal(err)
	}
	state := NewID("state")
	if err := st.CreateIdentityProviderLoginState(ctx, state, "google", "nonce", "verifier", time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.ConsumeIdentityProviderLoginState(ctx, state, "microsoft"); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("mismatched provider consumed state: %v", err)
	}
	nonce, verifier, err := st.ConsumeIdentityProviderLoginState(ctx, state, "google")
	if err != nil || nonce != "nonce" || verifier != "verifier" {
		t.Fatalf("correct provider state = %q %q %v", nonce, verifier, err)
	}
}

func TestIdentityProviderSessionWritesOrganizationLoginAudit(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	st, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := Migrate(ctx, st.Pool); err != nil {
		t.Fatal(err)
	}
	email := NewID("provider-audit") + "@example.invalid"
	userID, err := st.ResolveOIDCUser(ctx, "https://accounts.google.com", NewID("subject"), email, "Provider audit", func() (string, error) { return "unusable", nil })
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = st.Pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, userID) }()
	if err := st.CreateIdentityProviderSession(ctx, HashToken(NewID("session")), userID, "id-token", "https://accounts.google.com", "subject", "", "google", time.Hour); err != nil {
		t.Fatal(err)
	}
	var action, provider string
	if err := st.Pool.QueryRow(ctx, `
		SELECT action,detail->>'provider' FROM organization_audit_events
		WHERE actor_id=$1 AND action='identity.login' ORDER BY id DESC LIMIT 1`, userID).Scan(&action, &provider); err != nil {
		t.Fatal(err)
	}
	if action != "identity.login" || provider != "google" {
		t.Fatalf("audit = %q %q", action, provider)
	}
}
