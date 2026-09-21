package store

import (
	"context"
	"errors"
	"os"
	"testing"
)

// TestProjectsWithPermissionsOutlivesAVanishedProject covers the seam between
// listing the projects of a site and asking, project by project, what one
// person may do with them. They are separate statements: a project archived,
// trashed or deleted in between is gone by the time it is asked about, and
// the page being built belongs to somebody who had nothing to do with that.
func TestProjectsWithPermissionsOutlivesAVanishedProject(t *testing.T) {
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

	workspaceID, userID := NewID("ws"), NewID("usr")
	staying, going := NewID("prj"), NewID("prj")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM workspaces WHERE id=$1`, workspaceID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, userID)
	})
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Project listing')`, workspaceID)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Project reader')`, userID, userID+"@example.test")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'member')`, workspaceID, userID)
	exec(`INSERT INTO projects(id,workspace_id,key,name) VALUES($1,$2,'STAY','Still here')`, staying, workspaceID)
	exec(`INSERT INTO projects(id,workspace_id,key,name) VALUES($1,$2,'GONE','On its way out')`, going, workspaceID)

	// What the question says about a project that is no longer active. A
	// project in the trash is on its way out; a deleted one is not there at
	// all, and both read the same here.
	exec(`UPDATE projects SET lifecycle_state='TRASHED' WHERE id=$1`, going)
	if _, err := st.HasProjectPermission(ctx, workspaceID, userID, going, "", "BROWSE_PROJECTS"); !errors.Is(err, ErrPermissionSchemeNotFound) {
		t.Fatalf("permission on a trashed project: %v, want ErrPermissionSchemeNotFound", err)
	}

	// What the listing does with that answer: it leaves the project out,
	// rather than failing the whole page over it.
	granted, err := st.projectPermissionGranted(ctx, workspaceID, userID, going, "BROWSE_PROJECTS")
	if err != nil || granted {
		t.Fatalf("granted on a trashed project = %v, %v; want false and no error", granted, err)
	}
	projects, err := st.ProjectsWithPermissions(ctx, workspaceID, userID, []string{"BROWSE_PROJECTS"})
	if err != nil {
		t.Fatalf("listing projects with one trashed: %v", err)
	}
	for _, project := range projects {
		if project.ID == going {
			t.Fatal("a trashed project was listed")
		}
	}
	// A project that is removed outright, after the listing has named it, is
	// the same to the reader: their page is built without it.
	exec(`DELETE FROM projects WHERE id=$1`, going)
	if granted, err := st.projectPermissionGranted(ctx, workspaceID, userID, going, "BROWSE_PROJECTS"); err != nil || granted {
		t.Fatalf("granted on a removed project = %v, %v; want false and no error", granted, err)
	}
}
