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
	if _, err := st.Pool.Exec(ctx, `INSERT INTO workflows(id,name,def,workspace_id) VALUES($1,$2,$3,$4)`, workflowID, wf.Name, def, workspaceID); err != nil {
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

func TestStatusBatchesCommitAsOneMutation(t *testing.T) {
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

	firstID, secondID, rolledBackID := NewID("status_batch"), NewID("status_batch"), NewID("status_batch")
	firstName, secondName := "Batch first "+firstID, "Batch second "+secondID
	ids := []string{firstID, secondID, rolledBackID}
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM organization_audit_events WHERE target_id=ANY($1::text[])`, ids)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM statuses WHERE id=ANY($1::text[])`, ids)
	})

	created, err := st.CreateStatuses(ctx, workspaceID, actorID, []models.Status{
		{ID: firstID, Name: firstName, Category: "new"},
		{ID: secondID, Name: secondName, Category: "indeterminate"},
	})
	if err != nil || len(created) != 2 {
		t.Fatalf("create batch = %+v, %v", created, err)
	}
	_, err = st.CreateStatuses(ctx, workspaceID, actorID, []models.Status{
		{ID: rolledBackID, Name: "Must roll back " + rolledBackID, Category: "new"},
		{Name: firstName, Category: "done"},
	})
	if !errors.Is(err, ErrAdminConflict) {
		t.Fatalf("conflicting create batch error = %v", err)
	}
	var rolledBackRows, rolledBackAudit int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM statuses WHERE id=$1`, rolledBackID).Scan(&rolledBackRows); err != nil {
		t.Fatal(err)
	}
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM organization_audit_events WHERE target_id=$1`, rolledBackID).Scan(&rolledBackAudit); err != nil {
		t.Fatal(err)
	}
	if rolledBackRows != 0 || rolledBackAudit != 0 {
		t.Fatalf("create rollback rows=%d audit=%d", rolledBackRows, rolledBackAudit)
	}

	err = st.UpdateStatuses(ctx, workspaceID, actorID, []models.Status{
		{ID: firstID, Name: "Must not persist", Category: "done"},
		{ID: NewID("missing_status"), Name: "Missing", Category: "new"},
	})
	if !errors.Is(err, ErrAdminNotFound) {
		t.Fatalf("missing update batch error = %v", err)
	}
	var unchanged string
	if err := st.Pool.QueryRow(ctx, `SELECT name FROM statuses WHERE id=$1`, firstID).Scan(&unchanged); err != nil || unchanged != firstName {
		t.Fatalf("rolled-back update name=%q err=%v", unchanged, err)
	}

	if err := st.UpdateStatuses(ctx, workspaceID, actorID, []models.Status{
		{ID: firstID, Name: secondName, Category: "done"},
		{ID: secondID, Name: firstName, Category: "new"},
	}); err != nil {
		t.Fatalf("swap names in update batch: %v", err)
	}
	var swappedFirst, swappedSecond string
	if err := st.Pool.QueryRow(ctx, `SELECT name FROM statuses WHERE id=$1`, firstID).Scan(&swappedFirst); err != nil {
		t.Fatal(err)
	}
	if err := st.Pool.QueryRow(ctx, `SELECT name FROM statuses WHERE id=$1`, secondID).Scan(&swappedSecond); err != nil {
		t.Fatal(err)
	}
	if swappedFirst != secondName || swappedSecond != firstName {
		t.Fatalf("swapped names = %q, %q", swappedFirst, swappedSecond)
	}

	if err := st.DeleteStatuses(ctx, workspaceID, actorID, []string{firstID, "st_todo"}); !errors.Is(err, ErrAdminConflict) {
		t.Fatalf("protected delete batch error = %v", err)
	}
	var firstStillExists bool
	if err := st.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM statuses WHERE id=$1)`, firstID).Scan(&firstStillExists); err != nil || !firstStillExists {
		t.Fatalf("delete rollback exists=%v err=%v", firstStillExists, err)
	}
	if err := st.DeleteStatuses(ctx, workspaceID, actorID, []string{firstID, secondID}); err != nil {
		t.Fatalf("delete batch: %v", err)
	}
}
