package store

import (
	"context"
	"os"
	"testing"
)

func TestUserByIDUsernameFallsBackToEmailLocalPart(t *testing.T) {
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

	userID := NewID("usr")
	email := "someone.distinctive@example.invalid"
	if _, err := st.CreateUser(ctx, userID, email, "test", "Someone"); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = st.Pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, userID) }()

	u, err := st.UserByID(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if u.Username != "someone.distinctive" {
		t.Fatalf("expected username to fall back to the email local part, got %q", u.Username)
	}

	if err := st.SetOIDCUsername(ctx, userID, "sso-handle"); err != nil {
		t.Fatalf("set OIDC username: %v", err)
	}
	u, err = st.UserByID(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if u.Username != "sso-handle" {
		t.Fatalf("expected the OIDC-provided username to take precedence, got %q", u.Username)
	}
}
