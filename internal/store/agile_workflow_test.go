package store

import (
	"context"
	"os"
	"testing"

	"github.com/e6qu/zzira/internal/workflow"
)

func TestWorkflowPersistenceValidatesDefinitionsAndAssignments(t *testing.T) {
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

	workflowID := NewID("workflow_test")
	transitionID := NewID("transition_test")
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(ctx, `UPDATE projects SET workflow_id='wf_default' WHERE id='prj_default'`)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM workflows WHERE id=$1`, workflowID)
	})

	if err := st.CreateWorkflow(ctx, workflow.Workflow{ID: workflowID, Name: "Invalid"}); err == nil {
		t.Fatal("empty transition set must be rejected")
	}
	if err := st.CreateWorkflow(ctx, workflow.Workflow{
		ID: workflowID, Name: "Invalid",
		Transitions: []workflow.Transition{{ID: transitionID, Name: "Unknown", From: []string{"missing"}, To: "st_done"}},
	}); err == nil {
		t.Fatal("unknown source status must be rejected")
	}

	wf := workflow.Default()
	wf.ID = workflowID
	wf.Name = "Test delivery"
	if err := st.CreateWorkflow(ctx, wf); err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	stored, err := st.WorkflowByID(ctx, workflowID)
	if err != nil {
		t.Fatalf("load workflow: %v", err)
	}
	if stored.ID != workflowID || stored.Name != wf.Name || len(stored.Transitions) != len(wf.Transitions) {
		t.Fatalf("stored workflow = %+v, want %+v", stored, wf)
	}
	if err := st.AssignWorkflowToProject(ctx, "prj_default", workflowID); err != nil {
		t.Fatalf("assign workflow: %v", err)
	}
	assigned, err := st.WorkflowForProject(ctx, "prj_default")
	if err != nil {
		t.Fatalf("workflow for project: %v", err)
	}
	if assigned.ID != workflowID {
		t.Fatalf("assigned workflow id = %q, want %q", assigned.ID, workflowID)
	}
}
