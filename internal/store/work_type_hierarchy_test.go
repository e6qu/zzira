package store_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/store"
)

// A site starts with Jira's three levels and an administrator adds named
// levels above Epic, moves work types onto them, and removes them again.
func TestWorkTypeHierarchyLevels(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if err := store.Migrate(ctx, st.Pool); err != nil {
		t.Fatal(err)
	}
	ws, admin, member := store.NewID("ws"), store.NewID("usr"), store.NewID("usr")
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Hierarchy test')`, ws)
	for _, person := range []struct{ id, role string }{{admin, "admin"}, {member, "member"}} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Hierarchy person')`, person.id, person.id+"@example.test")
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,$3)`, ws, person.id, person.role)
	}
	t.Cleanup(func() {
		exec(`DELETE FROM issue_metadata_overrides WHERE workspace_id=$1`, ws)
		exec(`DELETE FROM issue_types WHERE workspace_id=$1`, ws)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, ws)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, ws)
		exec(`DELETE FROM workspaces WHERE id=$1`, ws)
		exec(`DELETE FROM users WHERE id = ANY($1)`, []string{admin, member})
	})

	// Jira's three levels, top down.
	levels, err := st.HierarchyLevels(ctx, ws)
	if err != nil {
		t.Fatal(err)
	}
	if len(levels) != 3 || levels[0].Name != "Epic" || levels[0].Level != 1 || levels[2].Level != -1 {
		t.Fatalf("levels = %+v", levels)
	}

	// Only a site administrator changes the hierarchy.
	if _, err := st.AddHierarchyLevel(ctx, ws, member, "Initiative"); !errors.Is(err, store.ErrProjectPermission) {
		t.Fatalf("a member added a level: %v", err)
	}
	initiative, err := st.AddHierarchyLevel(ctx, ws, admin, "Initiative")
	if err != nil {
		t.Fatal(err)
	}
	if initiative.Level != 2 {
		t.Fatalf("the new level = %+v", initiative)
	}
	if _, err := st.AddHierarchyLevel(ctx, ws, admin, "initiative"); !errors.Is(err, store.ErrHierarchyValidation) {
		t.Fatalf("a duplicate level name was accepted: %v", err)
	}

	// A work type moves onto the new level, and its work items then take a
	// parent from the level above it.
	workType, err := st.CreateIssueType(ctx, ws, "Programme "+strings.ToUpper(ws[len(ws)-4:]), "A programme of work.", "standard", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetWorkTypeHierarchyLevel(ctx, ws, member, workType.ID, 2); !errors.Is(err, store.ErrProjectPermission) {
		t.Fatalf("a member moved a work type: %v", err)
	}
	moved, err := st.SetWorkTypeHierarchyLevel(ctx, ws, admin, workType.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if moved.HierarchyLevel != 2 {
		t.Fatalf("moved work type = %+v", moved)
	}
	if _, err := st.SetWorkTypeHierarchyLevel(ctx, ws, admin, workType.ID, 7); !errors.Is(err, store.ErrHierarchyValidation) {
		t.Fatalf("a level the site does not have was accepted: %v", err)
	}

	// A shared default moves for this site only.
	if _, err := st.SetWorkTypeHierarchyLevel(ctx, ws, admin, "Story", 2); err != nil {
		t.Fatal(err)
	}
	if types, err := st.IssueTypesForWorkspace(ctx, ws); err != nil {
		t.Fatal(err)
	} else {
		for _, issueType := range types {
			if issueType.Name == "Story" && issueType.HierarchyLevel != 2 {
				t.Fatalf("Story stayed at level %d", issueType.HierarchyLevel)
			}
		}
	}
	var shared int
	if err := st.Pool.QueryRow(ctx, `SELECT hierarchy_level FROM issue_types WHERE id='it_story'`).Scan(&shared); err != nil {
		t.Fatal(err)
	}
	if shared != 0 {
		t.Fatalf("the shared default moved for every site: level %d", shared)
	}

	// Renaming, and the fixed levels Jira does not let anyone rename.
	if err := st.RenameHierarchyLevel(ctx, ws, admin, 2, "Initiatives"); err != nil {
		t.Fatal(err)
	}
	for _, fixed := range []int{0, -1} {
		if err := st.RenameHierarchyLevel(ctx, ws, admin, fixed, "Anything"); !errors.Is(err, store.ErrHierarchyValidation) {
			t.Fatalf("level %d was renamed: %v", fixed, err)
		}
	}
	if name, err := st.HierarchyLevelName(ctx, ws, 2); err != nil || name != "Initiatives" {
		t.Fatalf("level name = %q (%v)", name, err)
	}

	// A level with work types stays until they leave, Epic and below stay for
	// good, and only the top level goes.
	if err := st.DeleteHierarchyLevel(ctx, ws, admin, 2); !errors.Is(err, store.ErrHierarchyValidation) {
		t.Fatalf("an occupied level was removed: %v", err)
	}
	for _, name := range []string{workType.ID, "Story"} {
		if _, err := st.SetWorkTypeHierarchyLevel(ctx, ws, admin, name, 0); err != nil {
			t.Fatal(err)
		}
	}
	for _, fixed := range []int{1, 0, -1} {
		if err := st.DeleteHierarchyLevel(ctx, ws, admin, fixed); !errors.Is(err, store.ErrHierarchyValidation) {
			t.Fatalf("level %d was removed: %v", fixed, err)
		}
	}
	if err := st.DeleteHierarchyLevel(ctx, ws, admin, 2); err != nil {
		t.Fatal(err)
	}
	if levels, err = st.HierarchyLevels(ctx, ws); err != nil || len(levels) != 3 {
		t.Fatalf("levels after removal = %+v (%v)", levels, err)
	}

	// Every change is in the action log.
	var actions int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM actions WHERE workspace_id=$1 AND entity_id LIKE 'hierarchy:%'`, ws).Scan(&actions); err != nil {
		t.Fatal(err)
	}
	if actions < 4 {
		t.Fatalf("hierarchy actions = %d", actions)
	}
}
