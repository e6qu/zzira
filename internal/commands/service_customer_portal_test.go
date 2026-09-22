package commands_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/attachments"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
)

// TestAPortalCustomerWorksTheirOwnRequest holds the line that a customer with
// no seat on the site can do what the portal offers them: read their request,
// comment on it, and send a file with the comment.
func TestAPortalCustomerWorksTheirOwnRequest(t *testing.T) {
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
	blobs, err := attachments.NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := &commands.Service{Store: st, Blobs: blobs}
	projectKey := "P" + time.Now().UTC().Format("150405000")
	project, err := service.CreateProject(ctx, adminID, workspaceID, commands.CreateProjectInput{Key: projectKey, Name: "Portal customer test", LeadAccountID: adminID, ProjectTypeKey: "service_desk", ProjectTemplateKey: "com.atlassian.servicedesk:simplified-it-service-management"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = st.Pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, project.ID) })
	var deskID string
	if err := st.Pool.QueryRow(ctx, `SELECT id FROM service_desks WHERE project_id=$1`, project.ID).Scan(&deskID); err != nil {
		t.Fatal(err)
	}
	types, err := st.ServiceRequestTypes(ctx, workspaceID, deskID, "")
	if err != nil || len(types) == 0 {
		t.Fatalf("request types = %d, %v", len(types), err)
	}

	email := fmt.Sprintf("portal-%d@outside.test", time.Now().UnixNano())
	customer, err := st.CreateServiceCustomer(ctx, workspaceID, email, "Portal customer")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = st.Pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, customer.ID) })
	// A portal-only customer holds no seat on the site: that is what makes
	// them one, and it is what every check below has to cope with.
	var seated bool
	if err := st.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM memberships WHERE workspace_id=$1 AND user_id=$2)`, workspaceID, customer.ID).Scan(&seated); err != nil {
		t.Fatal(err)
	}
	if seated {
		t.Fatal("the portal customer was given a seat on the site")
	}

	raised, err := service.CreateServiceRequest(ctx, commands.CreateServiceRequestInput{
		ActorID: customer.ID, WorkspaceID: workspaceID, ServiceDeskID: deskID,
		RequestTypeID: types[0].ID, CustomerID: customer.ID, Channel: "portal",
		Summary: "The portal will not take my file", Description: "Here is what I see.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.AddServiceRequestComment(ctx, customer.ID, workspaceID, raised.Issue.ID, nil, "Any news?", true); err != nil {
		t.Fatalf("the customer could not comment on their own request: %v", err)
	}
	temporary, err := service.CreateServiceTemporaryAttachment(ctx, customer.ID, workspaceID, deskID, "screenshot.png", "image/png", strings.NewReader("not really a png, but bytes"))
	if err != nil {
		t.Fatalf("the customer could not upload a file to the desk: %v", err)
	}
	attached, comment, err := service.CreateServiceAttachmentComment(ctx, customer.ID, workspaceID, raised.Issue.ID, []string{temporary.ID}, "This is the screen.", true)
	if err != nil {
		t.Fatalf("the customer could not send the file with a comment: %v", err)
	}
	if len(attached) != 1 || attached[0].Attachment.Filename != "screenshot.png" || !comment.Public {
		t.Fatalf("attached %+v with comment %+v", attached, comment)
	}

	// Somebody else's customer account gets nowhere near the request.
	other, err := st.CreateServiceCustomer(ctx, workspaceID, fmt.Sprintf("other-%d@outside.test", time.Now().UnixNano()), "Another customer")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = st.Pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, other.ID) })
	if _, err := service.AddServiceRequestComment(ctx, other.ID, workspaceID, raised.Issue.ID, nil, "Let me in", true); err == nil {
		t.Fatal("a customer commented on a request that is not theirs")
	}
}
