package api3

import (
	"encoding/json"
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/attachments"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

// TestServiceDeskUploadsAndOrganizations covers who may add temporary
// attachments to a service desk: its agents and the customers its portal
// admits, within the site's attachment settings and the desk's own switch. It also covers the desk
// organization list narrowed by accountId, and the 404s Jira answers for
// unknown desks, users and organizations.
func TestServiceDeskUploadsAndOrganizations(t *testing.T) {
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
	adminID, customerID, outsiderID := store.NewID("usr"), store.NewID("usr"), store.NewID("usr")
	stamp := time.Now().UnixNano() % 100000
	deskKey, otherKey := fmt.Sprintf("UP%05d", stamp), fmt.Sprintf("UO%05d", stamp)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Desk uploads')`, workspaceID)
	people := []struct{ id, role, name string }{{adminID, "admin", "Site admin"}, {customerID, "member", "Desk customer"}, {outsiderID, "member", "Other desk customer"}}
	for _, person := range people {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test',$3)`, person.id, person.id+"@example.test", person.name)
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,$3)`, workspaceID, person.id, person.role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, person.id, store.HashToken(person.id))
	}
	t.Cleanup(func() {
		exec(`DELETE FROM service_temporary_attachments WHERE workspace_id=$1`, workspaceID)
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
	upload := func(accountID, serviceDeskID, content string, want int) {
		t.Helper()
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		part, err := writer.CreateFormFile("file", "trace.txt")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = part.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
		if err = writer.Close(); err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPost, "/rest/servicedeskapi/servicedesk/"+serviceDeskID+"/attachTemporaryFile", &body)
		request.SetBasicAuth(accountID+"@example.test", accountID)
		request.Header.Set("Content-Type", writer.FormDataContentType())
		request.Header.Set("X-Atlassian-Token", "no-check")
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("upload to %s as %s: got %d want %d: %s", serviceDeskID, accountID, response.Code, want, response.Body.String())
		}
	}
	deskIDs := map[string]string{}
	requestTypeIDs := map[string]string{}
	for _, key := range []string{deskKey, otherKey} {
		callAs(adminID, http.MethodPost, "/rest/api/3/project", `{"key":"`+key+`","name":"Uploads `+key+`","projectTypeKey":"service_desk","projectTemplateKey":"com.atlassian.servicedesk:simplified-it-service-management","leadAccountId":"`+adminID+`"}`, http.StatusCreated)
		var deskID, requestTypeID string
		if err = st.Pool.QueryRow(ctx, `SELECT sd.id FROM service_desks sd JOIN projects p ON p.id=sd.project_id WHERE p.workspace_id=$1 AND p.key=$2`, workspaceID, key).Scan(&deskID); err != nil {
			t.Fatal(err)
		}
		if err = st.Pool.QueryRow(ctx, `SELECT id FROM service_request_types WHERE service_desk_id=$1 ORDER BY id::bigint LIMIT 1`, deskID).Scan(&requestTypeID); err != nil {
			t.Fatal(err)
		}
		deskIDs[key], requestTypeIDs[key] = deskID, requestTypeID
	}
	serviceDeskID, otherDeskID := deskIDs[deskKey], deskIDs[otherKey]
	// Each customer joins one desk by raising a request there; then the desk's
	// portal closes to everyone else.
	callAs(customerID, http.MethodPost, "/rest/servicedeskapi/request", `{"serviceDeskId":"`+serviceDeskID+`","requestTypeId":"`+requestTypeIDs[deskKey]+`","requestFieldValues":{"summary":"Desk request"}}`, http.StatusCreated)
	callAs(outsiderID, http.MethodPost, "/rest/servicedeskapi/request", `{"serviceDeskId":"`+otherDeskID+`","requestTypeId":"`+requestTypeIDs[otherKey]+`","requestFieldValues":{"summary":"Other request"}}`, http.StatusCreated)
	if err = h.Commands.SetServiceDeskCustomerAccess(ctx, adminID, workspaceID, serviceDeskID, false); err != nil {
		t.Fatal(err)
	}

	upload(customerID, serviceDeskID, "desk trace", http.StatusCreated)
	upload(adminID, serviceDeskID, "agent trace", http.StatusCreated)
	upload(outsiderID, serviceDeskID, "not admitted", http.StatusForbidden)
	upload(outsiderID, otherDeskID, "own desk trace", http.StatusCreated)
	upload(customerID, "999999999", "no desk", http.StatusNotFound)

	configure := func(enabled bool, limit int64) {
		t.Helper()
		if err := h.Commands.UpdateGlobalJiraConfiguration(ctx, workspaceID, adminID, models.JiraSiteConfiguration{AttachmentsEnabled: enabled, AttachmentUploadLimit: limit, IssueLinkingEnabled: true, SubTasksEnabled: true, TimeTrackingEnabled: true, UnassignedIssuesAllowed: true, VotingEnabled: true, WatchingEnabled: true}); err != nil {
			t.Fatal(err)
		}
	}
	configure(true, 16)
	upload(customerID, serviceDeskID, "short", http.StatusCreated)
	upload(customerID, serviceDeskID, "a trace longer than sixteen bytes", http.StatusBadRequest)
	configure(false, 32<<20)
	upload(adminID, serviceDeskID, "disabled", http.StatusForbidden)
	configure(true, 32<<20)

	// A service desk administrator can turn attachments off for one desk.
	if err = h.Commands.SetServiceDeskAttachmentsEnabled(ctx, customerID, workspaceID, serviceDeskID, false); err == nil {
		t.Fatal("a customer turned off the desk's attachments")
	}
	if err = h.Commands.SetServiceDeskAttachmentsEnabled(ctx, adminID, workspaceID, serviceDeskID, false); err != nil {
		t.Fatal(err)
	}
	upload(customerID, serviceDeskID, "desk switched off", http.StatusForbidden)
	upload(outsiderID, otherDeskID, "other desk still on", http.StatusCreated)
	if err = h.Commands.SetServiceDeskAttachmentsEnabled(ctx, adminID, workspaceID, serviceDeskID, true); err != nil {
		t.Fatal(err)
	}
	upload(customerID, serviceDeskID, "desk switched on", http.StatusCreated)

	// Desk organizations narrowed by accountId.
	organization, err := h.Commands.CreateServiceOrganization(ctx, adminID, workspaceID, "Uploads org "+deskKey)
	if err != nil {
		t.Fatal(err)
	}
	if err = h.Commands.SetServiceOrganizationUsers(ctx, adminID, workspaceID, organization.ID, []string{customerID}, true); err != nil {
		t.Fatal(err)
	}
	organizationsPath := "/rest/servicedeskapi/servicedesk/" + serviceDeskID + "/organization"
	callAs(adminID, http.MethodPost, organizationsPath, `{"organizationId":`+organization.ID+`}`, http.StatusNoContent)
	if listed := callAs(adminID, http.MethodGet, organizationsPath+"?accountId="+customerID, "", http.StatusOK); !strings.Contains(listed, organization.Name) {
		t.Fatalf("the member's organizations = %s", listed)
	}
	if listed := callAs(adminID, http.MethodGet, organizationsPath+"?accountId="+outsiderID, "", http.StatusOK); strings.Contains(listed, organization.Name) {
		t.Fatalf("a non-member's organizations = %s", listed)
	}
	callAs(adminID, http.MethodGet, organizationsPath+"?accountId=nobody-"+deskKey, "", http.StatusNotFound)
	callAs(adminID, http.MethodGet, "/rest/servicedeskapi/servicedesk/999999999/organization", "", http.StatusNotFound)
	callAs(adminID, http.MethodPost, organizationsPath, `{"organizationId":999999999}`, http.StatusNotFound)
	callAs(customerID, http.MethodGet, organizationsPath, "", http.StatusForbidden)

	// Organizations carry Jira's uuid and creation date, and their users are
	// UserDTOs: links instead of platform-only fields.
	if single := callAs(customerID, http.MethodGet, "/rest/servicedeskapi/organization/"+organization.ID, "", http.StatusOK); !strings.Contains(single, `"uuid":"`) || !strings.Contains(single, `"created":{"epochMillis":`) {
		t.Fatalf("organization bean = %s", single)
	}
	users := callAs(adminID, http.MethodGet, "/rest/servicedeskapi/organization/"+organization.ID+"/user", "", http.StatusOK)
	if !strings.Contains(users, `"self":"https://zzira.test/rest/api/3/user?accountId=`) || strings.Contains(users, `"accountType"`) || !strings.Contains(users, `"avatarUrls":{`) {
		t.Fatalf("organization users = %s", users)
	}

	// Service desks are listed and returned only to those who can access them.
	outsiderDesks := callAs(outsiderID, http.MethodGet, "/rest/servicedeskapi/servicedesk", "", http.StatusOK)
	if !strings.Contains(outsiderDesks, `"projectKey":"`+otherKey+`"`) || strings.Contains(outsiderDesks, `"projectKey":"`+deskKey+`"`) {
		t.Fatalf("service desks the other desk's customer can access = %s", outsiderDesks)
	}
	callAs(outsiderID, http.MethodGet, "/rest/servicedeskapi/servicedesk/"+serviceDeskID, "", http.StatusForbidden)
	callAs(customerID, http.MethodGet, "/rest/servicedeskapi/servicedesk/"+serviceDeskID, "", http.StatusOK)
	callAs(adminID, http.MethodGet, "/rest/servicedeskapi/servicedesk/999999999", "", http.StatusNotFound)

	// Validation results follow Jira's RequestValidationResultDTO.
	validatePath := "/rest/servicedeskapi/request/validate"
	if result := callAs(customerID, http.MethodPost, validatePath, `{"serviceDeskId":"`+serviceDeskID+`","requestTypeId":"`+requestTypeIDs[deskKey]+`","requestFieldValues":{"summary":"Valid"}}`, http.StatusOK); !strings.Contains(result, `"valid":true`) || !strings.Contains(result, `"errorMessage":null`) || !strings.Contains(result, `"fieldErrors":[]`) {
		t.Fatalf("valid payload = %s", result)
	}

	// Invitations and request notifications are emailed.
	outbox := func(recipient, subjectPart string) {
		t.Helper()
		var queued bool
		if err := st.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM email_outbox WHERE workspace_id=$1 AND recipient=$2 AND strpos(subject,$3)>0)`, workspaceID, recipient, subjectPart).Scan(&queued); err != nil || !queued {
			t.Fatalf("no email to %s about %q (err=%v)", recipient, subjectPart, err)
		}
	}
	invitee := "invitee-" + strings.ToLower(deskKey) + "@example.test"
	t.Cleanup(func() { exec(`DELETE FROM users WHERE email=$1`, invitee) })
	callAs(adminID, http.MethodPost, "/rest/servicedeskapi/servicedesk/"+serviceDeskID+"/customer/invite", `{"email":"`+invitee+`","displayName":"Invitee"}`, http.StatusCreated)
	outbox(invitee, "invited")
	var requestKey string
	if err = st.Pool.QueryRow(ctx, `SELECT i.key FROM service_requests sr JOIN issues i ON i.id=sr.issue_id WHERE sr.workspace_id=$1 AND sr.customer_id=$2`, workspaceID, customerID).Scan(&requestKey); err != nil {
		t.Fatal(err)
	}
	callAs(customerID, http.MethodPost, "/rest/servicedeskapi/request/"+requestKey+"/participant", `{"accountIds":["`+outsiderID+`"]}`, http.StatusOK)
	outbox(outsiderID+"@example.test", "added you as a participant on "+requestKey)
	callAs(adminID, http.MethodPost, "/rest/servicedeskapi/request/"+requestKey+"/comment", `{"body":"We are looking into it.","public":true}`, http.StatusCreated)
	outbox(customerID+"@example.test", "commented on "+requestKey)

	// Customer satisfaction can be turned off for a desk.
	feedbackPath := "/rest/servicedeskapi/request/" + requestKey + "/feedback"
	if err = h.Commands.SetServiceDeskFeedbackEnabled(ctx, customerID, workspaceID, serviceDeskID, false); err == nil {
		t.Fatal("a customer turned off the desk's satisfaction feedback")
	}
	if err = h.Commands.SetServiceDeskFeedbackEnabled(ctx, adminID, workspaceID, serviceDeskID, false); err != nil {
		t.Fatal(err)
	}
	if refused := callAs(customerID, http.MethodPost, feedbackPath, `{"type":"csat","rating":4}`, http.StatusBadRequest); !strings.Contains(refused, "turned off") {
		t.Fatalf("feedback on a desk with satisfaction turned off = %s", refused)
	}
	if err = h.Commands.SetServiceDeskFeedbackEnabled(ctx, adminID, workspaceID, serviceDeskID, true); err != nil {
		t.Fatal(err)
	}
	// Turned back on, only the request's completion stands in the way.
	if refused := callAs(customerID, http.MethodPost, feedbackPath, `{"type":"csat","rating":4}`, http.StatusBadRequest); strings.Contains(refused, "turned off") || !strings.Contains(refused, "completed request") {
		t.Fatalf("feedback on an open request = %s", refused)
	}

	// Instance information needs no credentials and dates its build.
	infoResponse := httptest.NewRecorder()
	h.ServeHTTP(infoResponse, httptest.NewRequest(http.MethodGet, "/rest/servicedeskapi/info", nil))
	if infoResponse.Code != http.StatusOK || !strings.Contains(infoResponse.Body.String(), `"buildDate":{"epochMillis":`) {
		t.Fatalf("anonymous info = %d %s", infoResponse.Code, infoResponse.Body.String())
	}

	// Agents and service desk administrators create organizations; deleting one
	// needs the Jira administrator permission.
	if err = h.Commands.SetServiceDeskAgent(ctx, adminID, workspaceID, otherDeskID, outsiderID, true); err != nil {
		t.Fatal(err)
	}
	created := callAs(outsiderID, http.MethodPost, "/rest/servicedeskapi/organization", `{"name":"Agent org `+deskKey+`"}`, http.StatusCreated)
	var agentOrganization struct {
		ID string `json:"id"`
	}
	if err = json.Unmarshal([]byte(created), &agentOrganization); err != nil {
		t.Fatal(err)
	}
	callAs(customerID, http.MethodPost, "/rest/servicedeskapi/organization", `{"name":"Customer org `+deskKey+`"}`, http.StatusForbidden)
	callAs(outsiderID, http.MethodDelete, "/rest/servicedeskapi/organization/"+agentOrganization.ID, "", http.StatusForbidden)
	callAs(adminID, http.MethodDelete, "/rest/servicedeskapi/organization/"+agentOrganization.ID, "", http.StatusNoContent)

	// Revoking portal-only access names an invalid account with 404.
	callAs(adminID, http.MethodPut, "/rest/servicedeskapi/customer/user/nobody-"+deskKey+"/revoke-portal-only-access", "", http.StatusNotFound)
}
