package api3

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/attachments"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
)

// A customer raising a request says who it is for: themselves, or one of the
// organizations they belong to. Everybody in the organization it is shared
// with reads it; belonging to the same organization as the person who raised
// a private request is not enough, which is what a portal promises when it
// offers "Private request".
func TestServiceRequestSharedWithAnOrganization(t *testing.T) {
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
	if err = store.Migrate(ctx, st.Pool); err != nil {
		t.Fatal(err)
	}
	blobs, err := attachments.NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	workspaceID := store.NewID("ws")
	adminID, raiserID, colleagueID, outsiderID := store.NewID("usr"), store.NewID("usr"), store.NewID("usr"), store.NewID("usr")
	deskKey := fmt.Sprintf("SH%05d", time.Now().UnixNano()%100000)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Request sharing')`, workspaceID)
	people := []struct{ id, role, name string }{
		{adminID, "admin", "Site admin"}, {raiserID, "member", "Amara Okafor"},
		{colleagueID, "member", "Li Wei"}, {outsiderID, "member", "Felix Braun"},
	}
	for _, person := range people {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test',$3)`, person.id, person.id+"@example.test", person.name)
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,$3)`, workspaceID, person.id, person.role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, person.id, store.HashToken(person.id))
	}
	t.Cleanup(func() {
		exec(`DELETE FROM service_request_organizations WHERE organization_id IN (SELECT id FROM service_organizations WHERE workspace_id=$1)`, workspaceID)
		exec(`DELETE FROM service_organizations WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM notifications WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM service_desks WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM role_bindings WHERE scope_type='project' AND scope_id IN (SELECT id FROM projects WHERE workspace_id=$1)`, workspaceID)
		exec(`DELETE FROM boards WHERE project_id IN (SELECT id FROM projects WHERE workspace_id=$1)`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM jira_site_configuration WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		for _, person := range people {
			exec(`DELETE FROM api_tokens WHERE user_id=$1`, person.id)
			exec(`DELETE FROM users WHERE id=$1`, person.id)
		}
	})
	h := &Handler{Store: st, Commands: &commands.Service{Store: st, Blobs: blobs}, Blobs: blobs, WorkspaceSlug: workspaceID, BaseURL: "https://zzira.test"}
	callAs := func(accountID, method, path, body string, want int) string {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.SetBasicAuth(accountID+"@example.test", accountID)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s as %s: got %d want %d: %s", method, path, accountID, response.Code, want, response.Body.String())
		}
		return response.Body.String()
	}
	callAs(adminID, http.MethodPost, "/rest/api/3/project", `{"key":"`+deskKey+`","name":"Sharing `+deskKey+`","projectTypeKey":"service_desk","projectTemplateKey":"com.atlassian.servicedesk:simplified-it-service-management","leadAccountId":"`+adminID+`"}`, http.StatusCreated)
	var deskID, requestTypeID string
	if err = st.Pool.QueryRow(ctx, `SELECT sd.id FROM service_desks sd JOIN projects p ON p.id=sd.project_id WHERE p.workspace_id=$1 AND p.key=$2`, workspaceID, deskKey).Scan(&deskID); err != nil {
		t.Fatal(err)
	}
	if err = st.Pool.QueryRow(ctx, `SELECT id FROM service_request_types WHERE service_desk_id=$1 ORDER BY id::bigint LIMIT 1`, deskID).Scan(&requestTypeID); err != nil {
		t.Fatal(err)
	}
	// Each of them is a customer of this desk, which is what raising anything
	// through its portal makes them.
	raised := callAs(raiserID, http.MethodPost, "/rest/servicedeskapi/request", `{"serviceDeskId":"`+deskID+`","requestTypeId":"`+requestTypeID+`","requestFieldValues":{"summary":"The card reader by the loading bay is dead"}}`, http.StatusCreated)
	key := ""
	if start := strings.Index(raised, `"issueKey":"`); start >= 0 {
		key = raised[start+len(`"issueKey":"`):]
		key = key[:strings.Index(key, `"`)]
	}
	if key == "" {
		t.Fatalf("raised request = %s", raised)
	}
	requestPath := "/rest/servicedeskapi/request/" + key
	for _, customer := range []string{colleagueID, outsiderID} {
		callAs(customer, http.MethodPost, "/rest/servicedeskapi/request", `{"serviceDeskId":"`+deskID+`","requestTypeId":"`+requestTypeID+`","requestFieldValues":{"summary":"Something of their own"}}`, http.StatusCreated)
	}
	organization, err := h.Commands.CreateServiceOrganization(ctx, adminID, workspaceID, "Riverbank Foods "+deskKey)
	if err != nil {
		t.Fatal(err)
	}
	if err = h.Commands.SetServiceOrganizationUsers(ctx, adminID, workspaceID, organization.ID, []string{raiserID, colleagueID}, true); err != nil {
		t.Fatal(err)
	}
	callAs(adminID, http.MethodPost, "/rest/servicedeskapi/servicedesk/"+deskID+"/organization", `{"organizationId":`+organization.ID+`}`, http.StatusNoContent)

	// A private request is the person's own, whoever they share an
	// organization with.
	callAs(raiserID, http.MethodGet, requestPath, "", http.StatusOK)
	callAs(colleagueID, http.MethodGet, requestPath, "", http.StatusNotFound)

	shared, err := h.Commands.ShareServiceRequest(ctx, raiserID, workspaceID, key, []string{organization.ID})
	if err != nil {
		t.Fatalf("share the request: %v", err)
	}
	if len(shared) != 1 || shared[0].ID != organization.ID {
		t.Fatalf("shared with = %+v", shared)
	}
	callAs(colleagueID, http.MethodGet, requestPath, "", http.StatusOK)
	callAs(outsiderID, http.MethodGet, requestPath, "", http.StatusNotFound)

	// The colleague's portal lists it among their organization's requests.
	organizationRequests := callAs(colleagueID, http.MethodGet, "/rest/servicedeskapi/request?requestOwnership=ORGANIZATION&organizationId="+organization.ID, "", http.StatusOK)
	if !strings.Contains(organizationRequests, `"issueKey":"`+key+`"`) {
		t.Fatalf("the colleague's organization requests = %s", organizationRequests)
	}

	// Organizations searches what a request is shared with, not who its
	// customer happens to work with.
	found := callAs(adminID, http.MethodGet, "/rest/api/3/search/jql?fields=summary&jql="+url.QueryEscape(`Organizations = "`+organization.Name+`"`), "", http.StatusOK)
	if !strings.Contains(found, `"key":"`+key+`"`) {
		t.Fatalf("Organizations search = %s", found)
	}

	// An organization the customer does not belong to is not theirs to share
	// with, and the request keeps the sharing it had.
	other, err := h.Commands.CreateServiceOrganization(ctx, adminID, workspaceID, "Harbour Co "+deskKey)
	if err != nil {
		t.Fatal(err)
	}
	callAs(adminID, http.MethodPost, "/rest/servicedeskapi/servicedesk/"+deskID+"/organization", `{"organizationId":`+other.ID+`}`, http.StatusNoContent)
	if _, err = h.Commands.ShareServiceRequest(ctx, raiserID, workspaceID, key, []string{other.ID}); err == nil {
		t.Fatal("a request was shared with an organization its customer does not belong to")
	}
	callAs(colleagueID, http.MethodGet, requestPath, "", http.StatusOK)

	// Only the person who raised it or an agent decides who it is shared with.
	if _, err = h.Commands.ShareServiceRequest(ctx, colleagueID, workspaceID, key, nil); err == nil {
		t.Fatal("a colleague changed who a request is shared with")
	}

	// Taking the sharing away closes it again.
	if _, err = h.Commands.ShareServiceRequest(ctx, raiserID, workspaceID, key, nil); err != nil {
		t.Fatalf("stop sharing: %v", err)
	}
	callAs(colleagueID, http.MethodGet, requestPath, "", http.StatusNotFound)
	empty := callAs(adminID, http.MethodGet, "/rest/api/3/search/jql?fields=summary&jql="+url.QueryEscape(`Organizations IS EMPTY AND key = `+key), "", http.StatusOK)
	if !strings.Contains(empty, `"key":"`+key+`"`) {
		t.Fatalf("a request shared with nobody was not empty: %s", empty)
	}
}
