package store

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/workflow"
)

func TestStatusLifecycleProtectsReferencesAndBuiltIns(t *testing.T) {
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

	statusID := NewID("status_test")
	workflowID := NewID("workflow_test")
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM workflows WHERE id=$1`, workflowID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM statuses WHERE id=$1`, statusID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM organization_audit_events WHERE target_id=$1`, statusID)
	})

	created, err := st.CreateStatus(ctx, workspaceID, actorID, models.Status{
		ID: statusID, Name: "Ready for review", Description: "Peer review is pending.", Category: "indeterminate",
	})
	if err != nil {
		t.Fatalf("create status: %v", err)
	}
	if created.Protected || created.Name != "Ready for review" {
		t.Fatalf("created status = %+v", created)
	}
	if _, err := st.CreateStatus(ctx, workspaceID, actorID, models.Status{Name: "To Do", Category: "new"}); !errors.Is(err, ErrAdminConflict) {
		t.Fatalf("duplicate built-in name error = %v", err)
	}
	if err := st.UpdateStatus(ctx, workspaceID, actorID, models.Status{ID: "st_todo", Name: "Renamed", Category: "new"}); !errors.Is(err, ErrAdminConflict) {
		t.Fatalf("built-in update error = %v", err)
	}
	if err := st.UpdateStatus(ctx, workspaceID, actorID, models.Status{ID: statusID, Name: "In review", Description: "Review underway.", Category: "indeterminate"}); err != nil {
		t.Fatalf("update status: %v", err)
	}

	wf := workflow.Workflow{ID: workflowID, Name: "Review workflow", Transitions: []workflow.Transition{
		{ID: NewID("transition_test"), Name: "Review", From: []string{"st_todo"}, To: statusID},
		{ID: NewID("transition_test"), Name: "Finish", From: []string{statusID}, To: "st_done"},
	}}
	def, err := st.validateWorkflow(ctx, workspaceID, wf)
	if err != nil {
		t.Fatalf("workspace workflow validation: %v", err)
	}
	if _, err := st.Pool.Exec(ctx, `INSERT INTO workflows(id,name,def) VALUES($1,$2,$3)`, workflowID, wf.Name, def); err != nil {
		t.Fatalf("insert workflow: %v", err)
	}
	usage, err := st.StatusUsage(ctx, workspaceID, statusID)
	if err != nil || usage.Workflows != 1 {
		t.Fatalf("status usage = %+v, %v", usage, err)
	}
	if err := st.DeleteStatus(ctx, workspaceID, actorID, statusID); !errors.Is(err, ErrAdminConflict) {
		t.Fatalf("referenced delete error = %v", err)
	}
	if _, err := st.Pool.Exec(ctx, `DELETE FROM workflows WHERE id=$1`, workflowID); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteStatus(ctx, workspaceID, actorID, statusID); err != nil {
		t.Fatalf("delete unused status: %v", err)
	}
	var events int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM organization_audit_events WHERE target_id=$1 AND action IN ('status.created','status.updated','status.deleted')`, statusID).Scan(&events); err != nil || events != 3 {
		t.Fatalf("status audit events = %d, %v", events, err)
	}
}
