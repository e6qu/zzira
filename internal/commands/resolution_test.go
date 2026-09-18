package commands

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/store"
	"github.com/e6qu/zzira/internal/workflow"
)

// Jira sets a resolution from a transition screen or an edit, and clears it
// when work reopens. Reaching a done status without one still applies the
// site's default, as our workflows do.
func TestResolutionComesFromTheTransitionOrTheEdit(t *testing.T) {
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
	ws, actor, projectID, workflowID := store.NewID("ws"), store.NewID("usr"), store.NewID("prj"), store.NewID("wf")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Resolutions')`, ws)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Resolution actor')`, actor, actor+"@example.test")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'admin')`, ws, actor)
	exec(`INSERT INTO projects(id,workspace_id,key,name,lead_account_id) VALUES($1,$2,$3,'Resolutions',$4)`,
		projectID, ws, "RS"+strings.ToUpper(projectID[len(projectID)-4:]), actor)
	t.Cleanup(func() {
		exec(`DELETE FROM actions WHERE workspace_id=$1`, ws)
		exec(`DELETE FROM issues WHERE workspace_id=$1`, ws)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, ws)
		exec(`DELETE FROM workflow_schemes WHERE workspace_id=$1`, ws)
		exec(`DELETE FROM workflows WHERE workspace_id=$1`, ws)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, ws)
		exec(`DELETE FROM workspaces WHERE id=$1`, ws)
		exec(`DELETE FROM users WHERE id=$1`, actor)
	})

	// A workflow whose Done transition asks for a resolution, and whose reopen
	// transition does not.
	wf := workflow.Workflow{ID: workflowID, Name: "Resolution gates", Transitions: []workflow.Transition{
		{ID: "1", Name: "Create", Type: workflow.TransitionInitial, To: "st_todo"},
		{ID: "31", Name: "Done", From: []string{"st_todo"}, To: "st_done",
			Screen: &workflow.Rule{ID: "screen", RuleKey: workflow.RuleTransitionScreen, Parameters: map[string]string{"fields": "resolution"}}},
		{ID: "11", Name: "Reopen", From: []string{"st_done"}, To: "st_todo"},
	}}
	if err := st.CreateWorkflow(ctx, ws, wf); err != nil {
		t.Fatal(err)
	}
	if err := st.AssignWorkflowToProject(ctx, ws, projectID, workflowID); err != nil {
		t.Fatal(err)
	}

	svc := &Service{Store: st}
	raise := func(summary string) string {
		t.Helper()
		issue, _, err := svc.CreateIssue(ctx, CreateIssueInput{
			ActorID: actor, WorkspaceID: ws, ProjectIDOrKey: projectID, Summary: summary, IssueTypeID: "it_task",
		})
		if err != nil {
			t.Fatal(err)
		}
		return issue.ID
	}
	resolutionOf := func(issueID string) string {
		t.Helper()
		issue, err := st.IssueByIDOrKey(ctx, ws, issueID)
		if err != nil {
			t.Fatal(err)
		}
		if issue.Resolution == nil {
			return ""
		}
		return issue.Resolution.Name
	}

	// The transition screen's resolution is the one recorded.
	chosen := raise("finished a different way")
	duplicate := "Duplicate"
	if _, _, err := svc.TransitionIssueWithUpdate(ctx, actor, ws, chosen, "31", store.IssueUpdate{ResolutionID: &duplicate}); err != nil {
		t.Fatal(err)
	}
	if got := resolutionOf(chosen); got != "Duplicate" {
		t.Fatalf("resolution from the transition screen = %q", got)
	}

	// Without one, the site's default still records how work finished.
	fallback := raise("finished the usual way")
	if _, _, err := svc.TransitionIssue(ctx, actor, ws, fallback, "31"); err != nil {
		t.Fatal(err)
	}
	if got := resolutionOf(fallback); got != "Done" {
		t.Fatalf("resolution without a screen = %q", got)
	}

	// Reopening clears it, as leaving a done status does in Jira.
	if _, _, err := svc.TransitionIssue(ctx, actor, ws, chosen, "11"); err != nil {
		t.Fatal(err)
	}
	if got := resolutionOf(chosen); got != "" {
		t.Fatalf("reopened work kept the resolution %q", got)
	}

	// An edit sets and clears it too.
	wontDo := "Won't Do"
	if _, _, err := svc.UpdateIssue(ctx, UpdateIssueInput{
		ActorID: actor, WorkspaceID: ws, IssueIDOrKey: chosen, ResolutionID: &wontDo,
	}); err != nil {
		t.Fatal(err)
	}
	if got := resolutionOf(chosen); got != "Won't Do" {
		t.Fatalf("resolution from an edit = %q", got)
	}
	cleared := ""
	if _, _, err := svc.UpdateIssue(ctx, UpdateIssueInput{
		ActorID: actor, WorkspaceID: ws, IssueIDOrKey: chosen, ResolutionID: &cleared,
	}); err != nil {
		t.Fatal(err)
	}
	if got := resolutionOf(chosen); got != "" {
		t.Fatalf("cleared resolution = %q", got)
	}

	// A resolution the site does not have is refused, and a transition whose
	// screen does not ask for one refuses it.
	unknown := "Shipped sideways"
	if _, _, err := svc.UpdateIssue(ctx, UpdateIssueInput{
		ActorID: actor, WorkspaceID: ws, IssueIDOrKey: chosen, ResolutionID: &unknown,
	}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("an unknown resolution = %v", err)
	}
	offScreen := raise("no resolution on this screen")
	if _, _, err := svc.TransitionIssueWithUpdate(ctx, actor, ws, offScreen, "31", store.IssueUpdate{ResolutionID: &duplicate}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.TransitionIssueWithUpdate(ctx, actor, ws, offScreen, "11", store.IssueUpdate{ResolutionID: &duplicate}); err == nil ||
		!strings.Contains(err.Error(), "not available on transition") {
		t.Fatalf("a resolution the transition screen does not ask for = %v", err)
	}
}
