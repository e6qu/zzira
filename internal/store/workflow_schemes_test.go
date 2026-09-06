package store

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/e6qu/zzira/internal/workflow"
)

func TestWorkflowSchemeDraftRuntimeAndSafeAssignment(t *testing.T) {
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
	workspaceID, _, err := st.DefaultWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	actorID, err := st.FirstAdminID(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	workflowAID, workflowBID := NewID("workflow_scheme_test"), NewID("workflow_scheme_test")
	schemeID, projectID, issueID := NewID("scheme_test"), NewID("project_scheme_test"), NewID("issue_scheme_test")
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM issues WHERE id=$1`, issueID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, projectID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM organization_audit_events WHERE target_id=$1`, schemeID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM workflow_schemes WHERE id=$1`, schemeID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM workflows WHERE id=ANY($1)`, []string{workflowAID, workflowBID})
	})
	wfA := workflow.Default()
	wfA.ID, wfA.Name = workflowAID, "Full lifecycle"
	if err := st.CreateWorkflow(ctx, workspaceID, wfA); err != nil {
		t.Fatal(err)
	}
	wfB := workflow.Workflow{ID: workflowBID, Name: "Simple lifecycle", Transitions: []workflow.Transition{
		{ID: NewID("transition"), Name: "Complete", From: []string{"st_todo"}, To: "st_done"},
		{ID: NewID("transition"), Name: "Reopen", From: []string{"st_done"}, To: "st_todo"},
	}}
	if err := st.CreateWorkflow(ctx, workspaceID, wfB); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, `INSERT INTO projects(id,workspace_id,key,name,workflow_id) VALUES($1,$2,$3,'Scheme project','wf_default')`, projectID, workspaceID, "SC"+projectID[len(projectID)-4:]); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, `INSERT INTO issues(id,workspace_id,project_id,key,summary,status_id,issuetype_id,updated_seq) VALUES($1,$2,$3,$4,'Needs migration','st_inprogress','it_task',0)`, issueID, workspaceID, projectID, "SC"+issueID[len(issueID)-8:]); err != nil {
		t.Fatal(err)
	}
	scheme, err := st.CreateWorkflowScheme(ctx, workspaceID, actorID, workflow.Scheme{ID: schemeID, Name: "Scheme test " + schemeID[len(schemeID)-6:], DefaultWorkflowID: workflowBID})
	if err != nil {
		t.Fatal(err)
	}
	impact, err := st.WorkflowSchemeImpact(ctx, workspaceID, projectID, schemeID, false)
	if err != nil || len(impact) != 1 || impact[0].Status.ID != "st_inprogress" || impact[0].IssueCount != 1 {
		t.Fatalf("impact = %+v, %v", impact, err)
	}
	if err := st.AssignWorkflowScheme(ctx, workspaceID, actorID, projectID, schemeID); !errors.Is(err, ErrAdminConflict) {
		t.Fatalf("unsafe assignment error = %v", err)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE issues SET status_id='st_todo' WHERE id=$1`, issueID); err != nil {
		t.Fatal(err)
	}
	if err := st.AssignWorkflowScheme(ctx, workspaceID, actorID, projectID, schemeID); err != nil {
		t.Fatalf("safe assignment: %v", err)
	}
	runtime, err := st.WorkflowForProjectAndIssueType(ctx, projectID, "it_task")
	if err != nil || runtime.ID != workflowBID {
		t.Fatalf("default runtime = %+v, %v", runtime, err)
	}
	scheme.IssueTypeMappings = map[string]string{"it_task": workflowAID}
	if err := st.SaveWorkflowSchemeDraft(ctx, workspaceID, actorID, scheme); err != nil {
		t.Fatal(err)
	}
	before, err := st.WorkflowForProjectAndIssueType(ctx, projectID, "it_task")
	if err != nil || before.ID != workflowBID {
		t.Fatalf("draft leaked into runtime = %+v, %v", before, err)
	}
	if err := st.PublishWorkflowSchemeDraft(ctx, workspaceID, actorID, schemeID); err != nil {
		t.Fatal(err)
	}
	after, err := st.WorkflowForProjectAndIssueType(ctx, projectID, "it_task")
	if err != nil || after.ID != workflowAID {
		t.Fatalf("mapped runtime = %+v, %v", after, err)
	}
	published, err := st.WorkflowSchemeByID(ctx, workspaceID, schemeID, false)
	if err != nil || published.HasDraft || published.Version != 2 {
		t.Fatalf("published scheme = %+v, %v", published, err)
	}
	var auditEvents int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM organization_audit_events WHERE target_type='workflow_scheme' AND target_id=$1 AND action IN ('workflow.scheme.created','workflow.scheme.assigned','workflow.scheme.published')`, schemeID).Scan(&auditEvents); err != nil || auditEvents != 3 {
		t.Fatalf("scheme audit events = %d, %v", auditEvents, err)
	}
}
