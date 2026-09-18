package store_test

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/store"
	"github.com/e6qu/zzira/internal/workflow"
)

// A transition that names a screen asks for what the screen holds now, so
// adding a field to the screen changes the transition without touching the
// workflow, and removing one takes it away again.
func TestTransitionFollowsItsScreen(t *testing.T) {
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
	ws, admin, projectID, workflowID := store.NewID("ws"), store.NewID("usr"), store.NewID("prj"), store.NewID("wf")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Screen workflow')`, ws)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Screen admin')`, admin, admin+"@example.test")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'admin')`, ws, admin)
	exec(`INSERT INTO projects(id,workspace_id,key,name,workflow_id,lead_account_id) VALUES($1,$2,$3,'Screen workflow','wf_default',$4)`,
		projectID, ws, "SW"+strings.ToUpper(projectID[len(projectID)-4:]), admin)
	t.Cleanup(func() {
		for _, statement := range []string{
			`DELETE FROM actions WHERE workspace_id=$1`,
			`DELETE FROM issues WHERE workspace_id=$1`,
			`DELETE FROM projects WHERE workspace_id=$1`,
			`DELETE FROM workflow_schemes WHERE workspace_id=$1`,
			`DELETE FROM workflows WHERE workspace_id=$1`,
			`DELETE FROM screen_tab_fields WHERE workspace_id=$1`,
			`DELETE FROM memberships WHERE workspace_id=$1`,
			`DELETE FROM workspaces WHERE id=$1`,
		} {
			exec(statement, ws)
		}
		exec(`DELETE FROM users WHERE id=$1`, admin)
	})

	screen, err := st.CreateScreen(ctx, ws, admin, "Resolve screen", "What we ask when work finishes")
	if err != nil {
		t.Fatal(err)
	}
	tab, err := st.AddScreenTab(ctx, ws, admin, screen.ID, "Fields")
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"resolution", "assignee"} {
		if _, err := st.AddScreenTabField(ctx, ws, admin, screen.ID, tab.ID, field); err != nil {
			t.Fatalf("add %s: %v", field, err)
		}
	}

	definition := workflow.Workflow{ID: workflowID, Name: "Screen workflow", Transitions: []workflow.Transition{
		{ID: "1", Name: "Create", Type: workflow.TransitionInitial, To: "st_todo"},
		{ID: "31", Name: "Done", From: []string{"st_todo"}, To: "st_done",
			Screen: &workflow.Rule{ID: "screen", RuleKey: workflow.RuleTransitionScreen,
				Parameters: map[string]string{store.ScreenIDParameter: screen.ID}}},
	}}
	if err := st.CreateWorkflow(ctx, ws, definition); err != nil {
		t.Fatal(err)
	}
	if err := st.AssignWorkflowToProject(ctx, ws, projectID, workflowID); err != nil {
		t.Fatal(err)
	}

	fieldsNow := func() []string {
		t.Helper()
		wf, err := st.WorkflowForProjectAndIssueType(ctx, projectID, "it_task")
		if err != nil {
			t.Fatal(err)
		}
		transition, ok := wf.Validate("31", "st_todo")
		if !ok {
			t.Fatal("the Done transition is gone")
		}
		return transition.ScreenFields()
	}
	if got := fieldsNow(); !slices.Equal(got, []string{"resolution", "assignee"}) {
		t.Fatalf("the transition asks for %v", got)
	}

	// The screen is the single place the fields live: changing it changes the
	// transition, with no workflow edit and no new version.
	if _, err := st.AddScreenTabField(ctx, ws, admin, screen.ID, tab.ID, "labels"); err != nil {
		t.Fatal(err)
	}
	if got := fieldsNow(); !slices.Contains(got, "labels") {
		t.Fatalf("a field added to the screen is missing from the transition: %v", got)
	}
	if err := st.RemoveScreenTabField(ctx, ws, admin, screen.ID, tab.ID, "assignee"); err != nil {
		t.Fatal(err)
	}
	if got := fieldsNow(); slices.Contains(got, "assignee") {
		t.Fatalf("a field removed from the screen is still on the transition: %v", got)
	}

	// The workflow records the screen, not a copy of what it held.
	stored, err := st.WorkflowByID(ctx, ws, workflowID)
	if err != nil {
		t.Fatal(err)
	}
	for _, transition := range stored.Transitions {
		if transition.ID != "31" {
			continue
		}
		if transition.Screen.Parameters[store.ScreenIDParameter] != screen.ID {
			t.Fatalf("the transition lost its screen: %+v", transition.Screen.Parameters)
		}
	}
}
