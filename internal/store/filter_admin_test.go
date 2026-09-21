package store

import (
	"context"
	"errors"
	"os"
	"testing"
)

// Jira's Create shared objects permission governs sharing a filter with
// anyone beyond the people it names.
func TestSharingAFilterNeedsCreateSharedObjects(t *testing.T) {
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
	if err = Migrate(ctx, st.Pool); err != nil {
		t.Fatal(err)
	}
	workspaceID, ownerID := NewID("ws"), NewID("usr")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Sharing test')`, workspaceID)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Filter owner')`, ownerID, ownerID+"@example.test")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'member')`, workspaceID, ownerID)
	t.Cleanup(func() {
		exec(`DELETE FROM filters WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		exec(`DELETE FROM users WHERE id=$1`, ownerID)
	})
	filter, err := st.CreateManagedFilter(ctx, NewID("flt"), workspaceID, ownerID, FilterDetails{Name: "Mine", JQL: "ORDER BY created DESC"})
	if err != nil {
		t.Fatal(err)
	}
	// Every workspace grants it to its product users, so sharing works.
	if _, err = st.AddFilterPermission(ctx, workspaceID, ownerID, filter.ID, FilterPermissionInput{Type: "global", Rights: 1}); err != nil {
		t.Fatalf("sharing with the permission: %v", err)
	}
	// Taking the grant away takes sharing with it.
	exec(`DELETE FROM global_permission_grants WHERE workspace_id=$1 AND permission_key='CREATE_SHARED_OBJECTS'`, workspaceID)
	if _, err = st.AddFilterPermission(ctx, workspaceID, ownerID, filter.ID, FilterPermissionInput{Type: "authenticated", Rights: 1}); !errors.Is(err, ErrFilterPermission) {
		t.Fatalf("sharing without the permission = %v", err)
	}
	// Naming one person is not sharing broadly, and is still allowed -- on a
	// filter that is not already shared with everyone, which the store
	// refuses for its own reasons.
	private, err := st.CreateManagedFilter(ctx, NewID("flt"), workspaceID, ownerID, FilterDetails{Name: "Private", JQL: "ORDER BY created DESC"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.AddFilterPermission(ctx, workspaceID, ownerID, private.ID, FilterPermissionInput{Type: "user", Rights: 1, AccountID: ownerID}); err != nil {
		t.Fatalf("sharing with one person: %v", err)
	}
}
