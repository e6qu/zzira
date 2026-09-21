package store

import (
	"context"
	"os"
	"testing"

	"github.com/e6qu/zzira/internal/models"
)

// Every page's navigation asks whether the reader is service desk staff, and
// that answer walks the site's desks. A desk whose project has been trashed
// is not one anybody administers: it must not fail the page somebody else is
// reading, which is what a deletion between listing the desks and asking
// about each of them used to do.
func TestServiceDeskStaffSurvivesATrashedProject(t *testing.T) {
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
	workspaceID, _, err := st.DefaultWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	actorID, err := st.FirstAdminID(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	project, err := st.CreateProject(ctx, actorID, models.Project{
		WorkspaceID: workspaceID, Key: "SDN" + NewID("t")[len(NewID("t"))-4:], Name: "Desk navigation test",
		LeadAccountID: actorID, AssigneeType: "UNASSIGNED", ProjectTypeKey: "service_desk",
	}, "kanban")
	if err != nil {
		t.Fatalf("create service project: %v", err)
	}
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM service_desks WHERE project_id=$1`, project.ID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, project.ID)
	})
	// Somebody reading a page who administers nothing here.
	readerID := NewID("usr")
	reader, err := st.CreateUser(ctx, readerID, "desk-navigation-"+readerID+"@zzira.dev", "", "Desk Reader")
	if err != nil {
		t.Fatalf("create the reader: %v", err)
	}
	if err := st.AddMember(ctx, workspaceID, reader.ID, "member"); err != nil {
		t.Fatalf("add the reader to the workspace: %v", err)
	}
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM workspace_members WHERE user_id=$1`, reader.ID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, reader.ID)
	})

	if _, err := st.IsAnyServiceDeskStaff(ctx, workspaceID, reader.ID); err != nil {
		t.Fatalf("reading who is service desk staff: %v", err)
	}
	// The desks as somebody's page listed them, a moment before the project
	// went: exactly what the navigation holds when another person's deletion
	// lands between the two statements.
	desks, err := st.ServiceDesks(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	listed := false
	for _, desk := range desks {
		listed = listed || desk.ProjectID == project.ID
	}
	if !listed {
		t.Fatal("the new service desk was not listed, so the test proves nothing")
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE projects SET lifecycle_state='TRASHED' WHERE id=$1`, project.ID); err != nil {
		t.Fatal(err)
	}
	administered, err := st.serviceDesksAdministeredFrom(ctx, workspaceID, reader.ID, desks)
	if err != nil {
		t.Fatalf("a service desk project trashed mid-read failed the navigation: %v", err)
	}
	for _, desk := range administered {
		if desk.ProjectID == project.ID {
			t.Fatal("a desk whose project is gone was called one the reader administers")
		}
	}
	// The whole question still answers, and the reader administers nothing.
	staff, err := st.IsAnyServiceDeskStaff(ctx, workspaceID, reader.ID)
	if err != nil {
		t.Fatalf("reading who is service desk staff after the deletion: %v", err)
	}
	if staff {
		t.Fatal("a reader who administers nothing was called service desk staff")
	}
}
