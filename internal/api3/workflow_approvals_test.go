package api3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
	"github.com/e6qu/zzira/internal/workflow"
)

// TestWorkflowStatusApprovals covers a workflow status's Jira Service
// Management approval configuration: its validation and workflow bean, the
// approval a request entering the status opens from the approvers field less
// the excluded reporter, the number of approvals it needs, and the transitions
// that run once it is approved or declined.
func TestWorkflowStatusApprovals(t *testing.T) {
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
	workspaceID := store.NewID("ws")
	adminID, approverID, customerID := store.NewID("usr"), store.NewID("usr"), store.NewID("usr")
	projectKey := fmt.Sprintf("WA%05d", time.Now().UnixNano()%100000)
	fieldNumber, err := st.NextCustomFieldNumber(ctx)
	if err != nil {
		t.Fatal(err)
	}
	fieldID := fmt.Sprintf("customfield_%d", fieldNumber)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Workflow approvals')`, workspaceID)
	people := []struct{ id, role, name string }{{adminID, "admin", "Approvals admin"}, {approverID, "member", "Second approver"}, {customerID, "member", "Requesting customer"}}
	for _, person := range people {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test',$3)`, person.id, person.id+"@example.test", person.name)
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,$3)`, workspaceID, person.id, person.role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, person.id, store.HashToken(person.id))
	}
	exec(`INSERT INTO custom_fields(id,name,type,description,workspace_id) VALUES($1,'Approvers','multiuserpicker','Request approvers',$2)`, fieldID, workspaceID)
	t.Cleanup(func() {
		exec(`DELETE FROM notifications WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM service_desks WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM role_bindings WHERE scope_type='project' AND scope_id IN (SELECT id FROM projects WHERE workspace_id=$1)`, workspaceID)
		exec(`DELETE FROM boards WHERE project_id IN (SELECT id FROM projects WHERE workspace_id=$1)`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workflow_schemes WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workflows WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM custom_fields WHERE id=$1`, fieldID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		for _, person := range people {
			exec(`DELETE FROM api_tokens WHERE user_id=$1`, person.id)
			exec(`DELETE FROM users WHERE id=$1`, person.id)
		}
	})
	h := &Handler{Store: st, Commands: &commands.Service{Store: st}, WorkspaceSlug: workspaceID, BaseURL: "https://zzira.test"}
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

	// Two approvals are needed from the approvers field, the reporter never
	// approves their own request, and the decision moves the request on.
	approval := workflow.ApprovalConfiguration{Active: "true", ConditionType: "number", ConditionValue: "2", Exclude: []string{"reporter"}, FieldID: fieldID, TransitionApproved: "21", TransitionRejected: "31"}
	approvals := workflow.Workflow{ID: store.NewID("workflow"), Name: "Approvals " + projectKey, Transitions: []workflow.Transition{
		{ID: "11", Name: "Request approval", From: []string{"st_todo"}, To: "st_inprogress"},
		{ID: "21", Name: "Approve", From: []string{"st_inprogress"}, To: "st_done"},
		{ID: "31", Name: "Decline", From: []string{"st_inprogress"}, To: "st_todo"},
	}, Statuses: []workflow.StatusLayout{
		{StatusReference: "st_todo", Properties: map[string]string{}},
		{StatusReference: "st_inprogress", Properties: map[string]string{}, ApprovalConfiguration: &approval},
		{StatusReference: "st_done", Properties: map[string]string{}},
	}}
	refused := func(change func(*workflow.ApprovalConfiguration)) {
		t.Helper()
		broken := approval
		change(&broken)
		invalid := approvals
		invalid.ID, invalid.Name = store.NewID("workflow"), "Invalid "+store.NewID("name")
		invalid.Statuses = []workflow.StatusLayout{{StatusReference: "st_inprogress", Properties: map[string]string{}, ApprovalConfiguration: &broken}}
		if err := st.CreateWorkflow(ctx, workspaceID, invalid); err == nil {
			t.Fatalf("an invalid approval configuration was saved: %+v", broken)
		}
	}
	refused(func(c *workflow.ApprovalConfiguration) { c.ConditionValue = "21" })
	refused(func(c *workflow.ApprovalConfiguration) { c.ConditionType = "everyone" })
	refused(func(c *workflow.ApprovalConfiguration) { c.Exclude = []string{"watcher"} })
	refused(func(c *workflow.ApprovalConfiguration) { c.TransitionApproved = "11" })
	// The workflow API reports an invalid configuration against the request's
	// status reference.
	statuses := `"statuses":[{"id":"st_todo","name":"To Do","statusCategory":"TODO","statusReference":"todo"},{"id":"st_inprogress","name":"In Progress","statusCategory":"IN_PROGRESS","statusReference":"progress"},{"id":"st_done","name":"Done","statusCategory":"DONE","statusReference":"done"}]`
	invalidApproval := `{"active":"true","conditionType":"percent","conditionValue":"150","fieldId":"` + fieldID + `","transitionApproved":"21","transitionRejected":"31"}`
	payload := `{"scope":{"type":"GLOBAL"},` + statuses + `,"workflows":[{"name":"Invalid approvals ` + projectKey + `","description":"","statuses":[{"statusReference":"todo","properties":{}},{"statusReference":"progress","properties":{},"approvalConfiguration":` + invalidApproval + `},{"statusReference":"done","properties":{}}],"transitions":[` +
		`{"id":"1","name":"Create","type":"INITIAL","toStatusReference":"todo","links":[]},` +
		`{"id":"11","name":"Request approval","type":"DIRECTED","toStatusReference":"progress","links":[{"fromStatusReference":"todo"}]},` +
		`{"id":"21","name":"Approve","type":"DIRECTED","toStatusReference":"done","links":[{"fromStatusReference":"progress"}]},` +
		`{"id":"31","name":"Decline","type":"DIRECTED","toStatusReference":"todo","links":[{"fromStatusReference":"progress"}]}]}]}`
	validation := callAs(adminID, http.MethodPost, "/rest/api/3/workflows/create/validation", `{"payload":`+payload+`,"validationOptions":{"levels":["ERROR"]}}`, http.StatusOK)
	if !strings.Contains(validation, "STATUS_APPROVAL_CONFIGURATION_INVALID") || !strings.Contains(validation, `"statusReference":"progress"`) || strings.Contains(validation, "st_inprogress") {
		t.Fatalf("approval configuration validation = %s", validation)
	}
	if err = st.CreateWorkflow(ctx, workspaceID, approvals); err != nil {
		t.Fatal(err)
	}
	if found := callAs(adminID, http.MethodGet, "/rest/api/3/workflows/search?queryString=Approvals%20"+projectKey, "", http.StatusOK); !strings.Contains(found, `"approvalConfiguration":{"active":"true","conditionType":"number","conditionValue":"2","exclude":["reporter"],"fieldId":"`+fieldID+`","transitionApproved":"21","transitionRejected":"31"}`) {
		t.Fatalf("workflow statuses = %s", found)
	}

	project := map[string]any{}
	if err = json.Unmarshal([]byte(callAs(adminID, http.MethodPost, "/rest/api/3/project", `{"key":"`+projectKey+`","name":"Approvals `+projectKey+`","projectTypeKey":"service_desk","projectTemplateKey":"com.atlassian.servicedesk:simplified-it-service-management","leadAccountId":"`+adminID+`"}`, http.StatusCreated)), &project); err != nil {
		t.Fatal(err)
	}
	scheme := map[string]any{}
	if err = json.Unmarshal([]byte(callAs(adminID, http.MethodPost, "/rest/api/3/workflowscheme", `{"name":"Approvals scheme `+projectKey+`","defaultWorkflow":"`+approvals.Name+`"}`, http.StatusCreated)), &scheme); err != nil {
		t.Fatal(err)
	}
	callAs(adminID, http.MethodPut, "/rest/api/3/workflowscheme/project", fmt.Sprintf(`{"projectId":"%v","workflowSchemeId":"%v"}`, project["id"], scheme["id"]), http.StatusNoContent)
	var serviceDeskID, requestTypeID string
	if err = st.Pool.QueryRow(ctx, `SELECT sd.id FROM service_desks sd JOIN projects p ON p.id=sd.project_id WHERE p.workspace_id=$1 AND p.key=$2`, workspaceID, projectKey).Scan(&serviceDeskID); err != nil {
		t.Fatal(err)
	}
	if err = st.Pool.QueryRow(ctx, `SELECT id FROM service_request_types WHERE service_desk_id=$1 ORDER BY id::bigint LIMIT 1`, serviceDeskID).Scan(&requestTypeID); err != nil {
		t.Fatal(err)
	}
	statusOf := func(key string) string {
		t.Helper()
		issue, err := st.IssueByIDOrKey(ctx, workspaceID, key)
		if err != nil {
			t.Fatal(err)
		}
		return issue.Status.ID
	}
	// raise creates a request whose approvers field names everyone, then asks
	// for approval, returning the request key and the opened approval.
	raise := func(summary string) (string, string) {
		t.Helper()
		created := map[string]any{}
		if err := json.Unmarshal([]byte(callAs(customerID, http.MethodPost, "/rest/servicedeskapi/request", `{"serviceDeskId":"`+serviceDeskID+`","requestTypeId":"`+requestTypeID+`","requestFieldValues":{"summary":"`+summary+`"}}`, http.StatusCreated)), &created); err != nil {
			t.Fatal(err)
		}
		key := fmt.Sprint(created["issueKey"])
		issue, err := st.IssueByIDOrKey(ctx, workspaceID, key)
		if err != nil {
			t.Fatal(err)
		}
		approvers, _ := json.Marshal([]string{adminID, approverID, customerID})
		if _, _, err = st.UpdateIssue(ctx, adminID, workspaceID, issue.ID, store.IssueUpdate{Fields: map[string]json.RawMessage{fieldID: approvers}}); err != nil {
			t.Fatal(err)
		}
		callAs(adminID, http.MethodPost, "/rest/api/3/issue/"+key+"/transitions", `{"transition":{"id":"11"}}`, http.StatusNoContent)
		var page struct {
			Values []struct {
				ID        string `json:"id"`
				Name      string `json:"name"`
				Approvers []struct {
					Approver struct {
						AccountID string `json:"accountId"`
					} `json:"approver"`
				} `json:"approvers"`
			} `json:"values"`
		}
		if err := json.Unmarshal([]byte(callAs(adminID, http.MethodGet, "/rest/servicedeskapi/request/"+key+"/approval", "", http.StatusOK)), &page); err != nil || len(page.Values) != 1 {
			t.Fatalf("approvals on %s = %+v err=%v", key, page, err)
		}
		opened := page.Values[0]
		if len(opened.Approvers) != 2 {
			t.Fatalf("approvers = %+v; the reporter is excluded", opened.Approvers)
		}
		for _, approver := range opened.Approvers {
			if approver.Approver.AccountID == customerID {
				t.Fatal("the reporter approves their own request")
			}
		}
		return key, opened.ID
	}

	approvedKey, approvedID := raise("New laptop")
	decide := func(accountID, key, approvalID, decision string) {
		t.Helper()
		callAs(accountID, http.MethodPost, "/rest/servicedeskapi/request/"+key+"/approval/"+approvalID, `{"decision":"`+decision+`"}`, http.StatusOK)
	}
	decide(adminID, approvedKey, approvedID, "approve")
	if status := statusOf(approvedKey); status != "st_inprogress" {
		t.Fatalf("one of two approvals moved the request to %s", status)
	}
	decide(approverID, approvedKey, approvedID, "approve")
	if status := statusOf(approvedKey); status != "st_done" {
		t.Fatalf("the approved request is in %s, not its approved transition's status", status)
	}

	declinedKey, declinedID := raise("New server")
	decide(approverID, declinedKey, declinedID, "decline")
	if status := statusOf(declinedKey); status != "st_todo" {
		t.Fatalf("the declined request is in %s, not its declined transition's status", status)
	}
}
