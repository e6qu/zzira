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
	workspaceID, _, err := st.DefaultWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	actorID, err := st.FirstAdminID(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	draft := stored
	draft.Transitions = append(draft.Transitions, workflow.Transition{ID: transitionID, Name: "Review", From: []string{"st_inprogress"}, To: "st_done"})
	if err := st.SaveWorkflowDraft(ctx, "ws_default", draft); err != nil {
		t.Fatalf("save draft: %v", err)
	}
	publishedBefore, err := st.WorkflowByID(ctx, workflowID)
	if err != nil || len(publishedBefore.Transitions) != len(wf.Transitions) || !publishedBefore.HasDraft {
		t.Fatalf("published workflow changed before publish: %+v, %v", publishedBefore, err)
	}
	editorDraft, err := st.WorkflowDraftByID(ctx, workflowID)
	if err != nil || !editorDraft.HasDraft || len(editorDraft.Transitions) != len(wf.Transitions)+1 {
		t.Fatalf("editor draft = %+v, %v", editorDraft, err)
	}
	if err := st.PublishWorkflowDraft(ctx, workspaceID, actorID, workflowID); err != nil {
		t.Fatalf("publish draft: %v", err)
	}
	published, err := st.WorkflowByID(ctx, workflowID)
	if err != nil || published.HasDraft || published.Version != 2 || len(published.Transitions) != len(wf.Transitions)+1 {
		t.Fatalf("published workflow = %+v, %v", published, err)
	}
	discarded := published
	discarded.Transitions = discarded.Transitions[:len(discarded.Transitions)-1]
	if err := st.SaveWorkflowDraft(ctx, "ws_default", discarded); err != nil {
		t.Fatal(err)
	}
	if err := st.DiscardWorkflowDraft(ctx, workspaceID, actorID, workflowID); err != nil {
		t.Fatal(err)
	}
	afterDiscard, err := st.WorkflowByID(ctx, workflowID)
	if err != nil || afterDiscard.HasDraft || len(afterDiscard.Transitions) != len(published.Transitions) {
		t.Fatalf("workflow after discard = %+v, %v", afterDiscard, err)
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
