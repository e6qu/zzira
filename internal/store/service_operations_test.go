package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/models"
)

func TestServiceOperationsPolicyAndOnCallAudit(t *testing.T) {
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

	project, err := st.CreateProject(ctx, actorID, models.Project{
		WorkspaceID: workspaceID, Key: "OP" + NewID("test"), Name: "Operations governance test",
		LeadAccountID: actorID, AssigneeType: "UNASSIGNED", ProjectTypeKey: "service_desk",
	}, "kanban")
	if err != nil {
		t.Fatalf("create service project: %v", err)
	}
	var deskID string
	if err := st.Pool.QueryRow(ctx, `SELECT id FROM service_desks WHERE project_id=$1`, project.ID).Scan(&deskID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM organization_audit_events WHERE target_id=$1 OR detail->>'serviceDeskId'=$1`, deskID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, project.ID)
	})

	if err := st.UpdateServiceOperationsSettings(ctx, workspaceID, actorID, deskID, 12, 2, []string{actorID}); err != nil {
		t.Fatalf("update settings: %v", err)
	}
	settings, err := st.ServiceOperationsSettings(ctx, workspaceID, deskID)
	if err != nil {
		t.Fatal(err)
	}
	if settings.CABRiskThreshold != 12 || settings.ReviewDueDays != 2 || len(settings.CABMembers) != 1 || settings.CABMembers[0].ID != actorID {
		t.Fatalf("settings = %+v", settings)
	}

	startsAt := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	endsAt := startsAt.Add(8 * time.Hour)
	if err := st.CreateServiceOnCallShift(ctx, workspaceID, actorID, deskID, actorID, "Primary", startsAt, endsAt); err != nil {
		t.Fatalf("create shift: %v", err)
	}
	settings, err = st.ServiceOperationsSettings(ctx, workspaceID, deskID)
	if err != nil || len(settings.OnCallShifts) != 1 || settings.OnCallShifts[0].StartsAt.Location() != time.UTC || !settings.OnCallShifts[0].StartsAt.Equal(startsAt) || !settings.OnCallShifts[0].EndsAt.Equal(endsAt) {
		t.Fatalf("on-call shifts = %+v, %v", settings.OnCallShifts, err)
	}
	shiftID := settings.OnCallShifts[0].ID
	if err := st.DeleteServiceOnCallShift(ctx, workspaceID, actorID, deskID, shiftID); err != nil {
		t.Fatalf("delete shift: %v", err)
	}
	settings, err = st.ServiceOperationsSettings(ctx, workspaceID, deskID)
	if err != nil || len(settings.OnCallShifts) != 0 {
		t.Fatalf("shifts after delete = %+v, %v", settings.OnCallShifts, err)
	}

	var auditCount int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM organization_audit_events WHERE (target_id=$1 OR detail->>'serviceDeskId'=$1) AND action IN ('service_operations_settings_updated','service_on_call_shift_created','service_on_call_shift_deleted')`, deskID).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 3 {
		t.Fatalf("operations audit count = %d, want 3", auditCount)
	}
}
