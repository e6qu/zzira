package store

import (
	"context"
	"os"
	"testing"
)

func TestDefaultWorkspacePrefersSeededDefault(t *testing.T) {
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

	workspaceID := "ws_000_" + NewID("test")
	if _, err := st.Pool.Exec(ctx, `INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Default selection test')`, workspaceID); err != nil {
		t.Fatal(err)
	}
	var organizationID string
	if err := st.Pool.QueryRow(ctx, `SELECT organization_id::text FROM sites WHERE workspace_id=$1`, workspaceID).Scan(&organizationID); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM workspaces WHERE id=$1`, workspaceID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM organizations WHERE id::text=$1`, organizationID)
	}()

	gotID, gotSlug, err := st.DefaultWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if gotID != "ws_default" || gotSlug != "zzira" {
		t.Fatalf("DefaultWorkspace()=(%q,%q), want (ws_default,zzira)", gotID, gotSlug)
	}
}

func TestEnsureBootstrapAdmin(t *testing.T) {
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

	wsID, _, err := st.DefaultWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	email := NewID("bootstrap") + "@example.invalid"
	defer func() {
		var userID string
		if err := st.Pool.QueryRow(ctx, `SELECT id FROM users WHERE email=$1`, email).Scan(&userID); err == nil {
			_, _ = st.Pool.Exec(ctx, `DELETE FROM memberships WHERE workspace_id=$1 AND user_id=$2`, wsID, userID)
			_, _ = st.Pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, userID)
		}
	}()

	if err := st.EnsureBootstrapAdmin(ctx, email, "Bootstrap Admin", "unusable-hash", "admin"); err != nil {
		t.Fatalf("first ensure: %v", err)
	}
	var userID, role string
	if err := st.Pool.QueryRow(ctx, `SELECT id FROM users WHERE email=$1`, email).Scan(&userID); err != nil {
		t.Fatalf("user was not created: %v", err)
	}
	if err := st.Pool.QueryRow(ctx, `SELECT role FROM memberships WHERE workspace_id=$1 AND user_id=$2`, wsID, userID).Scan(&role); err != nil {
		t.Fatalf("membership was not created: %v", err)
	}
	if role != "admin" {
		t.Fatalf("expected admin role, got %q", role)
	}

	// Second call (as happens on every boot) must not fail or duplicate
	// anything, and must not touch an existing user's fields.
	if err := st.EnsureBootstrapAdmin(ctx, email, "A Different Name", "a-different-hash", "admin"); err != nil {
		t.Fatalf("second ensure: %v", err)
	}
	var displayName, passwordHash string
	if err := st.Pool.QueryRow(ctx, `SELECT display_name, password_hash FROM users WHERE email=$1`, email).Scan(&displayName, &passwordHash); err != nil {
		t.Fatal(err)
	}
	if displayName != "Bootstrap Admin" || passwordHash != "unusable-hash" {
		t.Fatalf("re-running EnsureBootstrapAdmin must not modify an existing user, got display_name=%q password_hash=%q", displayName, passwordHash)
	}

	if _, err := st.Pool.Exec(ctx, `UPDATE memberships SET role='member' WHERE workspace_id=$1 AND user_id=$2`, wsID, userID); err != nil {
		t.Fatal(err)
	}
	if err := st.EnsureBootstrapAdmin(ctx, email, "Ignored", "ignored", "admin"); err != nil {
		t.Fatalf("promote existing member: %v", err)
	}
	if err := st.Pool.QueryRow(ctx, `SELECT role FROM memberships WHERE workspace_id=$1 AND user_id=$2`, wsID, userID).Scan(&role); err != nil {
		t.Fatal(err)
	}
	if role != "admin" {
		t.Fatalf("bootstrap account role=%q after promotion, want admin", role)
	}
}
