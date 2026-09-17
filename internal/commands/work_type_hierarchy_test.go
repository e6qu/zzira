package commands

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/store"
)

// A work item's parent is a work item one level above it, at every level of
// the site's hierarchy — not only an epic above base work.
func TestParentComesFromTheLevelAbove(t *testing.T) {
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
	ws, actor, projectID := store.NewID("ws"), store.NewID("usr"), store.NewID("prj")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Levels')`, ws)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Level actor')`, actor, actor+"@example.test")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'admin')`, ws, actor)
	exec(`INSERT INTO projects(id,workspace_id,key,name,workflow_id,lead_account_id) VALUES($1,$2,$3,'Levels','wf_default',$4)`,
		projectID, ws, "LV"+strings.ToUpper(projectID[len(projectID)-4:]), actor)
	t.Cleanup(func() {
		exec(`DELETE FROM actions WHERE workspace_id=$1`, ws)
		exec(`DELETE FROM issues WHERE workspace_id=$1`, ws)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, ws)
		exec(`DELETE FROM issue_metadata_overrides WHERE workspace_id=$1`, ws)
		exec(`DELETE FROM issue_types WHERE workspace_id=$1`, ws)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, ws)
		exec(`DELETE FROM workspaces WHERE id=$1`, ws)
		exec(`DELETE FROM users WHERE id=$1`, actor)
	})

	// An Initiative level above Epic, with a work type on it.
	if _, err := st.AddHierarchyLevel(ctx, ws, actor, "Initiative"); err != nil {
		t.Fatal(err)
	}
	initiativeType, err := st.CreateIssueType(ctx, ws, "Initiative "+strings.ToUpper(ws[len(ws)-4:]), "A body of epics.", "standard", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetWorkTypeHierarchyLevel(ctx, ws, actor, initiativeType.ID, 2); err != nil {
		t.Fatal(err)
	}

	svc := &Service{Store: st}
	create := func(issueTypeID, summary, parent string) (string, error) {
		issue, _, err := svc.CreateIssue(ctx, CreateIssueInput{
			ActorID: actor, WorkspaceID: ws, ProjectIDOrKey: projectID,
			Summary: summary, IssueTypeID: issueTypeID, ParentIDOrKey: parent,
		})
		if err != nil {
			return "", err
		}
		return issue.ID, nil
	}
	initiative, err := create(initiativeType.ID, "Grow the platform", "")
	if err != nil {
		t.Fatal(err)
	}
	epic, err := create("it_epic", "Search", initiative)
	if err != nil {
		t.Fatalf("an epic under an initiative: %v", err)
	}
	task, err := create("it_task", "Index pages", epic)
	if err != nil {
		t.Fatalf("a task under an epic: %v", err)
	}

	// Two levels up, one level down, and a sibling are all refused.
	if _, err := create("it_task", "Skipping a level", initiative); !errors.Is(err, ErrParentHierarchy) {
		t.Fatalf("a task under an initiative: %v", err)
	}
	if _, err := create("it_epic", "Under a task", task); !errors.Is(err, ErrParentHierarchy) {
		t.Fatalf("an epic under a task: %v", err)
	}
	if _, err := create(initiativeType.ID, "Under an epic", epic); !errors.Is(err, ErrParentHierarchy) {
		t.Fatalf("an initiative under an epic: %v", err)
	}

	// An edit follows the same rule.
	wrong := initiative
	if _, _, err := svc.UpdateIssue(ctx, UpdateIssueInput{
		ActorID: actor, WorkspaceID: ws, IssueIDOrKey: task, ParentIDOrKey: &wrong,
	}); !errors.Is(err, ErrParentHierarchy) {
		t.Fatalf("editing a task's parent to an initiative: %v", err)
	}
	stored, err := st.IssueByIDOrKey(ctx, ws, task)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Parent == nil || stored.Parent.ID != epic {
		t.Fatalf("the refused edit changed the parent: %+v", stored.Parent)
	}
}
