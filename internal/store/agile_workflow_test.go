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
	otherWorkspaceID := NewID("ws_workflow_test")
	otherProjectID := NewID("project_workflow_test")
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(ctx, `UPDATE projects SET workflow_id='wf_default' WHERE id='prj_default'`)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, otherProjectID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM workflows WHERE id=$1`, workflowID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM workspaces WHERE id=$1`, otherWorkspaceID)
	})
	if _, err := st.Pool.Exec(ctx, `INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Workflow isolation')`, otherWorkspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, `INSERT INTO projects(id,workspace_id,key,name) VALUES($1,$2,'ISO','Isolation')`, otherProjectID, otherWorkspaceID); err != nil {
		t.Fatal(err)
	}

	if err := st.CreateWorkflow(ctx, "ws_default", workflow.Workflow{ID: workflowID, Name: "Invalid"}); err == nil {
		t.Fatal("empty transition set must be rejected")
	}
	if err := st.CreateWorkflow(ctx, "ws_default", workflow.Workflow{
		ID: workflowID, Name: "Invalid",
		Transitions: []workflow.Transition{{ID: transitionID, Name: "Unknown", From: []string{"missing"}, To: "st_done"}},
	}); err == nil {
		t.Fatal("unknown source status must be rejected")
	}

	wf := workflow.Default()
	wf.ID = workflowID
	wf.Name = "Test delivery"
	if err := st.CreateWorkflow(ctx, "ws_default", wf); err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	stored, err := st.WorkflowByID(ctx, "ws_default", workflowID)
	if err != nil {
		t.Fatalf("load workflow: %v", err)
	}
	if stored.ID != workflowID || stored.Name != wf.Name || len(stored.Transitions) != len(wf.Transitions) {
		t.Fatalf("stored workflow = %+v, want %+v", stored, wf)
	}
	if _, err := st.WorkflowByID(ctx, otherWorkspaceID, workflowID); err == nil {
		t.Fatal("custom workflow must not be visible from another workspace")
	}
	if err := st.AssignWorkflowToProject(ctx, otherWorkspaceID, otherProjectID, workflowID); err == nil {
		t.Fatal("custom workflow must not be assignable across workspaces")
	}
	otherWorkflows, err := st.ListWorkflows(ctx, otherWorkspaceID)
	if err != nil || len(otherWorkflows) != 1 || otherWorkflows[0].ID != workflow.Default().ID {
		t.Fatalf("other workspace workflows = %+v, %v", otherWorkflows, err)
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
	draft.Description = "A positioned delivery workflow"
	draft.Statuses = []workflow.StatusLayout{
		{StatusReference: "st_todo", Layout: &workflow.Layout{X: 40, Y: 72}, Properties: map[string]string{}},
		{StatusReference: "st_inprogress", Layout: &workflow.Layout{X: 340, Y: 72}, Properties: map[string]string{}},
		{StatusReference: "st_done", Layout: &workflow.Layout{X: 640, Y: 72}, Properties: map[string]string{}},
	}
	draft.Transitions = append(draft.Transitions, workflow.Transition{ID: transitionID, Name: "Review", From: []string{"st_inprogress"}, To: "st_done"})
	if err := st.SaveWorkflowDraft(ctx, "ws_default", draft); err != nil {
		t.Fatalf("save draft: %v", err)
	}
	publishedBefore, err := st.WorkflowByID(ctx, "ws_default", workflowID)
	if err != nil || len(publishedBefore.Transitions) != len(wf.Transitions) || publishedBefore.Description != "" || len(publishedBefore.Statuses) != 0 || !publishedBefore.HasDraft {
		t.Fatalf("published workflow changed before publish: %+v, %v", publishedBefore, err)
	}
	editorDraft, err := st.WorkflowDraftByID(ctx, "ws_default", workflowID)
	if err != nil || !editorDraft.HasDraft || len(editorDraft.Transitions) != len(wf.Transitions)+1 || editorDraft.Description != draft.Description || len(editorDraft.Statuses) != 3 || editorDraft.Statuses[1].Layout.X != 340 {
		t.Fatalf("editor draft = %+v, %v", editorDraft, err)
	}
	if err := st.PublishWorkflowDraft(ctx, workspaceID, actorID, workflowID); err != nil {
		t.Fatalf("publish draft: %v", err)
	}
	published, err := st.WorkflowByID(ctx, "ws_default", workflowID)
	if err != nil || published.HasDraft || published.Version != 2 || len(published.Transitions) != len(wf.Transitions)+1 || published.Description != draft.Description || len(published.Statuses) != 3 || published.Statuses[2].Layout.X != 640 {
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
	afterDiscard, err := st.WorkflowByID(ctx, "ws_default", workflowID)
	if err != nil || afterDiscard.HasDraft || len(afterDiscard.Transitions) != len(published.Transitions) {
		t.Fatalf("workflow after discard = %+v, %v", afterDiscard, err)
	}
	if err := st.AssignWorkflowToProject(ctx, "ws_default", "prj_default", workflowID); err != nil {
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
