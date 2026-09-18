package commands_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

// A service desk administrator runs the portal: they add request types, name
// them, put them in the groups the portal lists them under, and arrange both
// orders. A request type in no group is not on the portal, which is what Jira
// Service Management does with a request type created over REST.
func TestServiceRequestTypesAndPortalGroups(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if err := store.Migrate(ctx, st.Pool); err != nil {
		t.Fatal(err)
	}
	workspaceID, _, err := st.DefaultWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	adminID, err := st.FirstAdminID(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	customerID := store.NewID("usr")
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Portal customer')`, customerID, customerID+"@example.invalid")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'member')`, workspaceID, customerID)
	t.Cleanup(func() {
		exec(`DELETE FROM memberships WHERE user_id=$1`, customerID)
		exec(`DELETE FROM users WHERE id=$1`, customerID)
	})

	service := &commands.Service{Store: st}
	projectKey := "R" + time.Now().UTC().Format("150405000")
	project, err := service.CreateProject(ctx, adminID, workspaceID, commands.CreateProjectInput{Key: projectKey, Name: "Request type settings", LeadAccountID: adminID,
		ProjectTypeKey: "service_desk", ProjectTemplateKey: "com.atlassian.servicedesk:simplified-it-service-management"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = st.Pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, project.ID) })
	var deskID string
	if err := st.Pool.QueryRow(ctx, `SELECT id FROM service_desks WHERE project_id=$1`, project.ID).Scan(&deskID); err != nil {
		t.Fatal(err)
	}
	workTypes, err := st.ProjectIssueTypes(ctx, workspaceID, project.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	workTypeID := ""
	for _, workType := range workTypes {
		if !workType.Subtask {
			workTypeID = workType.ID
			break
		}
	}
	if workTypeID == "" {
		t.Fatal("the service project offers no work type")
	}
	portal := func() []models.ServicePortalGroup {
		t.Helper()
		groups, err := st.ServicePortalGroups(ctx, workspaceID, deskID, "")
		if err != nil {
			t.Fatal(err)
		}
		return groups
	}
	names := func(groups []models.ServicePortalGroup, groupID string) []string {
		t.Helper()
		for _, group := range groups {
			if group.Group.ID != groupID {
				continue
			}
			out := []string{}
			for _, requestType := range group.RequestTypes {
				out = append(out, requestType.Name)
			}
			return out
		}
		t.Fatalf("the portal has no %s group: %+v", groupID, groups)
		return nil
	}

	// Only the desk's administrators change its request types.
	if _, err := service.CreateServiceRequestType(ctx, customerID, workspaceID, deskID, "Order a laptop", "", "", workTypeID, []string{"help"}); err == nil ||
		!strings.Contains(err.Error(), "administrator access is required") {
		t.Fatalf("a customer creating a request type = %v", err)
	}
	if _, err := service.CreateServiceRequestTypeGroup(ctx, customerID, workspaceID, deskID, "Hardware"); err == nil ||
		!strings.Contains(err.Error(), "administrator access is required") {
		t.Fatalf("a customer creating a group = %v", err)
	}

	// A request type needs a name Jira accepts.
	if _, err := service.CreateServiceRequestType(ctx, adminID, workspaceID, deskID, "  ", "", "", workTypeID, nil); err == nil ||
		!strings.Contains(err.Error(), "1 to 255 characters on one line") {
		t.Fatalf("an unnamed request type = %v", err)
	}
	if _, err := service.CreateServiceRequestType(ctx, adminID, workspaceID, deskID, "Get IT help", "", "", workTypeID, nil); err == nil ||
		!strings.Contains(err.Error(), "already exists") {
		t.Fatalf("a duplicate request type name = %v", err)
	}
	if _, err := service.CreateServiceRequestType(ctx, adminID, workspaceID, deskID, "Order a laptop", "", "", workTypeID, []string{"nowhere"}); !errors.Is(err, store.ErrServiceRequestTypeGroupNotFound) {
		t.Fatalf("a request type in a group the desk does not have = %v", err)
	}

	// A request type created in no group is off the portal until it joins one.
	ungrouped, err := service.CreateServiceRequestType(ctx, adminID, workspaceID, deskID, "Order a laptop", "Ask for hardware.", "Say which model.", workTypeID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(ungrouped.GroupIDs) != 0 {
		t.Fatalf("a request type created without a group = %+v", ungrouped)
	}
	hidden, err := st.ServiceUngroupedRequestTypes(ctx, workspaceID, deskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(hidden) != 1 || hidden[0].ID != ungrouped.ID {
		t.Fatalf("request types kept off the portal = %+v", hidden)
	}
	for _, group := range portal() {
		for _, requestType := range group.RequestTypes {
			if requestType.ID == ungrouped.ID {
				t.Fatalf("a request type in no group is on the portal under %s", group.Group.Name)
			}
		}
	}

	// A group is added after the ones already there, and can be moved up.
	hardware, err := service.CreateServiceRequestTypeGroup(ctx, adminID, workspaceID, deskID, "Hardware and devices")
	if err != nil {
		t.Fatal(err)
	}
	groups := portal()
	if groups[len(groups)-1].Group.ID != hardware.ID {
		t.Fatalf("a new group is not last: %+v", groups)
	}
	if err := service.MoveServiceRequestTypeGroup(ctx, adminID, workspaceID, deskID, hardware.ID, "up"); err != nil {
		t.Fatal(err)
	}
	groups = portal()
	if groups[len(groups)-2].Group.ID != hardware.ID {
		t.Fatalf("a group moved up is not second to last: %+v", groups)
	}
	if err := service.MoveServiceRequestTypeGroup(ctx, adminID, workspaceID, deskID, hardware.ID, "sideways"); err == nil ||
		!strings.Contains(err.Error(), "up or down") {
		t.Fatalf("a group moved sideways = %v", err)
	}

	// Putting the request type in the group shows it on the portal.
	if err := service.SetServiceRequestTypeGroups(ctx, adminID, workspaceID, deskID, ungrouped.ID, []string{hardware.ID}); err != nil {
		t.Fatal(err)
	}
	if got := names(portal(), hardware.ID); len(got) != 1 || got[0] != "Order a laptop" {
		t.Fatalf("the hardware group shows %v", got)
	}

	// A second request type joins the group after the first, and the two can
	// be arranged the way the portal should list them.
	monitor, err := service.CreateServiceRequestType(ctx, adminID, workspaceID, deskID, "Ask for a monitor", "", "", workTypeID, []string{hardware.ID})
	if err != nil {
		t.Fatal(err)
	}
	if got := names(portal(), hardware.ID); strings.Join(got, ",") != "Order a laptop,Ask for a monitor" {
		t.Fatalf("the hardware group shows %v", got)
	}
	if err := service.MoveServiceRequestType(ctx, adminID, workspaceID, deskID, hardware.ID, monitor.ID, "up"); err != nil {
		t.Fatal(err)
	}
	if got := names(portal(), hardware.ID); strings.Join(got, ",") != "Ask for a monitor,Order a laptop" {
		t.Fatalf("after moving a request type up the group shows %v", got)
	}
	// The first request type in a group stays where it is.
	if err := service.MoveServiceRequestType(ctx, adminID, workspaceID, deskID, hardware.ID, monitor.ID, "up"); err != nil {
		t.Fatal(err)
	}
	if got := names(portal(), hardware.ID); strings.Join(got, ",") != "Ask for a monitor,Order a laptop" {
		t.Fatalf("after moving the first request type up the group shows %v", got)
	}

	// Renaming a request type changes what the portal offers.
	if err := service.UpdateServiceRequestType(ctx, customerID, workspaceID, deskID, monitor.ID, "Ask for a screen", "", ""); err == nil ||
		!strings.Contains(err.Error(), "administrator access is required") {
		t.Fatalf("a customer renaming a request type = %v", err)
	}
	if err := service.UpdateServiceRequestType(ctx, adminID, workspaceID, deskID, monitor.ID, "Ask for a screen", "A second display.", "Say which size."); err != nil {
		t.Fatal(err)
	}
	if got := names(portal(), hardware.ID); strings.Join(got, ",") != "Ask for a screen,Order a laptop" {
		t.Fatalf("after renaming a request type the group shows %v", got)
	}
	if err := service.UpdateServiceRequestType(ctx, adminID, workspaceID, deskID, monitor.ID, "Get IT help", "", ""); err == nil ||
		!strings.Contains(err.Error(), "already exists") {
		t.Fatalf("renaming a request type onto another name = %v", err)
	}

	// A group that still shows request types cannot be deleted; emptying it
	// takes its request types off the portal and lets the group go.
	if err := service.DeleteServiceRequestTypeGroup(ctx, adminID, workspaceID, deskID, hardware.ID); !errors.Is(err, store.ErrServiceRequestTypeGroupInUse) {
		t.Fatalf("deleting a group that holds request types = %v", err)
	}
	if err := service.RenameServiceRequestTypeGroup(ctx, adminID, workspaceID, deskID, hardware.ID, "Devices"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{ungrouped.ID, monitor.ID} {
		if err := service.SetServiceRequestTypeGroups(ctx, adminID, workspaceID, deskID, id, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := service.DeleteServiceRequestTypeGroup(ctx, adminID, workspaceID, deskID, hardware.ID); err != nil {
		t.Fatal(err)
	}
	for _, group := range portal() {
		if group.Group.ID == hardware.ID {
			t.Fatalf("a deleted group is still on the portal: %+v", group)
		}
	}
	if err := service.DeleteServiceRequestTypeGroup(ctx, adminID, workspaceID, deskID, hardware.ID); !errors.Is(err, store.ErrServiceRequestTypeGroupNotFound) {
		t.Fatalf("deleting a group twice = %v", err)
	}

	// The site's audit log records what the administrator did.
	for _, action := range []string{"service.request_type.created", "service.request_type.updated", "service.request_type.grouped",
		"service.request_type.moved", "service.request_type_group.created", "service.request_type_group.updated",
		"service.request_type_group.moved", "service.request_type_group.deleted"} {
		var recorded bool
		if err := st.Pool.QueryRow(ctx, `SELECT TRUE FROM organization_audit_events WHERE action=$1 AND actor_id=$2 LIMIT 1`, action, adminID).Scan(&recorded); err != nil {
			t.Fatalf("audit event %s: %v", action, err)
		}
	}

	// Deleting a request type is the administrator's alone.
	if err := service.DeleteServiceRequestType(ctx, customerID, workspaceID, deskID, monitor.ID); err == nil ||
		!strings.Contains(err.Error(), "administrator access is required") {
		t.Fatalf("a customer deleting a request type = %v", err)
	}
	if err := service.DeleteServiceRequestType(ctx, adminID, workspaceID, deskID, monitor.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ServiceRequestType(ctx, workspaceID, deskID, monitor.ID); err == nil {
		t.Fatal("a deleted request type is still there")
	}
}
