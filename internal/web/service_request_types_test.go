package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/authn"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
)

// A desk administrator runs the portal from the agent workspace: they add a
// request type group, add a request type to it, arrange the order, and the
// customer portal lists the request types under their group headings in that
// order. A request type in no group is not offered on the portal.
func TestServiceRequestTypeSettingsShapeThePortal(t *testing.T) {
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
	workspaceID, workspaceSlug, err := st.DefaultWorkspace(ctx)
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
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Portal visitor')`, customerID, customerID+"@example.invalid")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'member')`, workspaceID, customerID)
	t.Cleanup(func() {
		exec(`DELETE FROM sessions WHERE user_id=$1`, customerID)
		exec(`DELETE FROM memberships WHERE user_id=$1`, customerID)
		exec(`DELETE FROM users WHERE id=$1`, customerID)
	})

	service := &commands.Service{Store: st}
	projectKey := "P" + time.Now().UTC().Format("150405000")
	project, err := service.CreateProject(ctx, adminID, workspaceID, commands.CreateProjectInput{Key: projectKey, Name: "Portal request types", LeadAccountID: adminID,
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

	h := &Handler{Store: st, Commands: service, WorkspaceSlug: workspaceSlug}
	session := func(userID string) string {
		t.Helper()
		token, _, err := authn.LoginOIDC(ctx, st, userID, "id-token", "https://issuer.example.invalid", userID+"-subject", "")
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	adminSession, customerSession := session(adminID), session(customerID)
	post := func(token, path string, form url.Values) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, "/service/agent/"+deskID+"/"+path, strings.NewReader(form.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.SetPathValue("desk", deskID)
		request.AddCookie(&http.Cookie{Name: "zzira_session", Value: token})
		response := httptest.NewRecorder()
		if path == "request-types" {
			h.ServiceRequestTypeSettings(response, request)
		} else {
			h.ServiceRequestTypeGroupSettings(response, request)
		}
		return response
	}
	portalPage := func(token, query string) string {
		t.Helper()
		target := "/service/portals/" + deskID
		if query != "" {
			target += "?q=" + url.QueryEscape(query)
		}
		request := httptest.NewRequest(http.MethodGet, target, nil)
		request.SetPathValue("desk", deskID)
		request.AddCookie(&http.Cookie{Name: "zzira_session", Value: token})
		response := httptest.NewRecorder()
		h.ServicePortal(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("portal page = %d: %s", response.Code, response.Body.String())
		}
		return response.Body.String()
	}

	// A customer cannot change the desk's request types.
	if code := post(customerSession, "request-type-groups", url.Values{"action": {"create"}, "name": {"Hardware"}}).Code; code != http.StatusForbidden {
		t.Fatalf("a customer adding a group = %d, want 403", code)
	}
	if code := post(customerSession, "request-types", url.Values{"action": {"create"}, "name": {"Order a laptop"}, "workTypeId": {workTypeID}}).Code; code != http.StatusForbidden {
		t.Fatalf("a customer adding a request type = %d, want 403", code)
	}

	if code := post(adminSession, "request-type-groups", url.Values{"action": {"create"}, "name": {"Hardware and devices"}}).Code; code != http.StatusSeeOther {
		t.Fatalf("adding a group = %d, want 303", code)
	}
	var groupID string
	if err := st.Pool.QueryRow(ctx, `SELECT id FROM service_request_type_groups WHERE service_desk_id=$1 AND name=$2`, deskID, "Hardware and devices").Scan(&groupID); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Order a laptop", "Ask for a monitor"} {
		if code := post(adminSession, "request-types", url.Values{"action": {"create"}, "name": {name}, "description": {name + " from IT."},
			"workTypeId": {workTypeID}, "groupId": {groupID}}).Code; code != http.StatusSeeOther {
			t.Fatalf("adding the request type %s = %d, want 303", name, code)
		}
	}
	// A request type created in no group stays off the portal.
	if code := post(adminSession, "request-types", url.Values{"action": {"create"}, "name": {"Retire a server"}, "workTypeId": {workTypeID}}).Code; code != http.StatusSeeOther {
		t.Fatalf("adding an ungrouped request type = %d, want 303", code)
	}

	page := portalPage(customerSession, "")
	if !strings.Contains(page, "Hardware and devices") {
		t.Fatalf("the portal does not show the new group: %s", page)
	}
	if strings.Contains(page, "Retire a server") {
		t.Fatal("the portal offers a request type that is in no group")
	}
	laptop, monitor := strings.Index(page, "Order a laptop"), strings.Index(page, "Ask for a monitor")
	if laptop < 0 || monitor < 0 || laptop > monitor {
		t.Fatalf("the portal lists the group's request types out of order: %d, %d", laptop, monitor)
	}

	// The settings page offers the groups and every request type, and names
	// the ones the portal does not show.
	agentRequest := httptest.NewRequest(http.MethodGet, "/service/agent/"+deskID, nil)
	agentRequest.SetPathValue("desk", deskID)
	agentRequest.AddCookie(&http.Cookie{Name: "zzira_session", Value: adminSession})
	agentResponse := httptest.NewRecorder()
	h.ServiceAgent(agentResponse, agentRequest)
	if agentResponse.Code != http.StatusOK {
		t.Fatalf("the agent workspace = %d: %s", agentResponse.Code, agentResponse.Body.String())
	}
	settings := agentResponse.Body.String()
	for _, want := range []string{`id="request-types"`, "Hardware and devices", "Create request type", "Add group",
		"Not shown on the portal, because they are in no group: Retire a server."} {
		if !strings.Contains(settings, want) {
			t.Fatalf("the request type settings do not offer %q", want)
		}
	}

	// The administrator arranges the two request types the other way around.
	var monitorID string
	if err := st.Pool.QueryRow(ctx, `SELECT id FROM service_request_types WHERE service_desk_id=$1 AND name=$2`, deskID, "Ask for a monitor").Scan(&monitorID); err != nil {
		t.Fatal(err)
	}
	if code := post(adminSession, "request-types", url.Values{"action": {"move"}, "requestTypeId": {monitorID}, "groupId": {groupID}, "direction": {"up"}}).Code; code != http.StatusSeeOther {
		t.Fatalf("moving a request type up = %d, want 303", code)
	}
	page = portalPage(customerSession, "")
	if laptop, monitor = strings.Index(page, "Order a laptop"), strings.Index(page, "Ask for a monitor"); monitor > laptop {
		t.Fatalf("the portal did not follow the new order: %d, %d", laptop, monitor)
	}

	// Renaming the request type changes what the portal offers, and a search
	// still finds it under its group.
	if code := post(adminSession, "request-types", url.Values{"action": {"update"}, "requestTypeId": {monitorID}, "name": {"Ask for a screen"},
		"description": {"A second display."}, "helpText": {"Say which size."}}).Code; code != http.StatusSeeOther {
		t.Fatalf("renaming a request type = %d, want 303", code)
	}
	found := portalPage(customerSession, "screen")
	if !strings.Contains(found, "Ask for a screen") || !strings.Contains(found, "Hardware and devices") || strings.Contains(found, "Order a laptop") {
		t.Fatalf("a portal search = %s", found)
	}

	// Taking a request type out of every group hides it from the portal.
	if code := post(adminSession, "request-types", url.Values{"action": {"groups"}, "requestTypeId": {monitorID}}).Code; code != http.StatusSeeOther {
		t.Fatalf("clearing a request type's groups = %d, want 303", code)
	}
	if page = portalPage(customerSession, ""); strings.Contains(page, "Ask for a screen") {
		t.Fatal("a request type taken out of its groups is still on the portal")
	}
	if code := post(adminSession, "request-types", url.Values{"action": {"delete"}, "requestTypeId": {monitorID}}).Code; code != http.StatusSeeOther {
		t.Fatalf("deleting a request type = %d, want 303", code)
	}
	var left int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM service_request_types WHERE id=$1`, monitorID).Scan(&left); err != nil || left != 0 {
		t.Fatalf("a deleted request type = %d, %v", left, err)
	}
}
