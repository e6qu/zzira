package api3

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/attachments"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

func TestServiceProjectAndRequestTypeContract(t *testing.T) {
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
	workspaceID, actorID, customerID, agentID := store.NewID("ws"), store.NewID("usr"), store.NewID("usr"), store.NewID("usr")
	customFieldID := "customfield_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Service test')`, workspaceID)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Service admin')`, actorID, actorID+"@example.test")
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Portal customer')`, customerID, customerID+"@example.test")
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Service agent')`, agentID, agentID+"@example.test")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'admin')`, workspaceID, actorID)
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'member')`, workspaceID, customerID)
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'member')`, workspaceID, agentID)
	exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, actorID, store.HashToken(actorID))
	exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, customerID, store.HashToken(customerID))
	exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, agentID, store.HashToken(agentID))
	t.Cleanup(func() {
		exec(`DELETE FROM notifications WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM boards WHERE project_id IN (SELECT id FROM projects WHERE workspace_id=$1)`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM custom_fields WHERE id=$1`, customFieldID)
		exec(`DELETE FROM organization_audit_events WHERE actor_id=$1`, actorID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM wiki_page_versions WHERE page_id IN (SELECT p.id FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id WHERE s.workspace_id=$1)`, workspaceID)
		exec(`DELETE FROM wiki_pages WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`, workspaceID)
		exec(`DELETE FROM wiki_spaces WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		exec(`DELETE FROM api_tokens WHERE user_id IN ($1,$2,$3)`, actorID, customerID, agentID)
		exec(`DELETE FROM users WHERE id IN ($1,$2,$3) OR email IN ('invited.customer@example.test','agent-created.customer@example.test','lifecycle.customer@example.test','desk.invite@example.test')`, actorID, customerID, agentID)
	})
	blobs, err := attachments.NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	handler := &Handler{Store: st, Commands: &commands.Service{Store: st, Blobs: blobs}, Blobs: blobs, WorkspaceSlug: workspaceID, BaseURL: "https://zzira.test"}
	callAs := func(accountID, method, path, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.SetBasicAuth(accountID+"@example.test", accountID)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s: %d want %d: %s", method, path, response.Code, want, response.Body.String())
		}
		return response
	}
	call := func(method, path, body string, want int) *httptest.ResponseRecorder {
		return callAs(actorID, method, path, body, want)
	}
	callMultipartAs := func(accountID, path, filename, content string, want int) *httptest.ResponseRecorder {
		t.Helper()
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		part, err := writer.CreateFormFile("file", filename)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest("POST", path, &body)
		request.SetBasicAuth(accountID+"@example.test", accountID)
		request.Header.Set("Content-Type", writer.FormDataContentType())
		request.Header.Set("X-Atlassian-Token", "no-check")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("POST %s: %d want %d: %s", path, response.Code, want, response.Body.String())
		}
		return response
	}
	created := call("POST", "/rest/api/3/project", `{"key":"HELP","name":"Help Center","projectTypeKey":"service_desk","projectTemplateKey":"com.atlassian.servicedesk:simplified-it-service-management","leadAccountId":"`+actorID+`"}`, 201)
	if !strings.Contains(created.Body.String(), `"key":"HELP"`) {
		t.Fatal(created.Body.String())
	}
	project, err := st.ProjectByKey(ctx, workspaceID, "HELP")
	if err != nil || project.ProjectTypeKey != "service_desk" {
		t.Fatalf("service project = %+v, %v", project, err)
	}
	desks := call("GET", "/rest/servicedeskapi/servicedesk", "", 200)
	if !strings.Contains(desks.Body.String(), `"projectKey":"HELP"`) || !strings.Contains(desks.Body.String(), `"projectTypeKey":"service_desk"`) {
		t.Fatal(desks.Body.String())
	}
	var serviceDeskID string
	if err := st.Pool.QueryRow(ctx, `SELECT id FROM service_desks WHERE project_id=$1`, project.ID).Scan(&serviceDeskID); err != nil {
		t.Fatal(err)
	}
	call("GET", "/rest/servicedeskapi/servicedesk/"+serviceDeskID, "", 200)
	requestTypes := call("GET", "/rest/servicedeskapi/servicedesk/"+serviceDeskID+"/requesttype", "", 200)
	if !strings.Contains(requestTypes.Body.String(), `"name":"Get IT help"`) || !strings.Contains(requestTypes.Body.String(), `"name":"Report an incident"`) || !strings.Contains(requestTypes.Body.String(), `"name":"Investigate a problem"`) || !strings.Contains(requestTypes.Body.String(), `"name":"Request a change"`) {
		t.Fatal(requestTypes.Body.String())
	}
	var requestTypeID string
	if err := st.Pool.QueryRow(ctx, `SELECT id FROM service_request_types WHERE service_desk_id=$1 ORDER BY id::bigint LIMIT 1`, serviceDeskID).Scan(&requestTypeID); err != nil {
		t.Fatal(err)
	}
	fields := call("GET", "/rest/servicedeskapi/servicedesk/"+serviceDeskID+"/requesttype/"+requestTypeID+"/field", "", 200)
	if !strings.Contains(fields.Body.String(), `"fieldId":"summary"`) {
		t.Fatal(fields.Body.String())
	}
	createdType := call("POST", "/rest/servicedeskapi/servicedesk/"+serviceDeskID+"/requesttype", `{"name":"Ask a question","description":"General help","helpText":"What do you need?","issueTypeId":"it_task"}`, 200)
	if !strings.Contains(createdType.Body.String(), `"name":"Ask a question"`) {
		t.Fatal(createdType.Body.String())
	}
	var createdTypeID string
	if err := st.Pool.QueryRow(ctx, `SELECT id FROM service_request_types WHERE service_desk_id=$1 AND name='Ask a question'`, serviceDeskID).Scan(&createdTypeID); err != nil {
		t.Fatal(err)
	}
	call("DELETE", "/rest/servicedeskapi/servicedesk/"+serviceDeskID+"/requesttype/"+createdTypeID, "", 204)
	call("GET", "/rest/servicedeskapi/servicedesk/"+serviceDeskID+"/requesttype/"+createdTypeID, "", 404)
	call("GET", "/rest/servicedeskapi/requesttype?searchQuery=incident", "", 200)
	call("GET", "/rest/servicedeskapi/info", "", 200)
	assets := call("GET", "/rest/servicedeskapi/assets/workspace", "", 200)
	if !strings.Contains(assets.Body.String(), `"workspaceId"`) {
		t.Fatal(assets.Body.String())
	}
	deprecatedAssets := call("GET", "/rest/servicedeskapi/insight/workspace", "", 200)
	if !strings.Contains(deprecatedAssets.Body.String(), `"workspaceId"`) {
		t.Fatal(deprecatedAssets.Body.String())
	}
	groups := callAs(customerID, "GET", "/rest/servicedeskapi/servicedesk/"+serviceDeskID+"/requesttypegroup", "", 200)
	if !strings.Contains(groups.Body.String(), `"name":"Help and support"`) || !strings.Contains(groups.Body.String(), `"name":"Incidents"`) || !strings.Contains(groups.Body.String(), `"name":"Problems"`) || !strings.Contains(groups.Body.String(), `"name":"Changes"`) {
		t.Fatal(groups.Body.String())
	}
	call("GET", "/rest/servicedeskapi/servicedesk/missing/requesttypegroup", "", 404)

	permissionsBody := `{"permissions":["canCreateRequest","canAdminister"],"requestTypeIds":[` + requestTypeID + `,999999999]}`
	permissions := call("POST", "/rest/servicedeskapi/servicedesk/"+serviceDeskID+"/requesttype/permissions/check", permissionsBody, 200)
	if !strings.Contains(permissions.Body.String(), `"canCreateRequest":[`+requestTypeID+`]`) || !strings.Contains(permissions.Body.String(), `"canAdminister":[`+requestTypeID+`]`) {
		t.Fatal(permissions.Body.String())
	}
	targetPermissions := call("POST", "/rest/servicedeskapi/servicedesk/"+serviceDeskID+"/requesttype/permissions/check", `{"accountId":"`+customerID+`","permissions":["canCreateRequest","canAdminister"],"requestTypeIds":[`+requestTypeID+`]}`, 200)
	if !strings.Contains(targetPermissions.Body.String(), `"canCreateRequest":[`+requestTypeID+`]`) || !strings.Contains(targetPermissions.Body.String(), `"canAdminister":[]`) {
		t.Fatal(targetPermissions.Body.String())
	}
	callAs(customerID, "POST", "/rest/servicedeskapi/servicedesk/"+serviceDeskID+"/requesttype/permissions/check", `{"accountId":"`+actorID+`","permissions":["canCreateRequest"],"requestTypeIds":[`+requestTypeID+`]}`, 401)
	call("POST", "/rest/servicedeskapi/servicedesk/"+serviceDeskID+"/requesttype/permissions/check", `{"permissions":[],"requestTypeIds":[]}`, 400)

	propertyPath := "/rest/servicedeskapi/servicedesk/" + serviceDeskID + "/requesttype/" + requestTypeID + "/property/automation"
	call("PUT", propertyPath, `{"owner":"service-automation","enabled":true}`, 201)
	call("PUT", propertyPath, `{"owner":"service-automation","enabled":false}`, 200)
	requestTypeProperty := callAs(customerID, "GET", propertyPath, "", 200)
	if !strings.Contains(requestTypeProperty.Body.String(), `"key":"automation"`) || !strings.Contains(requestTypeProperty.Body.String(), `"enabled":false`) {
		t.Fatal(requestTypeProperty.Body.String())
	}
	propertyKeys := callAs(customerID, "GET", "/rest/servicedeskapi/servicedesk/"+serviceDeskID+"/requesttype/"+requestTypeID+"/property", "", 200)
	if !strings.Contains(propertyKeys.Body.String(), `"key":"automation"`) {
		t.Fatal(propertyKeys.Body.String())
	}
	callAs(customerID, "PUT", propertyPath, `{"enabled":true}`, 403)
	call("DELETE", propertyPath, "", 204)
	callAs(customerID, "GET", propertyPath, "", 404)

	space, err := handler.Commands.CreateWikiSpace(ctx, workspaceID, actorID, "HELPKB", "Support knowledge", "Customer self service", false)
	if err != nil {
		t.Fatal(err)
	}
	page, err := handler.Commands.SaveWikiPage(ctx, workspaceID, actorID, models.WikiPage{
		SpaceID: space.ID,
		Title:   "Restart checkout worker",
		Status:  "current",
		Body:    models.WikiBody{Representation: "storage", Value: "<p>Restart the checkout worker safely.</p>"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.Commands.SetServiceDeskKnowledgeSpace(ctx, actorID, workspaceID, serviceDeskID, space.ID, true); err != nil {
		t.Fatal(err)
	}
	call("GET", "/rest/servicedeskapi/knowledgebase/article", "", 400)
	articles := callAs(customerID, "GET", "/rest/servicedeskapi/knowledgebase/article?query=checkout&highlight=true", "", 200)
	if !strings.Contains(articles.Body.String(), `@@@hl@@@checkout@@@endhl@@@`) || !strings.Contains(articles.Body.String(), `"pageId":"`+page.ID+`"`) || !strings.Contains(articles.Body.String(), `"spaceKey":"HELPKB"`) {
		t.Fatal(articles.Body.String())
	}
	deskArticles := callAs(customerID, "GET", "/rest/servicedeskapi/servicedesk/"+serviceDeskID+"/knowledgebase/article?query=worker", "", 200)
	if !strings.Contains(deskArticles.Body.String(), "Restart checkout worker") {
		t.Fatal(deskArticles.Body.String())
	}
	article := callAs(customerID, "GET", "/rest/servicedeskapi/knowledgebase/article/view/"+page.ID, "", 200)
	if !strings.Contains(article.Body.String(), "Restart the checkout worker safely.") {
		t.Fatal(article.Body.String())
	}

	createdCustomer := call("POST", "/rest/servicedeskapi/customer", `{"email":"invited.customer@example.test","displayName":"Invited Customer"}`, 201)
	if !strings.Contains(createdCustomer.Body.String(), `"displayName":"Invited Customer"`) {
		t.Fatal(createdCustomer.Body.String())
	}
	var createdCustomerBean map[string]any
	if err := json.Unmarshal(createdCustomer.Body.Bytes(), &createdCustomerBean); err != nil {
		t.Fatal(err)
	}
	invitedCustomerID, _ := createdCustomerBean["accountId"].(string)
	call("POST", "/rest/servicedeskapi/customer?strictConflictStatusCode=true", `{"email":"invited.customer@example.test","displayName":"Invited Customer"}`, 409)
	callAs(agentID, "POST", "/rest/servicedeskapi/customer", `{"email":"forbidden.customer@example.test","displayName":"Forbidden Customer"}`, 403)
	lifecycleCustomer := call("POST", "/rest/servicedeskapi/customer/skip-permission-check", `{"email":"lifecycle.customer@example.test","displayName":"Lifecycle Customer"}`, 201)
	var lifecycleCustomerBean map[string]any
	if err := json.Unmarshal(lifecycleCustomer.Body.Bytes(), &lifecycleCustomerBean); err != nil {
		t.Fatal(err)
	}
	lifecycleCustomerID, _ := lifecycleCustomerBean["accountId"].(string)
	if lifecycleCustomerID == "" {
		t.Fatal(lifecycleCustomer.Body.String())
	}
	invitedToDesk := call("POST", "/rest/servicedeskapi/servicedesk/"+serviceDeskID+"/customer/invite", `{"email":"desk.invite@example.test","displayName":"Desk Invite"}`, 201)
	if !strings.Contains(invitedToDesk.Body.String(), `"displayName":"Desk Invite"`) {
		t.Fatal(invitedToDesk.Body.String())
	}
	deskCustomers := call("GET", "/rest/servicedeskapi/servicedesk/"+serviceDeskID+"/customer?query=Desk", "", 200)
	if !strings.Contains(deskCustomers.Body.String(), `"displayName":"Desk Invite"`) {
		t.Fatal(deskCustomers.Body.String())
	}
	call("DELETE", "/rest/servicedeskapi/servicedesk/"+serviceDeskID+"/customer", `{"accountIds":["`+lifecycleCustomerID+`"]}`, 400)
	if err := handler.Commands.SetServiceDeskCustomerAccess(ctx, actorID, workspaceID, serviceDeskID, false); err != nil {
		t.Fatal(err)
	}
	call("POST", "/rest/servicedeskapi/servicedesk/"+serviceDeskID+"/customer", `{"accountIds":["`+lifecycleCustomerID+`"]}`, 204)
	call("DELETE", "/rest/servicedeskapi/servicedesk/"+serviceDeskID+"/customer", `{"accountIds":["`+lifecycleCustomerID+`"]}`, 204)
	call("POST", "/rest/servicedeskapi/request", `{"serviceDeskId":"`+serviceDeskID+`","requestTypeId":"`+requestTypeID+`","raiseOnBehalfOf":"lifecycle.customer@example.test","requestFieldValues":{"summary":"Closed portal denial"}}`, 400)
	call("POST", "/rest/servicedeskapi/servicedesk/"+serviceDeskID+"/customer/skip-permission-check", `{"accountIds":["`+lifecycleCustomerID+`"]}`, 204)
	call("POST", "/rest/servicedeskapi/request", `{"serviceDeskId":"`+serviceDeskID+`","requestTypeId":"`+requestTypeID+`","raiseOnBehalfOf":"lifecycle.customer@example.test","requestFieldValues":{"summary":"Closed portal admission"}}`, 201)
	call("PUT", "/rest/servicedeskapi/customer/user/"+lifecycleCustomerID+"/revoke-portal-only-access", "", 204)
	var lifecycleActive, lifecycleRole bool
	if err := st.Pool.QueryRow(ctx, `SELECT active FROM service_customers WHERE workspace_id=$1 AND user_id=$2`, workspaceID, lifecycleCustomerID).Scan(&lifecycleActive); err != nil {
		t.Fatal(err)
	}
	if err := st.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM role_bindings rb JOIN sites si ON rb.scope_type='site' AND rb.scope_id=si.id::text WHERE si.workspace_id=$1 AND rb.principal_id=$2 AND rb.role_key='atlassian/customer')`, workspaceID, lifecycleCustomerID).Scan(&lifecycleRole); err != nil {
		t.Fatal(err)
	}
	if lifecycleActive || lifecycleRole {
		t.Fatalf("revoked lifecycle customer active=%v role=%v", lifecycleActive, lifecycleRole)
	}
	call("POST", "/rest/servicedeskapi/request", `{"serviceDeskId":"`+serviceDeskID+`","requestTypeId":"`+requestTypeID+`","raiseOnBehalfOf":"lifecycle.customer@example.test","requestFieldValues":{"summary":"Revoked portal denial"}}`, 400)
	if err := handler.Commands.SetServiceDeskCustomerAccess(ctx, actorID, workspaceID, serviceDeskID, true); err != nil {
		t.Fatal(err)
	}
	onBehalf := call("POST", "/rest/servicedeskapi/request", `{"serviceDeskId":"`+serviceDeskID+`","requestTypeId":"`+requestTypeID+`","raiseOnBehalfOf":"invited.customer@example.test","requestFieldValues":{"summary":"New starter access"}}`, 201)
	var onBehalfBean map[string]any
	if err := json.Unmarshal(onBehalf.Body.Bytes(), &onBehalfBean); err != nil {
		t.Fatal(err)
	}
	onBehalfIssue, err := st.IssueByIDOrKey(ctx, workspaceID, onBehalfBean["issueKey"].(string))
	if err != nil || onBehalfIssue.Reporter == nil || onBehalfIssue.Reporter.ID != invitedCustomerID {
		t.Fatalf("on-behalf issue = %+v, %v", onBehalfIssue, err)
	}
	var onBehalfActor string
	if err := st.Pool.QueryRow(ctx, `SELECT actor_id FROM actions WHERE workspace_id=$1 AND entity_type='issue' AND entity_id=$2 AND op='upsert' ORDER BY seq LIMIT 1`, workspaceID, onBehalfIssue.ID).Scan(&onBehalfActor); err != nil || onBehalfActor != actorID {
		t.Fatalf("on-behalf actor = %q, %v", onBehalfActor, err)
	}
	invalid := callAs(customerID, "POST", "/rest/servicedeskapi/request/validate", `{"serviceDeskId":"`+serviceDeskID+`","requestTypeId":"`+requestTypeID+`","requestFieldValues":{}}`, 200)
	if !strings.Contains(invalid.Body.String(), `"valid":false`) || !strings.Contains(invalid.Body.String(), `"summary"`) {
		t.Fatal(invalid.Body.String())
	}
	var incidentTypeID string
	if err := st.Pool.QueryRow(ctx, `SELECT id FROM service_request_types WHERE service_desk_id=$1 AND name='Report an incident'`, serviceDeskID).Scan(&incidentTypeID); err != nil {
		t.Fatal(err)
	}
	createdRequest := callAs(customerID, "POST", "/rest/servicedeskapi/request", `{"serviceDeskId":"`+serviceDeskID+`","requestTypeId":"`+incidentTypeID+`","requestFieldValues":{"summary":"Production checkout is unavailable","description":"Customers receive an error."}}`, 201)
	var requestBean map[string]any
	if err := json.Unmarshal(createdRequest.Body.Bytes(), &requestBean); err != nil {
		t.Fatal(err)
	}
	issueKey, _ := requestBean["issueKey"].(string)
	if issueKey == "" {
		t.Fatal(createdRequest.Body.String())
	}
	issue, err := st.IssueByIDOrKey(ctx, workspaceID, issueKey)
	if err != nil || !strings.Contains(strings.Join(issue.Labels, ","), "incident") {
		t.Fatalf("incident issue = %+v, %v", issue, err)
	}
	if err := handler.Commands.CreateServiceEscalationStep(ctx, actorID, workspaceID, serviceDeskID, actorID, 5); err != nil {
		t.Fatalf("create incident escalation step: %v", err)
	}
	if err := handler.Commands.CreateServiceEscalationStep(ctx, customerID, workspaceID, serviceDeskID, customerID, 10); err == nil {
		t.Fatal("customer configured an incident escalation step")
	}
	incidentProfile := models.ServiceOperationsProfile{Impact: 4, Likelihood: 3, ChangeType: "normal", MajorIncident: true, ReviewRequired: true, ReviewStatus: "pending"}
	if _, err := handler.Commands.UpdateServiceOperationsProfile(ctx, actorID, workspaceID, issue.ID, incidentProfile); err != nil {
		t.Fatalf("declare major incident: %v", err)
	}
	if _, err := handler.Commands.CreateServiceIncidentUpdate(ctx, actorID, workspaceID, issue.ID, "public", "Checkout is unavailable; responders are investigating."); err != nil {
		t.Fatalf("publish incident update: %v", err)
	}
	if _, err := handler.Commands.CreateServiceIncidentUpdate(ctx, actorID, workspaceID, issue.ID, "internal", "Database saturation is the leading hypothesis."); err != nil {
		t.Fatalf("publish internal incident update: %v", err)
	}
	agentUpdates, err := st.ServiceIncidentUpdates(ctx, workspaceID, actorID, issue.ID)
	if err != nil || len(agentUpdates) != 2 || agentUpdates[0].CreatedAt.Location() != time.UTC {
		t.Fatalf("agent incident updates = %+v, %v", agentUpdates, err)
	}
	customerUpdates, err := st.ServiceIncidentUpdates(ctx, workspaceID, customerID, issue.ID)
	if err != nil || len(customerUpdates) != 1 || customerUpdates[0].Audience != "public" {
		t.Fatalf("customer incident updates = %+v, %v", customerUpdates, err)
	}
	if _, err := handler.Commands.CreateServiceIncidentUpdate(ctx, customerID, workspaceID, issue.ID, "public", "Unauthorized update"); err == nil {
		t.Fatal("customer published a major incident update")
	}
	if _, err := handler.Commands.CreateServiceIncidentUpdate(ctx, actorID, workspaceID, issue.ID, "stakeholders", "Invalid audience"); err == nil {
		t.Fatal("invalid incident update audience succeeded")
	}
	var incidentUpdateAudits int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM organization_audit_events WHERE actor_id=$1 AND target_id=$2 AND action='service_major_incident_update_created'`, actorID, issue.ID).Scan(&incidentUpdateAudits); err != nil || incidentUpdateAudits != 2 {
		t.Fatalf("incident update audits = %d, %v", incidentUpdateAudits, err)
	}
	var customerIncidentNotifications int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE workspace_id=$1 AND user_id=$2 AND entity_id=$3 AND kind='service_incident_update'`, workspaceID, customerID, issue.Key).Scan(&customerIncidentNotifications); err != nil || customerIncidentNotifications != 1 {
		t.Fatalf("customer incident notifications = %d, %v", customerIncidentNotifications, err)
	}
	incidentEscalationNow := time.Now().UTC().Truncate(time.Second)
	if _, err := st.Pool.Exec(ctx, `UPDATE service_request_operations SET major_incident_declared_at=$2 WHERE request_issue_id=$1`, issue.ID, incidentEscalationNow.Add(-6*time.Minute)); err != nil {
		t.Fatal(err)
	}
	incidentRunner := &store.ServiceIncidentEscalationRunner{Store: st, Now: func() time.Time { return incidentEscalationNow }}
	if err := incidentRunner.DrainOnce(ctx, workspaceID); err != nil {
		t.Fatal(err)
	}
	if err := incidentRunner.DrainOnce(ctx, workspaceID); err != nil {
		t.Fatal(err)
	}
	var incidentEscalationNotifications int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE workspace_id=$1 AND user_id=$2 AND entity_id=$3 AND kind='service_incident_escalation'`, workspaceID, actorID, issue.Key).Scan(&incidentEscalationNotifications); err != nil || incidentEscalationNotifications != 1 {
		t.Fatalf("incident escalation notifications = %d, %v", incidentEscalationNotifications, err)
	}
	escalationStatus, err := st.ServiceIncidentEscalationStatus(ctx, workspaceID, actorID, issue.ID)
	if err != nil || len(escalationStatus) != 1 || escalationStatus[0].TriggeredAt == nil || escalationStatus[0].TriggeredAt.Location() != time.UTC {
		t.Fatalf("incident escalation status = %+v, %v", escalationStatus, err)
	}
	if _, err := st.ServiceIncidentEscalationStatus(ctx, workspaceID, customerID, issue.ID); err == nil {
		t.Fatal("customer read internal incident escalation status")
	}
	incidentProfile.MajorIncident = false
	if _, err := handler.Commands.UpdateServiceOperationsProfile(ctx, actorID, workspaceID, issue.ID, incidentProfile); err != nil {
		t.Fatalf("declassify major incident: %v", err)
	}
	archivedUpdates, err := st.ServiceIncidentUpdates(ctx, workspaceID, customerID, issue.ID)
	if err != nil || len(archivedUpdates) != 1 {
		t.Fatalf("archived public incident updates = %+v, %v", archivedUpdates, err)
	}
	if _, err := handler.Commands.CreateServiceIncidentUpdate(ctx, actorID, workspaceID, issue.ID, "public", "Published after declassification"); err == nil {
		t.Fatal("published an update after major incident declassification")
	}
	incidentProfile.MajorIncident = true
	if _, err := handler.Commands.UpdateServiceOperationsProfile(ctx, actorID, workspaceID, issue.ID, incidentProfile); err != nil {
		t.Fatalf("redeclare major incident: %v", err)
	}
	redeclaredProfile, err := st.ServiceOperationsProfile(ctx, workspaceID, issue.ID)
	if err != nil || redeclaredProfile.MajorIncidentGeneration != 2 || redeclaredProfile.MajorIncidentDeclaredAt == nil || redeclaredProfile.MajorIncidentDeclaredAt.Location() != time.UTC {
		t.Fatalf("redeclared incident profile = %+v, %v", redeclaredProfile, err)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE service_request_operations SET major_incident_declared_at=$2 WHERE request_issue_id=$1`, issue.ID, incidentEscalationNow.Add(-6*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := incidentRunner.DrainOnce(ctx, workspaceID); err != nil {
		t.Fatal(err)
	}
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE workspace_id=$1 AND user_id=$2 AND entity_id=$3 AND kind='service_incident_escalation'`, workspaceID, actorID, issue.Key).Scan(&incidentEscalationNotifications); err != nil || incidentEscalationNotifications != 2 {
		t.Fatalf("redeclared incident escalation notifications = %d, %v", incidentEscalationNotifications, err)
	}
	var problemTypeID, changeTypeID string
	if err := st.Pool.QueryRow(ctx, `SELECT id FROM service_request_types WHERE service_desk_id=$1 AND name='Investigate a problem'`, serviceDeskID).Scan(&problemTypeID); err != nil {
		t.Fatal(err)
	}
	if err := st.Pool.QueryRow(ctx, `SELECT id FROM service_request_types WHERE service_desk_id=$1 AND name='Request a change'`, serviceDeskID).Scan(&changeTypeID); err != nil {
		t.Fatal(err)
	}
	callAs(customerID, "POST", "/rest/servicedeskapi/request", `{"serviceDeskId":"`+serviceDeskID+`","requestTypeId":"`+changeTypeID+`","requestFieldValues":{"summary":"Incomplete change"}}`, 400)
	problemResponse := callAs(customerID, "POST", "/rest/servicedeskapi/request", `{"serviceDeskId":"`+serviceDeskID+`","requestTypeId":"`+problemTypeID+`","requestFieldValues":{"summary":"Recurring checkout timeout","description":"Investigate incidents caused by checkout worker exhaustion."}}`, 201)
	changeResponse := callAs(customerID, "POST", "/rest/servicedeskapi/request", `{"serviceDeskId":"`+serviceDeskID+`","requestTypeId":"`+changeTypeID+`","requestFieldValues":{"summary":"Increase checkout worker capacity","description":"Scale workers after approval and roll back if latency rises."}}`, 201)
	var problemBean, changeBean map[string]any
	if err := json.Unmarshal(problemResponse.Body.Bytes(), &problemBean); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(changeResponse.Body.Bytes(), &changeBean); err != nil {
		t.Fatal(err)
	}
	problemIssue, err := st.IssueByIDOrKey(ctx, workspaceID, problemBean["issueKey"].(string))
	if err != nil || !slices.Equal(problemIssue.Labels, []string{"problem"}) {
		t.Fatalf("problem issue = %+v, %v", problemIssue, err)
	}
	changeIssue, err := st.IssueByIDOrKey(ctx, workspaceID, changeBean["issueKey"].(string))
	if err != nil || !slices.Equal(changeIssue.Labels, []string{"change"}) {
		t.Fatalf("change issue = %+v, %v", changeIssue, err)
	}
	overlapResponse := callAs(customerID, "POST", "/rest/servicedeskapi/request", `{"serviceDeskId":"`+serviceDeskID+`","requestTypeId":"`+changeTypeID+`","requestFieldValues":{"summary":"Rotate checkout credentials","description":"Rotate credentials during the planned capacity change."}}`, 201)
	var overlapBean map[string]any
	if err := json.Unmarshal(overlapResponse.Body.Bytes(), &overlapBean); err != nil {
		t.Fatal(err)
	}
	overlapIssue, err := st.IssueByIDOrKey(ctx, workspaceID, overlapBean["issueKey"].(string))
	if err != nil {
		t.Fatal(err)
	}
	windowStart := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	windowEnd := windowStart.Add(2 * time.Hour)
	overlapStart := windowStart.Add(time.Hour)
	overlapEnd := windowEnd.Add(time.Hour)
	profile := models.ServiceOperationsProfile{Impact: 3, Likelihood: 3, ChangeType: "normal", PlannedStart: &windowStart, PlannedEnd: &windowEnd, RollbackPlan: "Restore the prior worker capacity.", ReviewStatus: "not_required"}
	if err := st.UpdateServiceOperationsProfile(ctx, workspaceID, actorID, changeIssue.ID, profile); err != nil {
		t.Fatalf("plan first change: %v", err)
	}
	storedProfile, err := st.ServiceOperationsProfile(ctx, workspaceID, changeIssue.ID)
	if err != nil || storedProfile.PlannedStart == nil || storedProfile.PlannedEnd == nil || storedProfile.PlannedStart.Location() != time.UTC || !storedProfile.PlannedStart.Equal(windowStart) || !storedProfile.PlannedEnd.Equal(windowEnd) {
		t.Fatalf("UTC change profile = %+v, %v", storedProfile, err)
	}
	profile.Impact, profile.Likelihood = 2, 2
	profile.PlannedStart, profile.PlannedEnd = &overlapStart, &overlapEnd
	profile.RollbackPlan = "Restore the previous credentials."
	if err := st.UpdateServiceOperationsProfile(ctx, workspaceID, actorID, overlapIssue.ID, profile); err != nil {
		t.Fatalf("plan overlapping change: %v", err)
	}
	changeCalendar, err := st.ServiceChangeCalendar(ctx, workspaceID, actorID, serviceDeskID, windowStart.Add(-time.Hour), overlapEnd.Add(time.Hour))
	if err != nil || len(changeCalendar) != 2 || changeCalendar[0].ConflictCount != 1 || changeCalendar[1].ConflictCount != 1 || changeCalendar[0].RiskLevel != "High" || changeCalendar[1].RiskLevel != "Medium" || changeCalendar[0].PlannedStart.Location() != time.UTC {
		t.Fatalf("change calendar = %+v, %v", changeCalendar, err)
	}
	conflicts, err := st.ServiceChangeConflicts(ctx, workspaceID, actorID, changeIssue.ID)
	if err != nil || len(conflicts) != 1 || conflicts[0].IssueID != overlapIssue.ID {
		t.Fatalf("change conflicts = %+v, %v", conflicts, err)
	}
	if _, err := st.ServiceChangeCalendar(ctx, workspaceID, customerID, serviceDeskID, windowStart, overlapEnd); !errors.Is(err, store.ErrProjectPermission) {
		t.Fatalf("customer calendar access error = %v, want project permission", err)
	}
	if _, err := st.ServiceChangeConflicts(ctx, workspaceID, customerID, changeIssue.ID); !errors.Is(err, store.ErrProjectPermission) {
		t.Fatalf("customer conflict access error = %v, want project permission", err)
	}
	exec(`UPDATE issues SET status_id='st_done' WHERE id=$1`, overlapIssue.ID)
	changeCalendar, err = st.ServiceChangeCalendar(ctx, workspaceID, actorID, serviceDeskID, windowStart.Add(-time.Hour), overlapEnd.Add(time.Hour))
	if err != nil || len(changeCalendar) != 1 || changeCalendar[0].IssueID != changeIssue.ID || changeCalendar[0].ConflictCount != 0 {
		t.Fatalf("calendar after completed change = %+v, %v", changeCalendar, err)
	}
	exec(`DELETE FROM issues WHERE id=$1`, overlapIssue.ID)
	linkTypes, err := st.LinkTypes(ctx)
	if err != nil || len(linkTypes) == 0 {
		t.Fatalf("operations link types = %+v, %v", linkTypes, err)
	}
	operationsLink, _, err := handler.Commands.LinkIssue(ctx, actorID, workspaceID, issue.ID, linkTypes[0].ID, changeIssue.ID)
	if err != nil {
		t.Fatal(err)
	}
	operationsLinks, err := st.LinksByIssue(ctx, issue.ID)
	if err != nil || len(operationsLinks) != 1 || operationsLinks[0].ID != operationsLink.ID {
		t.Fatalf("linked operations work = %+v, %v", operationsLinks, err)
	}
	dependencyLinks, err := st.ServiceDependencyLinks(ctx, workspaceID, actorID, serviceDeskID)
	if err != nil || len(dependencyLinks) != 1 || dependencyLinks[0].ID != operationsLink.ID {
		t.Fatalf("service dependency links = %+v, %v", dependencyLinks, err)
	}
	if _, err := st.ServiceDependencyLinks(ctx, workspaceID, customerID, serviceDeskID); !errors.Is(err, store.ErrProjectPermission) {
		t.Fatalf("customer dependency access error = %v, want project permission", err)
	}
	if _, err := st.CreateCustomField(ctx, customFieldID, "Business impact", models.CustomFieldNumber, "Affected orders per minute"); err != nil {
		t.Fatal(err)
	}
	if err := handler.Commands.SetServiceRequestTypeFields(ctx, actorID, workspaceID, serviceDeskID, incidentTypeID, []models.ServiceRequestTypeField{
		{ID: "summary", Required: true}, {ID: "description"}, {ID: customFieldID, Required: true, HelpText: "Estimate the affected orders per minute."},
	}); err != nil {
		t.Fatal(err)
	}
	if err := handler.Commands.SetServiceRequestTypeFields(ctx, customerID, workspaceID, serviceDeskID, incidentTypeID, []models.ServiceRequestTypeField{{ID: "summary"}}); err == nil {
		t.Fatal("customer configured a service request form")
	}
	var formAudits int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM organization_audit_events WHERE actor_id=$1 AND target_id=$2 AND action='service.request_type.fields.updated'`, actorID, incidentTypeID).Scan(&formAudits); err != nil || formAudits != 1 {
		t.Fatalf("service request form audits = %d, %v", formAudits, err)
	}
	dynamicFields := callAs(customerID, "GET", "/rest/servicedeskapi/servicedesk/"+serviceDeskID+"/requesttype/"+incidentTypeID+"/field", "", 200)
	if !strings.Contains(dynamicFields.Body.String(), `"fieldId":"`+customFieldID+`"`) || !strings.Contains(dynamicFields.Body.String(), `"required":true`) || !strings.Contains(dynamicFields.Body.String(), `"type":"number"`) {
		t.Fatal(dynamicFields.Body.String())
	}
	missingDynamic := callAs(customerID, "POST", "/rest/servicedeskapi/request/validate", `{"serviceDeskId":"`+serviceDeskID+`","requestTypeId":"`+incidentTypeID+`","requestFieldValues":{"summary":"Dynamic routing question"}}`, 200)
	if !strings.Contains(missingDynamic.Body.String(), `"`+customFieldID+`":"Business impact is required."`) {
		t.Fatal(missingDynamic.Body.String())
	}
	invalidDynamic := callAs(customerID, "POST", "/rest/servicedeskapi/request/validate", `{"serviceDeskId":"`+serviceDeskID+`","requestTypeId":"`+incidentTypeID+`","requestFieldValues":{"summary":"Dynamic routing question","`+customFieldID+`":"many"}}`, 200)
	if !strings.Contains(invalidDynamic.Body.String(), `"valid":false`) || !strings.Contains(invalidDynamic.Body.String(), `"`+customFieldID+`":"Business impact must be a number."`) {
		t.Fatal(invalidDynamic.Body.String())
	}
	dynamicRequest := callAs(customerID, "POST", "/rest/servicedeskapi/request", `{"serviceDeskId":"`+serviceDeskID+`","requestTypeId":"`+incidentTypeID+`","requestFieldValues":{"summary":"Dynamic routing question","description":"Route by impact.","`+customFieldID+`":42.5}}`, 201)
	var dynamicRequestBean map[string]any
	if err := json.Unmarshal(dynamicRequest.Body.Bytes(), &dynamicRequestBean); err != nil {
		t.Fatal(err)
	}
	dynamicIssue, err := st.IssueByIDOrKey(ctx, workspaceID, dynamicRequestBean["issueKey"].(string))
	if err != nil || string(dynamicIssue.Fields[customFieldID]) != "42.5" || !strings.Contains(dynamicRequest.Body.String(), `"label":"Business impact"`) {
		t.Fatalf("dynamic request = %+v, %v, %s", dynamicIssue, err, dynamicRequest.Body.String())
	}
	if _, err := handler.Commands.CreateServiceQueue(ctx, customerID, workspaceID, serviceDeskID, "Forbidden queue", `summary ~ "checkout"`); err == nil {
		t.Fatal("customer created a custom service queue")
	}
	if _, err := handler.Commands.CreateServiceQueue(ctx, actorID, workspaceID, serviceDeskID, "Invalid queue", `unknownField = value`); err == nil {
		t.Fatal("custom queue accepted unsupported JQL")
	}
	customQueue, err := handler.Commands.CreateServiceQueue(ctx, actorID, workspaceID, serviceDeskID, "Checkout incidents", `summary ~ "checkout" ORDER BY created ASC`)
	if err != nil {
		t.Fatal(err)
	}
	_, customRequests, err := st.ServiceQueueRequests(ctx, workspaceID, actorID, serviceDeskID, customQueue.ID)
	if err != nil || !slices.ContainsFunc(customRequests, func(request *models.ServiceRequest) bool { return request.Issue.ID == issue.ID }) {
		t.Fatalf("custom queue requests = %+v, %v", customRequests, err)
	}
	if err := handler.Commands.UpdateServiceQueue(ctx, actorID, workspaceID, serviceDeskID, customQueue.ID, "Checkout incidents now", `summary ~ "does not match"`); err != nil {
		t.Fatal(err)
	}
	_, customRequests, err = st.ServiceQueueRequests(ctx, workspaceID, actorID, serviceDeskID, customQueue.ID)
	if err != nil || len(customRequests) != 0 {
		t.Fatalf("updated custom queue requests = %+v, %v", customRequests, err)
	}
	if err := handler.Commands.DeleteServiceQueue(ctx, actorID, workspaceID, serviceDeskID, customQueue.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ServiceQueue(ctx, workspaceID, serviceDeskID, customQueue.ID); err == nil {
		t.Fatal("deleted custom queue still exists")
	}
	var queueAudits int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM organization_audit_events WHERE actor_id=$1 AND target_id=$2 AND action LIKE 'service.queue.%'`, actorID, customQueue.ID).Scan(&queueAudits); err != nil || queueAudits != 3 {
		t.Fatalf("custom queue audits = %d, %v", queueAudits, err)
	}
	organizationResponse := call("POST", "/rest/servicedeskapi/organization", `{"name":"Acme Customer Group"}`, 201)
	var organizationBean map[string]any
	if err := json.Unmarshal(organizationResponse.Body.Bytes(), &organizationBean); err != nil {
		t.Fatal(err)
	}
	organizationID, _ := organizationBean["id"].(string)
	if organizationID == "" || organizationBean["scimManaged"] != false {
		t.Fatal(organizationResponse.Body.String())
	}
	callAs(customerID, "GET", "/rest/servicedeskapi/organization/"+organizationID, "", 404)
	call("POST", "/rest/servicedeskapi/organization/"+organizationID+"/user", `{"accountIds":["`+customerID+`","`+invitedCustomerID+`"],"usernames":[]}`, 204)
	organization := callAs(customerID, "GET", "/rest/servicedeskapi/organization/"+organizationID, "", 200)
	if !strings.Contains(organization.Body.String(), `"name":"Acme Customer Group"`) {
		t.Fatal(organization.Body.String())
	}
	organizations := callAs(customerID, "GET", "/rest/servicedeskapi/organization", "", 200)
	if !strings.Contains(organizations.Body.String(), `"name":"Acme Customer Group"`) {
		t.Fatal(organizations.Body.String())
	}
	call("GET", "/rest/servicedeskapi/organization?accountId="+customerID, "", 200)
	call("PUT", "/rest/servicedeskapi/organization/"+organizationID+"/property/support-profile", `{"tier":"gold","region":"eu"}`, 204)
	property := callAs(customerID, "GET", "/rest/servicedeskapi/organization/"+organizationID+"/property/support-profile", "", 200)
	if !strings.Contains(property.Body.String(), `"tier":"gold"`) {
		t.Fatal(property.Body.String())
	}
	keys := callAs(customerID, "GET", "/rest/servicedeskapi/organization/"+organizationID+"/property", "", 200)
	if !strings.Contains(keys.Body.String(), `"key":"support-profile"`) {
		t.Fatal(keys.Body.String())
	}
	organizationUsers := call("GET", "/rest/servicedeskapi/organization/"+organizationID+"/user", "", 200)
	if !strings.Contains(organizationUsers.Body.String(), customerID) || !strings.Contains(organizationUsers.Body.String(), invitedCustomerID) {
		t.Fatal(organizationUsers.Body.String())
	}
	call("POST", "/rest/servicedeskapi/servicedesk/"+serviceDeskID+"/organization", `{"organizationId":`+organizationID+`}`, 204)
	deskOrganizations := call("GET", "/rest/servicedeskapi/servicedesk/"+serviceDeskID+"/organization", "", 200)
	if !strings.Contains(deskOrganizations.Body.String(), `"name":"Acme Customer Group"`) {
		t.Fatal(deskOrganizations.Body.String())
	}
	if err := handler.Commands.SetServiceDeskCustomerAccess(ctx, actorID, workspaceID, serviceDeskID, false); err != nil {
		t.Fatal(err)
	}
	call("DELETE", "/rest/servicedeskapi/servicedesk/"+serviceDeskID+"/customer", `{"accountIds":["`+customerID+`"]}`, 204)
	callAs(customerID, "POST", "/rest/servicedeskapi/request", `{"serviceDeskId":"`+serviceDeskID+`","requestTypeId":"`+requestTypeID+`","requestFieldValues":{"summary":"Organization portal admission"}}`, 201)
	if err := handler.Commands.SetServiceDeskCustomerAccess(ctx, actorID, workspaceID, serviceDeskID, true); err != nil {
		t.Fatal(err)
	}
	call("DELETE", "/rest/servicedeskapi/organization/"+organizationID+"/user", `{"accountIds":["`+customerID+`"]}`, 204)
	callAs(customerID, "GET", "/rest/servicedeskapi/organization/"+organizationID, "", 404)
	call("DELETE", "/rest/servicedeskapi/organization/"+organizationID+"/property/support-profile", "", 204)
	call("DELETE", "/rest/servicedeskapi/servicedesk/"+serviceDeskID+"/organization", `{"organizationId":`+organizationID+`}`, 204)
	call("DELETE", "/rest/servicedeskapi/organization/"+organizationID, "", 204)
	call("GET", "/rest/servicedeskapi/organization/"+organizationID, "", 404)
	subscription := callAs(customerID, "GET", "/rest/servicedeskapi/request/"+issueKey+"/notification", "", 200)
	if !strings.Contains(subscription.Body.String(), `"subscribed":true`) {
		t.Fatal(subscription.Body.String())
	}
	callAs(customerID, "DELETE", "/rest/servicedeskapi/request/"+issueKey+"/notification", "", 204)
	subscription = callAs(customerID, "GET", "/rest/servicedeskapi/request/"+issueKey+"/notification", "", 200)
	if !strings.Contains(subscription.Body.String(), `"subscribed":false`) {
		t.Fatal(subscription.Body.String())
	}
	callAs(customerID, "PUT", "/rest/servicedeskapi/request/"+issueKey+"/notification", "", 204)
	callAs(customerID, "POST", "/rest/servicedeskapi/request/"+issueKey+"/feedback", `{"type":"csat","rating":5}`, 400)
	approval, err := handler.Commands.CreateServiceApproval(ctx, actorID, workspaceID, issue.ID, "Production change approval", []string{customerID})
	if err != nil {
		t.Fatal(err)
	}
	approvalList := callAs(customerID, "GET", "/rest/servicedeskapi/request/"+issueKey+"/approval", "", 200)
	if !strings.Contains(approvalList.Body.String(), `"canAnswerApproval":true`) || !strings.Contains(approvalList.Body.String(), "Production change approval") {
		t.Fatal(approvalList.Body.String())
	}
	call("POST", "/rest/servicedeskapi/request/"+issueKey+"/approval/"+approval.ID, `{"decision":"approve"}`, 400)
	approved := callAs(customerID, "POST", "/rest/servicedeskapi/request/"+issueKey+"/approval/"+approval.ID, `{"decision":"approve"}`, 200)
	if !strings.Contains(approved.Body.String(), `"finalDecision":"approved"`) || !strings.Contains(approved.Body.String(), `"completedDate"`) {
		t.Fatal(approved.Body.String())
	}
	callAs(customerID, "GET", "/rest/servicedeskapi/request/"+issueKey+"/approval/"+approval.ID, "", 200)
	multiApproval, err := handler.Commands.CreateServiceApproval(ctx, actorID, workspaceID, issue.ID, "Two-person change approval", []string{customerID, agentID})
	if err != nil {
		t.Fatal(err)
	}
	pendingApproval := callAs(customerID, "POST", "/rest/servicedeskapi/request/"+issueKey+"/approval/"+multiApproval.ID, `{"decision":"approve"}`, 200)
	if !strings.Contains(pendingApproval.Body.String(), `"finalDecision":"pending"`) {
		t.Fatal(pendingApproval.Body.String())
	}
	declinedApproval := callAs(agentID, "POST", "/rest/servicedeskapi/request/"+issueKey+"/approval/"+multiApproval.ID, `{"decision":"decline"}`, 200)
	if !strings.Contains(declinedApproval.Body.String(), `"finalDecision":"declined"`) {
		t.Fatal(declinedApproval.Body.String())
	}

	temporaryResponse := callMultipartAs(customerID, "/rest/servicedeskapi/servicedesk/"+serviceDeskID+"/attachTemporaryFile", "customer-log.txt", "customer-visible-log", 201)
	var temporaryBean struct {
		TemporaryAttachments []struct {
			ID string `json:"temporaryAttachmentId"`
		} `json:"temporaryAttachments"`
	}
	if err := json.Unmarshal(temporaryResponse.Body.Bytes(), &temporaryBean); err != nil || len(temporaryBean.TemporaryAttachments) != 1 {
		t.Fatalf("temporary attachment: %v %s", err, temporaryResponse.Body.String())
	}
	createdAttachment := callAs(customerID, "POST", "/rest/servicedeskapi/request/"+issueKey+"/attachment", `{"temporaryAttachmentIds":["`+temporaryBean.TemporaryAttachments[0].ID+`"],"public":true,"additionalComment":{"body":"Diagnostic log"}}`, 201)
	if !strings.Contains(createdAttachment.Body.String(), "customer-log.txt") || !strings.Contains(createdAttachment.Body.String(), "Diagnostic log") {
		t.Fatal(createdAttachment.Body.String())
	}
	var attachmentNotificationCount int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE workspace_id=$1 AND user_id=$2 AND kind='service_comment'`, workspaceID, agentID).Scan(&attachmentNotificationCount); err != nil || attachmentNotificationCount != 1 {
		t.Fatalf("attachment notifications = %d, %v", attachmentNotificationCount, err)
	}
	var publicAttachmentID string
	if err := st.Pool.QueryRow(ctx, `SELECT a.id FROM attachments a WHERE a.issue_id=$1 AND a.filename='customer-log.txt'`, issue.ID).Scan(&publicAttachmentID); err != nil {
		t.Fatal(err)
	}
	var publicAttachmentCommentID string
	if err := st.Pool.QueryRow(ctx, `SELECT comment_id FROM service_request_attachments WHERE attachment_id=$1`, publicAttachmentID).Scan(&publicAttachmentCommentID); err != nil {
		t.Fatal(err)
	}
	commentAttachments := callAs(customerID, "GET", "/rest/servicedeskapi/request/"+issueKey+"/comment/"+publicAttachmentCommentID+"/attachment", "", 200)
	if !strings.Contains(commentAttachments.Body.String(), "customer-log.txt") {
		t.Fatal(commentAttachments.Body.String())
	}
	content := callAs(customerID, "GET", "/rest/servicedeskapi/request/"+issueKey+"/attachment/"+publicAttachmentID, "", 200)
	if content.Body.String() != "customer-visible-log" {
		t.Fatal(content.Body.String())
	}
	attachmentList := callAs(customerID, "GET", "/rest/servicedeskapi/request/"+issueKey+"/attachment?start=0&limit=50", "", 200)
	if !strings.Contains(attachmentList.Body.String(), "customer-log.txt") {
		t.Fatal(attachmentList.Body.String())
	}

	internalTemporary := callMultipartAs(actorID, "/rest/servicedeskapi/servicedesk/"+serviceDeskID+"/attachTemporaryFile", "agent-note.txt", "agent-only-note", 201)
	if err := json.Unmarshal(internalTemporary.Body.Bytes(), &temporaryBean); err != nil {
		t.Fatal(err)
	}
	call("POST", "/rest/servicedeskapi/request/"+issueKey+"/attachment", `{"temporaryAttachmentIds":["`+temporaryBean.TemporaryAttachments[0].ID+`"],"public":false,"additionalComment":{"body":"Internal evidence"}}`, 201)
	var internalAttachmentID string
	if err := st.Pool.QueryRow(ctx, `SELECT a.id FROM attachments a WHERE a.issue_id=$1 AND a.filename='agent-note.txt'`, issue.ID).Scan(&internalAttachmentID); err != nil {
		t.Fatal(err)
	}
	customerAttachmentList := callAs(customerID, "GET", "/rest/servicedeskapi/request/"+issueKey+"/attachment?start=0&limit=50", "", 200)
	if strings.Contains(customerAttachmentList.Body.String(), "agent-note.txt") {
		t.Fatal(customerAttachmentList.Body.String())
	}
	callAs(customerID, "GET", "/rest/servicedeskapi/request/"+issueKey+"/attachment/"+internalAttachmentID, "", 404)
	callAs(customerID, "GET", "/rest/api/3/attachment/content/"+internalAttachmentID, "", 404)
	call("GET", "/rest/servicedeskapi/request/"+issueKey+"/attachment/"+internalAttachmentID+"/thumbnail", "", 200)
	expiredTemporary := callMultipartAs(customerID, "/rest/servicedeskapi/servicedesk/"+serviceDeskID+"/attachTemporaryFile", "expired.txt", "expired-content", 201)
	if err := json.Unmarshal(expiredTemporary.Body.Bytes(), &temporaryBean); err != nil {
		t.Fatal(err)
	}
	var expiredBlobRef string
	if err := st.Pool.QueryRow(ctx, `UPDATE service_temporary_attachments SET expires_at=now()-interval '1 minute' WHERE id=$1 RETURNING blob_ref`, temporaryBean.TemporaryAttachments[0].ID).Scan(&expiredBlobRef); err != nil {
		t.Fatal(err)
	}
	if err := (&commands.ServiceTemporaryAttachmentRunner{Service: handler.Commands}).DrainOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err := blobs.Get(ctx, expiredBlobRef); !errors.Is(err, attachments.ErrNotFound) {
		t.Fatalf("expired temporary blob error = %v", err)
	}
	metrics, err := st.ServiceSLAMetrics(ctx, workspaceID, serviceDeskID)
	if err != nil || len(metrics) != 2 {
		t.Fatalf("SLA metrics = %+v, %v", metrics, err)
	}
	metricByKind := make(map[string]string, len(metrics))
	for _, metric := range metrics {
		metricByKind[metric.Kind] = metric.ID
	}
	if _, err := handler.Commands.CreateServiceSLAGoal(ctx, customerID, workspaceID, serviceDeskID, metricByKind["first_response"], "Forbidden incident response", `labels = incident`, time.Hour.Milliseconds()); err == nil {
		t.Fatal("customer configured a conditional SLA goal")
	}
	if _, err := handler.Commands.CreateServiceSLAGoal(ctx, actorID, workspaceID, serviceDeskID, metricByKind["first_response"], "Invalid ordered goal", `labels = incident ORDER BY created ASC`, time.Hour.Milliseconds()); err == nil {
		t.Fatal("conditional SLA goal accepted ORDER BY")
	}
	incidentGoal, err := handler.Commands.CreateServiceSLAGoal(ctx, actorID, workspaceID, serviceDeskID, metricByKind["first_response"], "Incident response", `labels = incident`, time.Hour.Milliseconds())
	if err != nil {
		t.Fatal(err)
	}
	keywordGoal, err := handler.Commands.CreateServiceSLAGoal(ctx, actorID, workspaceID, serviceDeskID, metricByKind["first_response"], "Keyword response", `summary ~ "conditional"`, (2 * time.Hour).Milliseconds())
	if err != nil {
		t.Fatal(err)
	}
	conditionalRequest := callAs(customerID, "POST", "/rest/servicedeskapi/request", `{"serviceDeskId":"`+serviceDeskID+`","requestTypeId":"`+incidentTypeID+`","requestFieldValues":{"summary":"Incident with conditional response target","description":"Verify selected goal snapshots.","`+customFieldID+`":7}}`, 201)
	var conditionalRequestBean map[string]any
	if err := json.Unmarshal(conditionalRequest.Body.Bytes(), &conditionalRequestBean); err != nil {
		t.Fatal(err)
	}
	conditionalIssue, err := st.IssueByIDOrKey(ctx, workspaceID, conditionalRequestBean["issueKey"].(string))
	if err != nil {
		t.Fatal(err)
	}
	conditionalSLAs, err := st.ServiceSLAs(ctx, workspaceID, conditionalIssue.ID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	var conditionalCycle *models.ServiceSLACycle
	for index := range conditionalSLAs {
		if conditionalSLAs[index].Kind == "first_response" {
			conditionalCycle = conditionalSLAs[index].OngoingCycle
		}
	}
	if conditionalCycle == nil || conditionalCycle.GoalID != incidentGoal.ID || conditionalCycle.GoalName != "Incident response" || conditionalCycle.GoalMillis != time.Hour.Milliseconds() {
		t.Fatalf("selected conditional SLA cycle = %+v", conditionalCycle)
	}
	defaultRequest := callAs(customerID, "POST", "/rest/servicedeskapi/request", `{"serviceDeskId":"`+serviceDeskID+`","requestTypeId":"`+requestTypeID+`","requestFieldValues":{"summary":"General hardware request"}}`, 201)
	var defaultRequestBean map[string]any
	if err := json.Unmarshal(defaultRequest.Body.Bytes(), &defaultRequestBean); err != nil {
		t.Fatal(err)
	}
	defaultIssue, err := st.IssueByIDOrKey(ctx, workspaceID, defaultRequestBean["issueKey"].(string))
	if err != nil {
		t.Fatal(err)
	}
	defaultSLAs, err := st.ServiceSLAs(ctx, workspaceID, defaultIssue.ID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	for index := range defaultSLAs {
		if defaultSLAs[index].Kind == "first_response" && (defaultSLAs[index].OngoingCycle == nil || defaultSLAs[index].OngoingCycle.GoalName != "Default goal") {
			t.Fatalf("default SLA fallback = %+v", defaultSLAs[index].OngoingCycle)
		}
	}
	if err := handler.Commands.DeleteServiceSLAGoal(ctx, actorID, workspaceID, serviceDeskID, metricByKind["first_response"], keywordGoal.ID); err != nil {
		t.Fatal(err)
	}
	if err := handler.Commands.UpdateServiceSLAGoal(ctx, actorID, workspaceID, serviceDeskID, metricByKind["first_response"], incidentGoal.ID, "Priority incident response", `labels = incident`, (90 * time.Minute).Milliseconds()); err != nil {
		t.Fatal(err)
	}
	conditionalSLA := call("GET", "/rest/servicedeskapi/request/"+conditionalIssue.Key+"/sla/"+metricByKind["first_response"], "", 200)
	if !strings.Contains(conditionalSLA.Body.String(), `"millis":5400000`) {
		t.Fatal(conditionalSLA.Body.String())
	}
	if err := handler.Commands.DeleteServiceSLAGoal(ctx, actorID, workspaceID, serviceDeskID, metricByKind["first_response"], incidentGoal.ID); err != nil {
		t.Fatal(err)
	}
	conditionalSLAs, err = st.ServiceSLAs(ctx, workspaceID, conditionalIssue.ID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	conditionalCycle = nil
	for index := range conditionalSLAs {
		if conditionalSLAs[index].Kind == "first_response" {
			conditionalCycle = conditionalSLAs[index].OngoingCycle
		}
	}
	if conditionalCycle == nil || conditionalCycle.GoalID != "" || conditionalCycle.GoalName != "Priority incident response" || conditionalCycle.GoalMillis != (90*time.Minute).Milliseconds() {
		t.Fatalf("deleted conditional SLA snapshot = %+v", conditionalCycle)
	}
	var conditionalGoalAudits int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM organization_audit_events WHERE actor_id=$1 AND target_id=$2 AND action LIKE 'service.sla.goal.%'`, actorID, incidentGoal.ID).Scan(&conditionalGoalAudits); err != nil || conditionalGoalAudits != 3 {
		t.Fatalf("conditional SLA goal audits = %d, %v", conditionalGoalAudits, err)
	}
	callAs(customerID, "GET", "/rest/servicedeskapi/request/"+issueKey+"/sla", "", 403)
	slaList := call("GET", "/rest/servicedeskapi/request/"+issueKey+"/sla", "", 200)
	if !strings.Contains(slaList.Body.String(), "Time to first response") || !strings.Contains(slaList.Body.String(), `"ongoingCycle"`) {
		t.Fatal(slaList.Body.String())
	}
	call("GET", "/rest/servicedeskapi/request/"+issueKey+"/sla/does-not-exist", "", 404)
	if err := handler.Commands.UpdateServiceCalendar(ctx, actorID, workspaceID, serviceDeskID, "Every day", "UTC", []int16{1, 2, 3, 4, 5, 6, 7}, 0, 1440); err != nil {
		t.Fatal(err)
	}
	if err := handler.Commands.UpsertServiceCalendarHoliday(ctx, customerID, workspaceID, serviceDeskID, "2030-01-01", "Forbidden holiday"); err == nil {
		t.Fatal("customer configured a service calendar holiday")
	}
	if err := handler.Commands.UpsertServiceCalendarHoliday(ctx, actorID, workspaceID, serviceDeskID, "2030-01-01", "Regional support shutdown"); err != nil {
		t.Fatal(err)
	}
	calendar, err := st.ServiceCalendar(ctx, workspaceID, serviceDeskID)
	if err != nil || calendar.Holidays["2030-01-01"] != "Regional support shutdown" {
		t.Fatalf("service calendar holidays = %+v, %v", calendar, err)
	}
	if err := handler.Commands.DeleteServiceCalendarHoliday(ctx, actorID, workspaceID, serviceDeskID, "2030-01-01"); err != nil {
		t.Fatal(err)
	}
	if err := handler.Commands.UpdateServiceSLAMetric(ctx, actorID, workspaceID, serviceDeskID, metricByKind["first_response"], (2 * time.Hour).Milliseconds()); err != nil {
		t.Fatal(err)
	}
	configuredSLA := call("GET", "/rest/servicedeskapi/request/"+issueKey+"/sla/"+metricByKind["first_response"], "", 200)
	var configuredSLABean map[string]any
	if err := json.Unmarshal(configuredSLA.Body.Bytes(), &configuredSLABean); err != nil {
		t.Fatal(err)
	}
	ongoingCycle, _ := configuredSLABean["ongoingCycle"].(map[string]any)
	goalDuration, _ := ongoingCycle["goalDuration"].(map[string]any)
	if goalDuration["millis"] != float64((2 * time.Hour).Milliseconds()) {
		t.Fatal(configuredSLA.Body.String())
	}
	var serviceConfigAudits int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM organization_audit_events WHERE actor_id=$1 AND action IN ('service.calendar.updated','service.sla.updated')`, actorID).Scan(&serviceConfigAudits); err != nil || serviceConfigAudits != 2 {
		t.Fatalf("service configuration audits = %d, %v", serviceConfigAudits, err)
	}
	var holidayAudits int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM organization_audit_events WHERE actor_id=$1 AND action LIKE 'service.calendar.holiday.%'`, actorID).Scan(&holidayAudits); err != nil || holidayAudits != 2 {
		t.Fatalf("service holiday audits = %d, %v", holidayAudits, err)
	}
	escalationNow := time.Now().UTC().Truncate(time.Second)
	if _, err := st.Pool.Exec(ctx, `UPDATE service_sla_cycles SET started_at=$2 WHERE request_issue_id=$1 AND metric_id=$3`, issue.ID, escalationNow.Add(-110*time.Minute), metricByKind["first_response"]); err != nil {
		t.Fatal(err)
	}
	runner := &store.ServiceSLARunner{Store: st, Now: func() time.Time { return escalationNow }}
	if err := runner.DrainOnce(ctx, workspaceID); err != nil {
		t.Fatal(err)
	}
	if err := runner.DrainOnce(ctx, workspaceID); err != nil {
		t.Fatal(err)
	}
	var warningCount int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE workspace_id=$1 AND kind='service_sla_warning'`, workspaceID).Scan(&warningCount); err != nil || warningCount != 1 {
		t.Fatalf("warning notifications = %d, %v", warningCount, err)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE service_sla_cycles SET started_at=$2 WHERE request_issue_id=$1 AND metric_id=$3`, issue.ID, escalationNow.Add(-3*time.Hour), metricByKind["first_response"]); err != nil {
		t.Fatal(err)
	}
	if err := runner.DrainOnce(ctx, workspaceID); err != nil {
		t.Fatal(err)
	}
	var breachCount int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE workspace_id=$1 AND kind='service_sla_breached'`, workspaceID).Scan(&breachCount); err != nil || breachCount != 1 {
		t.Fatalf("breach notifications = %d, %v", breachCount, err)
	}
	var attentionQueueID string
	if err := st.Pool.QueryRow(ctx, `SELECT id FROM service_queues WHERE service_desk_id=$1 AND kind='sla_attention'`, serviceDeskID).Scan(&attentionQueueID); err != nil {
		t.Fatal(err)
	}
	attention := call("GET", "/rest/servicedeskapi/servicedesk/"+serviceDeskID+"/queue/"+attentionQueueID+"/issue", "", 200)
	if !strings.Contains(attention.Body.String(), issueKey) {
		t.Fatal(attention.Body.String())
	}
	owned := callAs(customerID, "GET", "/rest/servicedeskapi/request", "", 200)
	if !strings.Contains(owned.Body.String(), issueKey) {
		t.Fatal(owned.Body.String())
	}
	callAs(customerID, "POST", "/rest/servicedeskapi/request/"+issueKey+"/comment", `{"body":"I can reproduce this in two browsers.","public":true}`, 201)
	call("POST", "/rest/servicedeskapi/request/"+issueKey+"/comment", `{"body":"Escalate to the payments team.","public":false}`, 201)
	customerComments := callAs(customerID, "GET", "/rest/servicedeskapi/request/"+issueKey+"/comment", "", 200)
	if !strings.Contains(customerComments.Body.String(), "two browsers") || strings.Contains(customerComments.Body.String(), "payments team") {
		t.Fatal(customerComments.Body.String())
	}
	agentComments := call("GET", "/rest/servicedeskapi/request/"+issueKey+"/comment", "", 200)
	if !strings.Contains(agentComments.Body.String(), "payments team") {
		t.Fatal(agentComments.Body.String())
	}
	callAs(customerID, "GET", "/rest/servicedeskapi/request/"+issueKey+"/status", "", 200)
	transitions := callAs(customerID, "GET", "/rest/servicedeskapi/request/"+issueKey+"/transition", "", 200)
	if !strings.Contains(transitions.Body.String(), `"id":"21"`) {
		t.Fatal(transitions.Body.String())
	}
	callAs(customerID, "POST", "/rest/servicedeskapi/request/"+issueKey+"/transition", `{"id":"21","additionalComment":{"body":"Work can begin.","public":true}}`, 204)
	detail := callAs(customerID, "GET", "/rest/servicedeskapi/request/"+issueKey, "", 200)
	if !strings.Contains(detail.Body.String(), `"status":"In Progress"`) || !strings.Contains(detail.Body.String(), "Work can begin") {
		t.Fatal(detail.Body.String())
	}
	participants := callAs(customerID, "POST", "/rest/servicedeskapi/request/"+issueKey+"/participant", `{"usernames":["invited.customer@example.test"]}`, 200)
	if !strings.Contains(participants.Body.String(), "Invited Customer") {
		t.Fatal(participants.Body.String())
	}
	if _, err := st.ServiceRequest(ctx, workspaceID, invitedCustomerID, issueKey, false); err != nil {
		t.Fatalf("participant access: %v", err)
	}
	listedParticipants := callAs(customerID, "GET", "/rest/servicedeskapi/request/"+issueKey+"/participant", "", 200)
	if !strings.Contains(listedParticipants.Body.String(), invitedCustomerID) {
		t.Fatal(listedParticipants.Body.String())
	}
	removedParticipants := callAs(customerID, "DELETE", "/rest/servicedeskapi/request/"+issueKey+"/participant", `{"accountIds":["`+invitedCustomerID+`"]}`, 200)
	if strings.Contains(removedParticipants.Body.String(), invitedCustomerID) {
		t.Fatal(removedParticipants.Body.String())
	}
	if _, err := st.ServiceRequest(ctx, workspaceID, invitedCustomerID, issueKey, false); err == nil {
		t.Fatal("removed participant retained request access")
	}
	allRequests := call("GET", "/rest/servicedeskapi/request?requestOwnership=ALL_REQUESTS", "", 200)
	if !strings.Contains(allRequests.Body.String(), issueKey) {
		t.Fatal(allRequests.Body.String())
	}
	call("POST", "/rest/api/3/project", `{"key":"OPS","name":"Operations desk","projectTypeKey":"service_desk","projectTemplateKey":"com.atlassian.servicedesk:simplified-it-service-management","leadAccountId":"`+actorID+`"}`, 201)
	otherProject, err := st.ProjectByKey(ctx, workspaceID, "OPS")
	if err != nil {
		t.Fatal(err)
	}
	var otherDeskID, otherTypeID string
	if err := st.Pool.QueryRow(ctx, `SELECT id FROM service_desks WHERE project_id=$1`, otherProject.ID).Scan(&otherDeskID); err != nil {
		t.Fatal(err)
	}
	if err := st.Pool.QueryRow(ctx, `SELECT id FROM service_request_types WHERE service_desk_id=$1 ORDER BY id::bigint LIMIT 1`, otherDeskID).Scan(&otherTypeID); err != nil {
		t.Fatal(err)
	}
	otherRequest := callAs(customerID, "POST", "/rest/servicedeskapi/request", `{"serviceDeskId":"`+otherDeskID+`","requestTypeId":"`+otherTypeID+`","requestFieldValues":{"summary":"Other desk request"}}`, 201)
	var otherRequestBean map[string]any
	if err := json.Unmarshal(otherRequest.Body.Bytes(), &otherRequestBean); err != nil {
		t.Fatal(err)
	}
	otherIssueKey, _ := otherRequestBean["issueKey"].(string)
	queues := call("GET", "/rest/servicedeskapi/servicedesk/"+serviceDeskID+"/queue?includeCount=true", "", 200)
	if !strings.Contains(queues.Body.String(), "Unassigned requests") || !strings.Contains(queues.Body.String(), `"issueCount"`) {
		t.Fatal(queues.Body.String())
	}
	callAs(customerID, "GET", "/rest/servicedeskapi/servicedesk/"+serviceDeskID+"/queue", "", 403)
	callAs(agentID, "GET", "/rest/servicedeskapi/servicedesk/"+serviceDeskID+"/queue", "", 403)
	callAs(agentID, "GET", "/rest/servicedeskapi/request?requestOwnership=ALL_REQUESTS", "", 403)
	if err := handler.Commands.SetServiceDeskAgent(ctx, actorID, workspaceID, serviceDeskID, agentID, true); err != nil {
		t.Fatal(err)
	}
	if err := handler.Commands.SetServiceDeskAgent(ctx, agentID, workspaceID, serviceDeskID, customerID, true); err == nil {
		t.Fatal("regular service agent managed the agent roster")
	}
	agentQueues := callAs(agentID, "GET", "/rest/servicedeskapi/servicedesk/"+serviceDeskID+"/queue?includeCount=true", "", 200)
	if !strings.Contains(agentQueues.Body.String(), "Unassigned requests") {
		t.Fatal(agentQueues.Body.String())
	}
	agentRequests := callAs(agentID, "GET", "/rest/servicedeskapi/request?requestOwnership=ALL_REQUESTS", "", 200)
	if !strings.Contains(agentRequests.Body.String(), issueKey) || strings.Contains(agentRequests.Body.String(), otherIssueKey) {
		t.Fatal(agentRequests.Body.String())
	}
	callAs(agentID, "GET", "/rest/servicedeskapi/servicedesk/"+otherDeskID+"/queue", "", 403)
	callAs(agentID, "GET", "/rest/servicedeskapi/request/"+otherIssueKey, "", 404)
	callAs(agentID, "POST", "/rest/servicedeskapi/customer", `{"email":"agent-created.customer@example.test","displayName":"Agent-created Customer"}`, 403)
	agentCustomer := call("POST", "/rest/servicedeskapi/customer", `{"email":"agent-created.customer@example.test","displayName":"Agent-created Customer"}`, 201)
	if !strings.Contains(agentCustomer.Body.String(), "Agent-created Customer") {
		t.Fatal(agentCustomer.Body.String())
	}
	agentRaised := callAs(agentID, "POST", "/rest/servicedeskapi/request", `{"serviceDeskId":"`+serviceDeskID+`","requestTypeId":"`+requestTypeID+`","raiseOnBehalfOf":"agent-created.customer@example.test","requestFieldValues":{"summary":"Agent-raised customer request"}}`, 201)
	if !strings.Contains(agentRaised.Body.String(), "Agent-raised customer request") {
		t.Fatal(agentRaised.Body.String())
	}
	agentDetail := callAs(agentID, "GET", "/rest/servicedeskapi/request/"+issueKey, "", 200)
	if !strings.Contains(agentDetail.Body.String(), `"sla":[{`) {
		t.Fatal(agentDetail.Body.String())
	}
	var customerCommentNotificationsBefore int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE workspace_id=$1 AND user_id=$2 AND kind='service_comment'`, workspaceID, customerID).Scan(&customerCommentNotificationsBefore); err != nil {
		t.Fatal(err)
	}
	callAs(agentID, "POST", "/rest/servicedeskapi/request/"+issueKey+"/comment", `{"body":"Agent-only investigation detail.","public":false}`, 201)
	var customerCommentNotificationsAfter int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE workspace_id=$1 AND user_id=$2 AND kind='service_comment'`, workspaceID, customerID).Scan(&customerCommentNotificationsAfter); err != nil || customerCommentNotificationsAfter != customerCommentNotificationsBefore {
		t.Fatalf("private comment customer notifications = %d before, %d after, %v", customerCommentNotificationsBefore, customerCommentNotificationsAfter, err)
	}
	regularAgentComments := callAs(agentID, "GET", "/rest/servicedeskapi/request/"+issueKey+"/comment", "", 200)
	if !strings.Contains(regularAgentComments.Body.String(), "Agent-only investigation detail") {
		t.Fatal(regularAgentComments.Body.String())
	}
	customerComments = callAs(customerID, "GET", "/rest/servicedeskapi/request/"+issueKey+"/comment", "", 200)
	if strings.Contains(customerComments.Body.String(), "Agent-only investigation detail") {
		t.Fatal(customerComments.Body.String())
	}
	callAs(agentID, "POST", "/rest/servicedeskapi/request/"+issueKey+"/comment", `{"body":"The service team is investigating.","public":true}`, 201)
	firstResponseSLA := callAs(agentID, "GET", "/rest/servicedeskapi/request/"+issueKey+"/sla/"+metricByKind["first_response"], "", 200)
	if !strings.Contains(firstResponseSLA.Body.String(), `"completedCycles":[{`) || strings.Contains(firstResponseSLA.Body.String(), `"ongoingCycle"`) {
		t.Fatal(firstResponseSLA.Body.String())
	}
	var unassignedQueueID, mineQueueID string
	if err := st.Pool.QueryRow(ctx, `SELECT id FROM service_queues WHERE service_desk_id=$1 AND kind='unassigned'`, serviceDeskID).Scan(&unassignedQueueID); err != nil {
		t.Fatal(err)
	}
	if err := st.Pool.QueryRow(ctx, `SELECT id FROM service_queues WHERE service_desk_id=$1 AND kind='assigned_to_me'`, serviceDeskID).Scan(&mineQueueID); err != nil {
		t.Fatal(err)
	}
	unassigned := call("GET", "/rest/servicedeskapi/servicedesk/"+serviceDeskID+"/queue/"+unassignedQueueID+"/issue", "", 200)
	if !strings.Contains(unassigned.Body.String(), issueKey) {
		t.Fatal(unassigned.Body.String())
	}
	assigneeID := actorID
	if _, _, err := handler.Commands.UpdateIssue(ctx, commands.UpdateIssueInput{ActorID: actorID, WorkspaceID: workspaceID, IssueIDOrKey: issueKey, AssigneeID: &assigneeID}); err != nil {
		t.Fatal(err)
	}
	assigned := call("GET", "/rest/servicedeskapi/servicedesk/"+serviceDeskID+"/queue/"+mineQueueID+"/issue", "", 200)
	if !strings.Contains(assigned.Body.String(), issueKey) {
		t.Fatal(assigned.Body.String())
	}
	callAs(agentID, "POST", "/rest/servicedeskapi/request/"+issueKey+"/transition", `{"id":"31"}`, 204)
	resolutionSLA := callAs(agentID, "GET", "/rest/servicedeskapi/request/"+issueKey+"/sla/"+metricByKind["resolution"], "", 200)
	if !strings.Contains(resolutionSLA.Body.String(), `"completedCycles":[{`) || strings.Contains(resolutionSLA.Body.String(), `"ongoingCycle"`) {
		t.Fatal(resolutionSLA.Body.String())
	}
	feedback := callAs(customerID, "POST", "/rest/servicedeskapi/request/"+issueKey+"/feedback", `{"type":"csat","rating":5,"comment":{"body":"Fast and clear resolution."}}`, 201)
	if !strings.Contains(feedback.Body.String(), `"rating":5`) || !strings.Contains(feedback.Body.String(), "Fast and clear resolution.") {
		t.Fatal(feedback.Body.String())
	}
	var feedbackNotificationCount int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE workspace_id=$1 AND user_id=$2 AND kind='service_feedback'`, workspaceID, agentID).Scan(&feedbackNotificationCount); err != nil || feedbackNotificationCount != 1 {
		t.Fatalf("feedback notifications = %d, %v", feedbackNotificationCount, err)
	}
	callAs(agentID, "GET", "/rest/servicedeskapi/request/"+issueKey+"/feedback", "", 200)
	report, err := st.ServiceReport(ctx, workspaceID, serviceDeskID, 30, time.Now().UTC())
	if err != nil || report.TotalRequests < 5 || report.ResolvedRequests < 1 || report.SatisfactionResponses != 1 || report.AverageSatisfaction != 5 || len(report.Daily) != 30 || len(report.RequestTypes) == 0 || len(report.Channels) == 0 {
		t.Fatalf("service report = %+v, %v", report, err)
	}
	resolvedReport, err := st.ServiceReportFiltered(ctx, workspaceID, serviceDeskID, models.ServiceReportFilter{Status: "resolved"}, 30, time.Now().UTC())
	if err != nil || resolvedReport.TotalRequests != 1 || resolvedReport.OpenRequests != 0 || resolvedReport.ResolvedRequests != 1 || resolvedReport.SatisfactionResponses != 1 {
		t.Fatalf("resolved service report = %+v, %v", resolvedReport, err)
	}
	incidentReport, err := st.ServiceReportFiltered(ctx, workspaceID, serviceDeskID, models.ServiceReportFilter{RequestTypeID: incidentTypeID, Channel: "portal"}, 30, time.Now().UTC())
	if err != nil || incidentReport.TotalRequests < 3 || len(incidentReport.RequestTypes) != 1 || incidentReport.RequestTypes[0].ID != incidentTypeID {
		t.Fatalf("segmented incident report = %+v, %v", incidentReport, err)
	}
	channels, err := st.ServiceRequestChannels(ctx, workspaceID, serviceDeskID)
	if err != nil || !slices.Contains(channels, "portal") {
		t.Fatalf("service request channels = %+v, %v", channels, err)
	}
	if _, err := st.ServiceReportFiltered(ctx, workspaceID, serviceDeskID, models.ServiceReportFilter{Status: "invalid"}, 30, time.Now().UTC()); err == nil {
		t.Fatal("service report accepted unsupported status filter")
	}
	if _, err := st.ServiceReport(ctx, workspaceID, serviceDeskID, 31, time.Now().UTC()); err == nil {
		t.Fatal("service report accepted unsupported window")
	}
	call("DELETE", "/rest/servicedeskapi/request/"+issueKey+"/feedback", "", 400)
	callAs(customerID, "DELETE", "/rest/servicedeskapi/request/"+issueKey+"/feedback", "", 204)
	callAs(customerID, "GET", "/rest/servicedeskapi/request/"+issueKey+"/feedback", "", 404)
	if err := handler.Commands.SetServiceDeskAgent(ctx, actorID, workspaceID, serviceDeskID, agentID, false); err != nil {
		t.Fatal(err)
	}
	callAs(agentID, "GET", "/rest/servicedeskapi/servicedesk/"+serviceDeskID+"/queue", "", 403)
	formerAgentView := callAs(agentID, "GET", "/rest/servicedeskapi/request/"+issueKey, "", 200)
	if strings.Contains(formerAgentView.Body.String(), "Agent-only investigation detail") || strings.Contains(formerAgentView.Body.String(), "agent-note.txt") {
		t.Fatal(formerAgentView.Body.String())
	}
}
