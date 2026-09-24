package store

import (
	"context"
	"os"
	"testing"

	"github.com/e6qu/zzira/internal/workflow"
)

// A workflow's global and initial transitions carry no source statuses, and
// its definition keeps "from": null for them. Reading the statuses a
// project's active workflows use must read that shape as an empty list:
// unwrapping it as an array failed with "cannot extract elements from a
// scalar", and the project's status read failed with it.
func TestStatusesInProjectWorkflowsReadGlobalAndInitialTransitions(t *testing.T) {
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
	if err := Migrate(ctx, st.Pool); err != nil {
		t.Fatal(err)
	}

	workspaceID, projectID, workflowID := NewID("ws"), NewID("prj"), NewID("wf")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Workflow status reads')`, workspaceID)
	exec(`INSERT INTO projects(id,workspace_id,key,name) VALUES($1,$2,'WFR','Workflow reads')`, projectID, workspaceID)
	t.Cleanup(func() {
		drop := func(query string, args ...any) { _, _ = st.Pool.Exec(ctx, query, args...) }
		drop(`UPDATE projects SET workflow_id='wf_default' WHERE id=$1`, projectID)
		drop(`DELETE FROM projects WHERE id=$1`, projectID)
		drop(`DELETE FROM workflows WHERE id=$1`, workflowID)
		drop(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
	})

	err = st.CreateWorkflow(ctx, workspaceID, workflow.Workflow{
		ID:   workflowID,
		Name: "Status reads",
		Transitions: []workflow.Transition{
			{ID: "tr_initial", Name: "Create", Type: workflow.TransitionInitial, To: "st_todo"},
			{ID: "tr_start", Name: "Start", To: "st_inprogress", From: []string{"st_todo"}},
			{ID: "tr_done", Name: "Done", To: "st_done", From: []string{"st_inprogress"}},
			{ID: "tr_reopen", Name: "Reopen", Type: workflow.TransitionGlobal, To: "st_inprogress"},
		},
	})
	if err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	exec(`UPDATE projects SET workflow_id=$2 WHERE id=$1`, projectID, workflowID)

	statuses, err := st.StatusesInProjectWorkflows(ctx, workspaceID, []string{projectID})
	if err != nil {
		t.Fatalf("statuses in project workflows: %v", err)
	}
	seen := make(map[string]bool, len(statuses))
	for _, status := range statuses {
		seen[status.ID] = true
	}
	for _, want := range []string{"st_todo", "st_inprogress", "st_done"} {
		if !seen[want] {
			t.Fatalf("status %q missing from the project's active workflows, got %v", want, statuses)
		}
	}
}
