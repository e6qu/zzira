package api3

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/commands"
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
	workspaceID, actorID, customerID := store.NewID("ws"), store.NewID("usr"), store.NewID("usr")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Service test')`, workspaceID)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Service admin')`, actorID, actorID+"@example.test")
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Portal customer')`, customerID, customerID+"@example.test")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'admin')`, workspaceID, actorID)
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'member')`, workspaceID, customerID)
	exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, actorID, store.HashToken(actorID))
	exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, customerID, store.HashToken(customerID))
	t.Cleanup(func() {
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM boards WHERE project_id IN (SELECT id FROM projects WHERE workspace_id=$1)`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM organization_audit_events WHERE actor_id=$1`, actorID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		exec(`DELETE FROM api_tokens WHERE user_id IN ($1,$2)`, actorID, customerID)
		exec(`DELETE FROM users WHERE id IN ($1,$2) OR email='invited.customer@example.test'`, actorID, customerID)
	})
	handler := &Handler{Store: st, Commands: &commands.Service{Store: st}, WorkspaceSlug: workspaceID, BaseURL: "https://zzira.test"}
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
	if !strings.Contains(requestTypes.Body.String(), `"name":"Get IT help"`) || !strings.Contains(requestTypes.Body.String(), `"name":"Report an incident"`) {
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

	createdCustomer := call("POST", "/rest/servicedeskapi/customer", `{"email":"invited.customer@example.test","displayName":"Invited Customer"}`, 201)
	if !strings.Contains(createdCustomer.Body.String(), `"displayName":"Invited Customer"`) {
		t.Fatal(createdCustomer.Body.String())
	}
	var createdCustomerBean map[string]any
	if err := json.Unmarshal(createdCustomer.Body.Bytes(), &createdCustomerBean); err != nil {
		t.Fatal(err)
	}
	invitedCustomerID, _ := createdCustomerBean["accountId"].(string)
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
	allRequests := call("GET", "/rest/servicedeskapi/request?requestOwnership=ALL_REQUESTS", "", 200)
	if !strings.Contains(allRequests.Body.String(), issueKey) {
		t.Fatal(allRequests.Body.String())
	}
}
