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
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

// TestWorkflowApprovalReminderAndAgentRules covers the remaining system rules
// Jira documents: approval conditions follow the work item's approvals, a
// reminder screen accepts its fields, a trigger-agent post function records an
// agent run request, and new transitions get Jira's numeric ids.
func TestWorkflowApprovalReminderAndAgentRules(t *testing.T) {
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
	workspaceID, adminID, agentID := store.NewID("ws"), store.NewID("usr"), "app_principal_"+store.NewID("agent")
	projectKey := fmt.Sprintf("WR%06d", time.Now().UnixNano()%1000000)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Workflow rules')`, workspaceID)
	for _, identity := range []struct{ id, role, name string }{{adminID, "admin", "Rules Admin"}, {agentID, "member", "Release agent"}} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test',$3)`, identity.id, identity.id+"@example.test", identity.name)
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,$3)`, workspaceID, identity.id, identity.role)
	}
	exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, adminID, store.HashToken(adminID))
	t.Cleanup(func() {
		exec(`DELETE FROM workflow_agent_runs WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM service_requests WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM boards WHERE project_id IN (SELECT id FROM projects WHERE workspace_id=$1)`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workflow_schemes WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workflows WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		exec(`DELETE FROM api_tokens WHERE user_id=$1`, adminID)
		for _, id := range []string{adminID, agentID} {
			exec(`DELETE FROM users WHERE id=$1`, id)
		}
	})
	service := &commands.Service{Store: st}
	h := &Handler{Store: st, Commands: service, WorkspaceSlug: workspaceID, BaseURL: "https://zzira.test"}
	call := func(method, path, body string, want int) string {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.SetBasicAuth(adminID+"@example.test", adminID)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, response.Code, want, response.Body.String())
		}
		return response.Body.String()
	}

	// A workflow whose Approve transition waits for approval, reminds people to
	// update the assignee and requests an agent run; its Close transition has
	// no id and is numbered.
	workflowName := "Rules " + projectKey
	create := `{"scope":{"type":"GLOBAL"},"statuses":[{"id":"st_todo","name":"To Do","statusCategory":"TODO","statusReference":"todo"},{"id":"st_done","name":"Done","statusCategory":"DONE","statusReference":"done"}],
		"workflows":[{"name":"` + workflowName + `","description":"","statuses":[{"statusReference":"todo","properties":{}},{"statusReference":"done","properties":{}}],
		"transitions":[
		  {"id":"11","name":"Approve","type":"DIRECTED","toStatusReference":"done","links":[{"fromStatusReference":"todo"}],
		   "conditions":{"operation":"ALL","conditions":[{"ruleKey":"system:jsd-approvals-block-until-approved","parameters":{"approvalConfigurationJson":"{\"statusExternalUuid\":\"done\"}"}}],"conditionGroups":[]},
		   "transitionScreen":{"ruleKey":"system:remind-people-to-update-fields","parameters":{"remindingFieldIds":"assignee","remindingMessage":"Confirm the owner","remindingAlwaysAsk":"true"}},
		   "actions":[{"ruleKey":"system:trigger-agent","parameters":{"agentId":"` + agentID + `","promptValue":"Summarize the approval"}}]},
		  {"name":"Reopen","type":"DIRECTED","toStatusReference":"todo","links":[{"fromStatusReference":"done"}]}]}]}`
	call(http.MethodPost, "/rest/api/3/workflows/create", create, http.StatusOK)
	var search struct {
		Values []struct {
			ID          string `json:"id"`
			Transitions []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"transitions"`
		} `json:"values"`
	}
	if err = json.Unmarshal([]byte(call(http.MethodGet, "/rest/api/3/workflows/search?queryString="+projectKey+"&expand=values.transitions", "", http.StatusOK)), &search); err != nil || len(search.Values) != 1 {
		t.Fatalf("workflow search = %+v err=%v", search, err)
	}
	for _, transition := range search.Values[0].Transitions {
		if transition.Name == "Reopen" && transition.ID != "21" {
			t.Fatalf("new transition id = %q", transition.ID)
		}
	}
	capabilities := call(http.MethodGet, "/rest/api/3/workflows/capabilities?workflowId="+search.Values[0].ID, "", http.StatusOK)
	for _, key := range []string{"system:block-in-progress-approval", "system:jsd-approvals-block-until-approved", "system:jsd-approvals-block-until-rejected", "system:remind-people-to-update-fields", "system:trigger-agent"} {
		if !strings.Contains(capabilities, `"ruleKey":"`+key+`"`) {
			t.Fatalf("capabilities omit %s", key)
		}
	}

	// Approvals belong to service requests, so the work item is raised in a
	// service management project.
	project := map[string]any{}
	if err = json.Unmarshal([]byte(call(http.MethodPost, "/rest/api/3/project", `{"key":"`+projectKey+`","name":"Rules `+projectKey+`","projectTypeKey":"service_desk","projectTemplateKey":"com.atlassian.servicedesk:simplified-it-service-management","leadAccountId":"`+adminID+`","assigneeType":"PROJECT_LEAD"}`, http.StatusCreated)), &project); err != nil {
		t.Fatal(err)
	}
	scheme := map[string]any{}
	if err = json.Unmarshal([]byte(call(http.MethodPost, "/rest/api/3/workflowscheme", `{"name":"Rules scheme `+projectKey+`","defaultWorkflow":"`+workflowName+`"}`, http.StatusCreated)), &scheme); err != nil {
		t.Fatal(err)
	}
	call(http.MethodPut, "/rest/api/3/workflowscheme/project", fmt.Sprintf(`{"projectId":"%v","workflowSchemeId":"%v"}`, project["id"], scheme["id"]), http.StatusNoContent)
	firstID := func(body string) string {
		t.Helper()
		var page struct {
			Values []struct {
				ID any `json:"id"`
			} `json:"values"`
		}
		if decodeErr := json.Unmarshal([]byte(body), &page); decodeErr != nil || len(page.Values) == 0 {
			t.Fatalf("page = %s err=%v", body, decodeErr)
		}
		return fmt.Sprint(page.Values[0].ID)
	}
	serviceDeskID := firstID(call(http.MethodGet, "/rest/servicedeskapi/servicedesk", "", http.StatusOK))
	requestTypeID := firstID(call(http.MethodGet, "/rest/servicedeskapi/servicedesk/"+serviceDeskID+"/requesttype", "", http.StatusOK))
	request := map[string]any{}
	if err = json.Unmarshal([]byte(call(http.MethodPost, "/rest/servicedeskapi/request", `{"serviceDeskId":"`+serviceDeskID+`","requestTypeId":"`+requestTypeID+`","requestFieldValues":{"summary":"Needs approval"}}`, http.StatusCreated)), &request); err != nil {
		t.Fatal(err)
	}
	issueKey, _ := request["issueKey"].(string)
	if issueKey == "" {
		t.Fatalf("service request = %v", request)
	}
	stored, err := st.IssueByIDOrKey(ctx, workspaceID, issueKey)
	if err != nil {
		t.Fatal(err)
	}
	available := func() bool {
		t.Helper()
		return strings.Contains(call(http.MethodGet, "/rest/api/3/issue/"+issueKey+"/transitions", "", http.StatusOK), `"name":"Approve"`)
	}

	// Without an approved approval the transition is hidden and refused.
	if available() {
		t.Fatal("the Approve transition is offered before any approval")
	}
	call(http.MethodPost, "/rest/api/3/issue/"+issueKey+"/transitions", `{"transition":{"id":"11"}}`, http.StatusBadRequest)
	approval, _, err := st.CreateServiceApproval(ctx, workspaceID, stored.ID, adminID, "Release sign-off", []string{adminID}, "")
	if err != nil {
		t.Fatal(err)
	}
	if available() {
		t.Fatal("the Approve transition is offered while the approval is pending")
	}
	if _, err = st.AnswerServiceApproval(ctx, stored.ID, approval.ID, adminID, "approved"); err != nil {
		t.Fatal(err)
	}
	if !available() {
		t.Fatal("the Approve transition is not offered after approval")
	}

	// The reminder's field is a transition input and the agent run is recorded.
	call(http.MethodPost, "/rest/api/3/issue/"+issueKey+"/transitions", `{"transition":{"id":"11"},"fields":{"assignee":{"accountId":"`+adminID+`"}}}`, http.StatusNoContent)
	var agent, prompt string
	if err = st.Pool.QueryRow(ctx, `SELECT agent_id, prompt FROM workflow_agent_runs WHERE workspace_id=$1 AND issue_id=$2`, workspaceID, stored.ID).Scan(&agent, &prompt); err != nil || agent != agentID || prompt != "Summarize the approval" {
		t.Fatalf("agent run = %q %q err=%v", agent, prompt, err)
	}

	// A person's account is not an agent.
	if _, _, err = st.UpdateIssue(ctx, adminID, workspaceID, stored.ID, store.IssueUpdate{TriggeredAgents: []models.WorkflowAgentTrigger{{AgentID: adminID}}}); err == nil {
		t.Fatal("an agent run was requested for a person's account")
	}
}
