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
	"github.com/e6qu/zzira/internal/workflow"
)

func TestDevelopmentInformationDrivesIssuePanelAndWorkflowTrigger(t *testing.T) {
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
	workspaceID, actorID := store.NewID("ws"), store.NewID("usr")
	projectID, workflowID := store.NewID("project"), store.NewID("workflow")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Development test')`, workspaceID)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Development user')`, actorID, actorID+"@example.test")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'admin')`, workspaceID, actorID)
	exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, actorID, store.HashToken(actorID))
	exec(`INSERT INTO projects(id,workspace_id,key,name) VALUES($1,$2,'DEV','Development project')`, projectID, workspaceID)
	t.Cleanup(func() {
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workflow_schemes WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workflows WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM organization_audit_events WHERE actor_id=$1`, actorID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		exec(`DELETE FROM api_tokens WHERE user_id=$1`, actorID)
		exec(`DELETE FROM users WHERE id=$1`, actorID)
	})

	wf := workflow.Workflow{ID: workflowID, Name: "Branch automation", Transitions: []workflow.Transition{
		{ID: "review", Name: "Ready for review", From: []string{"st_todo"}, To: "st_inprogress", Triggers: []workflow.Rule{{ID: "branch", RuleKey: workflow.RuleDevelopmentTrigger, Parameters: map[string]string{"triggerType": workflow.DevelopmentBranchCreated}}}},
		{ID: "done", Name: "Done", From: []string{"st_inprogress"}, To: "st_done", Triggers: []workflow.Rule{{ID: "branch-2", RuleKey: workflow.RuleDevelopmentTrigger, Parameters: map[string]string{"triggerType": workflow.DevelopmentBranchCreated}}}},
	}}
	if err := st.CreateWorkflow(ctx, workspaceID, wf); err != nil {
		t.Fatal(err)
	}
	if err := st.AssignWorkflowToProject(ctx, workspaceID, projectID, workflowID); err != nil {
		t.Fatal(err)
	}
	service := &commands.Service{Store: st}
	first, _, err := service.CreateIssue(ctx, commands.CreateIssueInput{ActorID: actorID, WorkspaceID: workspaceID, ProjectIDOrKey: projectID, Summary: "Open a branch", IssueTypeID: "it_task"})
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := service.CreateIssue(ctx, commands.CreateIssueInput{ActorID: actorID, WorkspaceID: workspaceID, ProjectIDOrKey: projectID, Summary: "Do not move", IssueTypeID: "it_task"})
	if err != nil {
		t.Fatal(err)
	}
	handler := &Handler{Store: st, Commands: service, WorkspaceSlug: workspaceID, BaseURL: "https://zzira.test"}
	call := func(method, path, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.SetBasicAuth(actorID+"@example.test", actorID)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s: %d want %d: %s", method, path, response.Code, want, response.Body.String())
		}
		return response
	}
	bulk := `{"properties":{"accountId":"acct-1","projectId":"delivery"},"repositories":[{"id":"repo-1","name":"zzira","url":"https://git.example/zzira","updateSequenceId":2,"branches":[{"id":"branch-1","name":"feature/` + first.Key + `-devinfo","url":"https://git.example/zzira/branches/1","updateSequenceId":2,"issueKeys":["` + first.Key + `"]}],"commits":[{"id":"commit-1","displayId":"abc123","message":"Implement ` + first.Key + `","url":"https://git.example/zzira/commits/1","updateSequenceId":2,"authorTimestamp":"2026-09-06T10:00:00Z","associations":[{"associationType":"issueIdOrKeys","values":["` + first.ID + `"]}]}],"pullRequests":[{"id":"pr-1","title":"Ship ` + first.Key + `","url":"https://git.example/zzira/pulls/1","status":"OPEN","updateSequenceId":2,"lastUpdate":"2026-09-06T11:00:00Z","issueKeys":["` + first.Key + `"]}]}]}`
	response := call("POST", "/rest/devinfo/0.10/bulk", bulk, 202)
	if !strings.Contains(response.Body.String(), `"unknownIssueKeys":[]`) {
		t.Fatal(response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"acceptedDevinfoEntities":{"repo-1":{"branches":["branch-1"],"commits":["commit-1"],"pullRequests":["pr-1"]}}`) {
		t.Fatal(response.Body.String())
	}
	moved, err := st.IssueByIDOrKey(ctx, workspaceID, first.Key)
	if err != nil || moved.Status.ID != "st_inprogress" {
		t.Fatalf("branch-triggered issue = %+v, %v", moved, err)
	}
	items, err := st.DevelopmentItemsForIssue(ctx, workspaceID, first.Key)
	if err != nil || len(items) != 3 {
		t.Fatalf("development items = %+v, %v", items, err)
	}
	if repository := call("GET", "/rest/devinfo/0.10/repository/repo-1", "", 200); !strings.Contains(repository.Body.String(), `"name":"zzira"`) {
		t.Fatal(repository.Body.String())
	}

	stale := strings.ReplaceAll(strings.ReplaceAll(bulk, `"updateSequenceId":2`, `"updateSequenceId":1`), `"name":"zzira"`, `"name":"stale"`)
	call("POST", "/rest/devinfo/0.10/bulk", stale, 202)
	items, err = st.DevelopmentItemsForIssue(ctx, workspaceID, first.Key)
	if err != nil || len(items) != 3 || items[0].RepositoryName != "zzira" {
		t.Fatalf("stale delivery changed data: %+v, %v", items, err)
	}
	branchUpdate := `{"properties":{"accountId":"acct-1","projectId":"delivery"},"repositories":[{"id":"repo-1","name":"zzira","url":"https://git.example/zzira","updateSequenceId":3,"branches":[{"id":"branch-1","name":"feature/` + first.Key + `-renamed","url":"https://git.example/zzira/branches/1","updateSequenceId":3,"issueKeys":["` + first.Key + `"]}]}]}`
	call("POST", "/rest/devinfo/0.10/bulk", branchUpdate, 202)
	updatedBranchIssue, err := st.IssueByIDOrKey(ctx, workspaceID, first.Key)
	if err != nil || updatedBranchIssue.Status.ID != "st_inprogress" {
		t.Fatalf("branch update fired create trigger: %+v, %v", updatedBranchIssue, err)
	}

	prevented := `{"preventTransitions":true,"properties":{"accountId":"acct-1","projectId":"delivery"},"repositories":[{"id":"repo-1","name":"zzira","url":"https://git.example/zzira","updateSequenceId":4,"branches":[{"id":"branch-2","name":"feature/` + second.Key + `","url":"https://git.example/zzira/branches/2","updateSequenceId":4,"issueKeys":["` + second.Key + `"]}]}]}`
	call("POST", "/rest/devinfo/0.10/bulk", prevented, 202)
	unmoved, err := st.IssueByIDOrKey(ctx, workspaceID, second.Key)
	if err != nil || unmoved.Status.ID != "st_todo" {
		t.Fatalf("preventTransitions issue = %+v, %v", unmoved, err)
	}
	call("DELETE", "/rest/devinfo/0.10/repository/repo-1/branch/branch-2?_updateSequenceId=3", "", 202)
	if got, err := st.DevelopmentItemsForIssue(ctx, workspaceID, second.Key); err != nil || len(got) != 1 {
		t.Fatalf("stale branch delete = %+v, %v", got, err)
	}
	call("DELETE", "/rest/devinfo/0.10/repository/repo-1/branch/branch-2?_updateSequenceId=5", "", 202)
	if got, err := st.DevelopmentItemsForIssue(ctx, workspaceID, second.Key); err != nil || len(got) != 0 {
		t.Fatalf("deleted branch = %+v, %v", got, err)
	}

	var cloudID string
	if err := st.Pool.QueryRow(ctx, `SELECT cloud_id::text FROM workspaces WHERE id=$1`, workspaceID).Scan(&cloudID); err != nil {
		t.Fatal(err)
	}
	call("GET", "/jira/devinfo/0.1/cloud/"+cloudID+"/repository/repo-1", "", 200)
	call("GET", "/jira/devinfo/0.1/cloud/not-this-site/repository/repo-1", "", 404)
	if response := call("GET", "/rest/devinfo/0.10/existsByProperties?accountId=acct-1&projectId=delivery&_updateSequenceId=4", "", 200); !strings.Contains(response.Body.String(), `"hasDataMatchingProperties":true`) {
		t.Fatal(response.Body.String())
	}
	call("DELETE", "/rest/devinfo/0.10/repository/repo-1?_updateSequenceId=3", "", 202)
	call("GET", "/rest/devinfo/0.10/repository/repo-1", "", 200)
	call("DELETE", "/rest/devinfo/0.10/bulkByProperties?accountId=acct-1&projectId=delivery", "", 202)
	call("GET", "/rest/devinfo/0.10/repository/repo-1", "", 404)
}

func TestDevelopmentWorkflowTriggerWireValidation(t *testing.T) {
	transition := workflow.Transition{ID: "review", Name: "Review", From: []string{"st_todo"}, To: "st_done", Triggers: []workflow.Rule{{
		ID: "branch", RuleKey: workflow.RuleDevelopmentTrigger, Parameters: map[string]string{"triggerType": workflow.DevelopmentBranchCreated},
	}}}
	if err := workflow.ValidateTransitionRules(transition); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(workflowTransitionBean(transition))
	if err != nil || !strings.Contains(string(encoded), `"ruleKey":"system:development-triggers"`) {
		t.Fatalf("transition wire = %s, %v", encoded, err)
	}
	capabilities, _ := json.Marshal(workflowCapabilitiesResponse("GLOBAL"))
	if !strings.Contains(string(capabilities), workflow.DevelopmentBranchCreated) {
		t.Fatal(string(capabilities))
	}
	wf, validationErrors := workflowDefinitionFromRequest("wf", "Development workflow", "", nil, nil,
		[]workflowStatusLayoutRequest{{StatusReference: "todo"}, {StatusReference: "done"}},
		[]workflowTransitionUpdateRequest{{
			ID: "review", Name: "Review", ToStatusReference: "done",
			Links:    []workflowTransitionLinkRequest{{FromStatusReference: "todo"}},
			Triggers: []workflowRuleUpdateRequest{{RuleKey: workflow.RuleDevelopmentTrigger, Parameters: map[string]string{"triggerType": workflow.DevelopmentBranchCreated}}},
		}}, map[string]string{"todo": "st_todo", "done": "st_done"})
	if len(validationErrors) != 0 || len(wf.Transitions) != 1 || len(wf.Transitions[0].Triggers) != 1 || wf.Transitions[0].Triggers[0].ID == "" {
		t.Fatalf("modern workflow trigger = %+v, errors = %+v", wf, validationErrors)
	}
}
