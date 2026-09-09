package store

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/e6qu/zzira/internal/workflow"
)

func TestAPITaskRunnerCancellationAndStaleClaimRecovery(t *testing.T) {
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
	workspaceID, _, err := st.DefaultWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	actorID, err := st.FirstAdminID(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	projectID, schemeID := NewID("task_project"), NewID("task_scheme")
	if _, err := st.Pool.Exec(ctx, `INSERT INTO projects(id,workspace_id,key,name,workflow_id) VALUES($1,$2,$3,'Task queue project','wf_default')`, projectID, workspaceID, "TQ"+projectID[len(projectID)-5:]); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateWorkflowScheme(ctx, workspaceID, actorID, workflow.Scheme{ID: schemeID, Name: "Task queue " + schemeID[len(schemeID)-6:], DefaultWorkflowID: workflow.Default().ID}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM api_tasks WHERE workspace_id=$1 AND (payload->>'projectId'=$2 OR payload->>'workflowSchemeId'=$3)`, workspaceID, projectID, schemeID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, projectID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM organization_audit_events WHERE target_id=$1`, schemeID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM workflow_schemes WHERE id=$1`, schemeID)
	})

	runner := &APITaskRunner{Store: st}
	cancelledTask, err := st.SwitchWorkflowSchemeTask(ctx, workspaceID, actorID, projectID, schemeID, nil)
	if err != nil || cancelledTask.Status != "ENQUEUED" {
		t.Fatalf("enqueue cancelled task = %+v, %v", cancelledTask, err)
	}
	claimed, err := runner.claim(ctx, workspaceID)
	if err != nil || claimed.ID != cancelledTask.ID || claimed.Status != "RUNNING" || claimed.StartedAt == nil {
		t.Fatalf("claimed task = %+v, %v", claimed, err)
	}
	if task, err := st.CancelAPITask(ctx, workspaceID, claimed.ID); err != nil || task.Status != "CANCELLED" || task.FinishedAt == nil {
		t.Fatalf("cancel running task = %+v, %v", task, err)
	}
	if err := runner.execute(ctx, claimed); !errors.Is(err, ErrAPITaskCancelled) {
		t.Fatalf("cancelled execution error = %v", err)
	}
	var assignedScheme string
	if err := st.Pool.QueryRow(ctx, `SELECT COALESCE(workflow_scheme_id,'') FROM projects WHERE id=$1`, projectID).Scan(&assignedScheme); err != nil || assignedScheme != "" {
		t.Fatalf("cancelled task changed project scheme=%q err=%v", assignedScheme, err)
	}

	recoveredTask, err := st.SwitchWorkflowSchemeTask(ctx, workspaceID, actorID, projectID, schemeID, nil)
	if err != nil {
		t.Fatal(err)
	}
	firstClaim, err := runner.claim(ctx, workspaceID)
	if err != nil || firstClaim.ID != recoveredTask.ID {
		t.Fatalf("first recovery claim = %+v, %v", firstClaim, err)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE api_tasks SET last_update_at=now()-interval '3 minutes' WHERE id=$1`, recoveredTask.ID); err != nil {
		t.Fatal(err)
	}
	recoveredClaim, err := runner.claim(ctx, workspaceID)
	if err != nil || recoveredClaim.ID != recoveredTask.ID || recoveredClaim.Status != "RUNNING" {
		t.Fatalf("recovered claim = %+v, %v", recoveredClaim, err)
	}
	if err := runner.execute(ctx, recoveredClaim); err != nil {
		t.Fatal(err)
	}
	completed, err := st.APITaskByID(ctx, workspaceID, recoveredTask.ID)
	if err != nil || completed.Status != "COMPLETE" || completed.Progress != 100 || completed.FinishedAt == nil {
		t.Fatalf("completed recovered task = %+v, %v", completed, err)
	}
	if err := st.Pool.QueryRow(ctx, `SELECT COALESCE(workflow_scheme_id,'') FROM projects WHERE id=$1`, projectID).Scan(&assignedScheme); err != nil || assignedScheme != schemeID {
		t.Fatalf("recovered task project scheme=%q err=%v", assignedScheme, err)
	}
	if _, err := st.CancelAPITask(ctx, workspaceID, recoveredTask.ID); !errors.Is(err, ErrAPITaskNotCancellable) {
		t.Fatalf("completed task cancellation error = %v", err)
	}
	failedTask, err := queuedAPITask(workspaceID, actorID, "Unsupported test task", "unsupported-test-kind", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.enqueueAPITask(ctx, failedTask); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = st.Pool.Exec(ctx, `DELETE FROM api_tasks WHERE id=$1`, failedTask.ID) })
	if err := runner.DrainOnce(ctx, workspaceID); err != nil {
		t.Fatal(err)
	}
	failed, err := st.APITaskByID(ctx, workspaceID, failedTask.ID)
	if err != nil || failed.Status != "FAILED" || failed.Progress != 100 || failed.FinishedAt == nil || len(failed.Result) == 0 {
		t.Fatalf("failed task = %+v, %v", failed, err)
	}
}
