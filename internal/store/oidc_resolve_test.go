package store

import (
	"context"
	"os"
	"testing"
)

// unusedPasswordHash fails the test if invoked -- ResolveOIDCUser must only
// call its password-hash generator when it is actually provisioning a new
// user, never on the existing-user sign-in path (hashing is deliberately
// expensive and must not run on every login).
func unusedPasswordHash(t *testing.T) func() (string, error) {
	t.Helper()
	return func() (string, error) {
		t.Fatal("ResolveOIDCUser hashed a password for a user that already existed")
		return "", nil
	}
}

// TestResolveOIDCUserProvisionsANewMemberOnFirstSignIn covers the actual
// authorization boundary for ZZIRA's OIDC integration: the identity provider
// (Shauth's own catalog registration and GitHub-org role mapping) already
// decided this person may reach ZZIRA. ZZIRA must not additionally require an
// operator to have pre-invited the exact email by hand, or every real member
// the identity provider already vetted is silently locked out.
func TestResolveOIDCUserProvisionsANewMemberOnFirstSignIn(t *testing.T) {
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

	const issuer = "https://auth.dev.e6qu.dev"
	subject := NewID("sub")
	email := subject + "@example.invalid"
	hashCalls := 0
	newHash := func() (string, error) {
		hashCalls++
		return "$2a$10$unusable", nil
	}

	userID, err := st.ResolveOIDCUser(ctx, issuer, subject, email, "New Member", newHash)
	if err != nil {
		t.Fatalf("first sign-in did not provision a new member: %v", err)
	}
	defer func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM oidc_identities WHERE issuer=$1 AND subject=$2`, issuer, subject)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM memberships WHERE user_id=$1`, userID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, userID)
	}()
	if hashCalls != 1 {
		t.Fatalf("password hash generator called %d times, want exactly 1", hashCalls)
	}

	user, err := st.UserByID(ctx, userID)
	if err != nil {
		t.Fatalf("provisioned user is not readable: %v", err)
	}
	if user.Email != email {
		t.Fatalf("provisioned user email = %q, want %q", user.Email, email)
	}

	workspaceID, _, err := st.DefaultWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var role string
	if err := st.Pool.QueryRow(ctx, `SELECT role FROM memberships WHERE workspace_id=$1 AND user_id=$2`, workspaceID, userID).Scan(&role); err != nil {
		t.Fatalf("provisioned user was not granted default-workspace membership: %v", err)
	}
	if role != "member" {
		t.Fatalf("provisioned member role = %q, want %q", role, "member")
	}

	// A later sign-in with the same (issuer, subject) must bind to the same
	// user and must never hash a password again.
	again, err := st.ResolveOIDCUser(ctx, issuer, subject, email, "New Member", unusedPasswordHash(t))
	if err != nil {
		t.Fatalf("repeat sign-in: %v", err)
	}
	if again != userID {
		t.Fatalf("repeat sign-in resolved user %q, want the same user %q", again, userID)
	}
}

// TestResolveOIDCUserBindsAnExistingActiveMemberWithoutHashing covers the
// pre-existing-member path: a user an operator (or ZZIRA's own seed data)
// already created with this email binds by email on first sign-in, and the
// password-hash generator -- meant only for provisioning a brand-new account
// -- must never run.
func TestResolveOIDCUserBindsAnExistingActiveMemberWithoutHashing(t *testing.T) {
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

	const issuer = "https://auth.dev.e6qu.dev"
	userID := NewID("usr")
	email := userID + "@example.invalid"
	if _, err := st.CreateUser(ctx, userID, email, "test", userID); err != nil {
		t.Fatal(err)
	}
	subject := userID + "-sub"
	defer func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM oidc_identities WHERE issuer=$1 AND subject=$2`, issuer, subject)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, userID)
	}()

	bound, err := st.ResolveOIDCUser(ctx, issuer, subject, email, "New Member", unusedPasswordHash(t))
	if err != nil {
		t.Fatalf("bind existing member: %v", err)
	}
	if bound != userID {
		t.Fatalf("bound user = %q, want the existing user %q", bound, userID)
	}
}
