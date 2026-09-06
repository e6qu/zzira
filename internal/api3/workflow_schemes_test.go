package api3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
	"github.com/e6qu/zzira/internal/workflow"
)

func TestWorkflowSchemeAPILifecycleAndAssignment(t *testing.T) {
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
	ws, actor, member, projectID := store.NewID("ws"), store.NewID("usr"), store.NewID("usr"), store.NewID("project")
	issueID, workflowID := store.NewID("issue"), store.NewID("workflow")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Workflow scheme API')`, ws)
	for _, user := range []string{actor, member} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Scheme user')`, user, user+"@example.test")
		role := "member"
		if user == actor {
			role = "admin"
		}
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,$3)`, ws, user, role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, user, store.HashToken(user))
	}
	exec(`INSERT INTO projects(id,workspace_id,key,name,workflow_id) VALUES($1,$2,'WSA','Scheme API project','wf_default')`, projectID, ws)
	simpleWorkflow := workflow.Workflow{ID: workflowID, Name: "Simple API lifecycle", Transitions: []workflow.Transition{
		{ID: store.NewID("transition"), Name: "Complete", From: []string{"st_todo"}, To: "st_done"},
		{ID: store.NewID("transition"), Name: "Reopen", From: []string{"st_done"}, To: "st_todo"},
	}}
	if err := st.CreateWorkflow(ctx, ws, simpleWorkflow); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO issues(id,workspace_id,project_id,key,summary,status_id,issuetype_id,updated_seq) VALUES($1,$2,$3,'WSA-1','Switch me','st_inprogress','it_task',0)`, issueID, ws, projectID)
	t.Cleanup(func() {
		exec(`DELETE FROM actions WHERE workspace_id=$1 AND entity_id=$2`, ws, issueID)
		exec(`DELETE FROM issues WHERE id=$1`, issueID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, ws)
		exec(`DELETE FROM workflow_schemes WHERE workspace_id=$1`, ws)
		exec(`DELETE FROM workflows WHERE id=$1`, workflowID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, ws)
		exec(`DELETE FROM workspaces WHERE id=$1`, ws)
		for _, user := range []string{actor, member} {
			exec(`DELETE FROM api_tokens WHERE user_id=$1`, user)
			exec(`DELETE FROM users WHERE id=$1`, user)
		}
	})
	h := &Handler{Store: st, Commands: &commands.Service{Store: st}, WorkspaceSlug: ws, BaseURL: "https://zzira.test"}
	call := func(user, method, path, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.SetBasicAuth(user+"@example.test", user)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("%s %s: %d want %d: %s", method, path, rec.Code, want, rec.Body.String())
		}
		return rec
	}
	body := `{"name":"Delivery scheme","description":"Routes delivery","defaultWorkflow":"Default","issueTypeMappings":{"it_task":"Default"}}`
	call(member, "POST", "/rest/api/3/workflowscheme", body, 403)
	created := call(actor, "POST", "/rest/api/3/workflowscheme", body, 201)
	var scheme map[string]any
	if err := json.Unmarshal(created.Body.Bytes(), &scheme); err != nil {
		t.Fatal(err)
	}
	schemeID := scheme["id"].(string)
	call(actor, "GET", "/rest/api/3/workflowscheme", "", 200)
	call(actor, "GET", "/rest/api/3/workflowscheme/"+schemeID, "", 200)
	update := `{"name":"Delivery scheme updated","description":"Draft routing","defaultWorkflow":"Default","issueTypeMappings":{}}`
	draft := call(actor, "PUT", "/rest/api/3/workflowscheme/"+schemeID, update, 200)
	if !strings.Contains(draft.Body.String(), `"draft":true`) {
		t.Fatal(draft.Body.String())
	}
	call(actor, "POST", "/rest/api/3/workflowscheme/"+schemeID+"/createdraft", "", 409)
	call(actor, "GET", "/rest/api/3/workflowscheme/"+schemeID+"/draft", "", 200)
	defaultMapping := call(actor, "GET", "/rest/api/3/workflowscheme/"+schemeID+"/draft/default", "", 200)
	if !strings.Contains(defaultMapping.Body.String(), `"workflow":"Default"`) {
		t.Fatal(defaultMapping.Body.String())
	}
	call(actor, "PUT", "/rest/api/3/workflowscheme/"+schemeID+"/draft/default", `{"workflow":"Simple API lifecycle"}`, 200)
	call(actor, "DELETE", "/rest/api/3/workflowscheme/"+schemeID+"/draft/default", "", 200)
	call(actor, "PUT", "/rest/api/3/workflowscheme/"+schemeID+"/draft/default", `{"workflow":"Default"}`, 200)
	call(actor, "PUT", "/rest/api/3/workflowscheme/"+schemeID+"/draft/issuetype/it_task", `{"workflow":"Simple API lifecycle"}`, 200)
	issueTypeMapping := call(actor, "GET", "/rest/api/3/workflowscheme/"+schemeID+"/draft/issuetype/it_task", "", 200)
	if !strings.Contains(issueTypeMapping.Body.String(), `"workflow":"Simple API lifecycle"`) {
		t.Fatal(issueTypeMapping.Body.String())
	}
	workflowMapping := call(actor, "GET", "/rest/api/3/workflowscheme/"+schemeID+"/draft/workflow?workflowName=Simple%20API%20lifecycle", "", 200)
	if !strings.Contains(workflowMapping.Body.String(), `"it_task"`) {
		t.Fatal(workflowMapping.Body.String())
	}
	call(actor, "PUT", "/rest/api/3/workflowscheme/"+schemeID+"/draft/workflow?workflowName=Simple%20API%20lifecycle", `{"workflow":"Default","issueTypes":["it_task"]}`, 200)
	call(actor, "DELETE", "/rest/api/3/workflowscheme/"+schemeID+"/draft/issuetype/it_task", "", 200)
	call(actor, "GET", "/rest/api/3/workflowscheme/"+schemeID+"/draft/issuetype/it_task", "", 404)
	call(actor, "POST", "/rest/api/3/workflowscheme/"+schemeID+"/draft/publish?validateOnly=true", `{}`, 204)
	publishedTask := call(actor, "POST", "/rest/api/3/workflowscheme/"+schemeID+"/draft/publish", `{}`, 303)
	if !strings.Contains(publishedTask.Body.String(), `"status":"COMPLETE"`) {
		t.Fatal(publishedTask.Body.String())
	}
	call(actor, "GET", "/rest/api/3/workflowscheme/"+schemeID+"/draft", "", 404)
	call(actor, "POST", "/rest/api/3/workflowscheme/"+schemeID+"/createdraft", "", 201)
	call(actor, "DELETE", "/rest/api/3/workflowscheme/"+schemeID+"/draft", "", 204)
	call(actor, "GET", "/rest/api/3/workflowscheme/"+schemeID+"/default", "", 200)
	call(actor, "PUT", "/rest/api/3/workflowscheme/"+schemeID+"/default", `{"workflow":"Simple API lifecycle"}`, 200)
	call(actor, "DELETE", "/rest/api/3/workflowscheme/"+schemeID+"/default", "", 200)
	call(actor, "PUT", "/rest/api/3/workflowscheme/"+schemeID+"/issuetype/it_task", `{"workflow":"Simple API lifecycle"}`, 200)
	call(actor, "GET", "/rest/api/3/workflowscheme/"+schemeID+"/issuetype/it_task", "", 200)
	call(actor, "GET", "/rest/api/3/workflowscheme/"+schemeID+"/workflow?workflowName=Simple%20API%20lifecycle", "", 200)
	call(actor, "PUT", "/rest/api/3/workflowscheme/"+schemeID+"/workflow?workflowName=Simple%20API%20lifecycle", `{"workflow":"Default","issueTypes":["it_task"]}`, 200)
	call(actor, "DELETE", "/rest/api/3/workflowscheme/"+schemeID+"/issuetype/it_task", "", 200)
	call(actor, "PUT", "/rest/api/3/workflowscheme/"+schemeID+"/issuetype/it_task", `{"workflow":"Simple API lifecycle"}`, 200)
	call(actor, "DELETE", "/rest/api/3/workflowscheme/"+schemeID+"/workflow?workflowName=Simple%20API%20lifecycle", "", 204)
	usage := call(actor, "GET", "/rest/api/3/workflowscheme/"+schemeID+"/projectUsages?maxResults=1", "", 200)
	if !strings.Contains(usage.Body.String(), `"values":[]`) {
		t.Fatal(usage.Body.String())
	}
	call(actor, "PUT", "/rest/api/3/workflowscheme/project", `{"projectId":"`+projectID+`","workflowSchemeId":"`+schemeID+`"}`, 204)
	association := call(actor, "GET", "/rest/api/3/workflowscheme/project?projectId="+projectID, "", 200)
	if !strings.Contains(association.Body.String(), schemeID) {
		t.Fatal(association.Body.String())
	}
	usage = call(actor, "GET", "/rest/api/3/workflowscheme/"+schemeID+"/projectUsages?maxResults=1", "", 200)
	if !strings.Contains(usage.Body.String(), projectID) {
		t.Fatal(usage.Body.String())
	}
	call(actor, "POST", "/rest/api/3/workflowscheme/"+schemeID+"/createdraft", "", 201)
	call(actor, "PUT", "/rest/api/3/workflowscheme/"+schemeID+"/draft/default", `{"workflow":"Simple API lifecycle"}`, 200)
	publishMappings := `{"statusMappings":[{"issueTypeId":"it_task","statusId":"st_inprogress","newStatusId":"st_todo"}]}`
	call(actor, "POST", "/rest/api/3/workflowscheme/"+schemeID+"/draft/publish?validateOnly=true", publishMappings, 204)
	call(actor, "POST", "/rest/api/3/workflowscheme/"+schemeID+"/draft/publish", publishMappings, 303)
	var draftMigratedStatus string
	if err := st.Pool.QueryRow(ctx, `SELECT status_id FROM issues WHERE id=$1`, issueID).Scan(&draftMigratedStatus); err != nil || draftMigratedStatus != "st_todo" {
		t.Fatalf("draft migrated status=%q err=%v", draftMigratedStatus, err)
	}
	call(actor, "PUT", "/rest/api/3/workflowscheme/"+schemeID+"/default", `{"workflow":"Default"}`, 200)
	exec(`UPDATE issues SET status_id='st_inprogress' WHERE id=$1`, issueID)
	bulkRead := call(actor, "POST", "/rest/api/3/workflowscheme/read", `{"projectIds":["`+projectID+`"],"workflowSchemeIds":["`+schemeID+`"]}`, 200)
	var readSchemes []map[string]any
	if err := json.Unmarshal(bulkRead.Body.Bytes(), &readSchemes); err != nil || len(readSchemes) != 1 {
		t.Fatalf("bulk read = %s, %v", bulkRead.Body.String(), err)
	}
	version := int(readSchemes[0]["version"].(map[string]any)["versionNumber"].(float64))
	requiredMappings := call(actor, "POST", "/rest/api/3/workflowscheme/update/mappings", `{"id":"`+schemeID+`","defaultWorkflowId":"`+workflowID+`","workflowsForIssueTypes":[]}`, 200)
	if !strings.Contains(requiredMappings.Body.String(), `"st_inprogress"`) {
		t.Fatal(requiredMappings.Body.String())
	}
	unsafeBulkUpdate := `{"id":"` + schemeID + `","name":"Unsafe bulk","description":"Unsafe","defaultWorkflowId":"` + workflowID + `","version":{"versionNumber":` + fmt.Sprint(version) + `},"workflowsForIssueTypes":[]}`
	call(actor, "POST", "/rest/api/3/workflowscheme/update", unsafeBulkUpdate, 409)
	mappedBulkUpdate := `{"id":"` + schemeID + `","name":"Delivery migrated","description":"Bulk migrated","defaultWorkflowId":"` + workflowID + `","version":{"versionNumber":` + fmt.Sprint(version) + `},"workflowsForIssueTypes":[],"statusMappingsByIssueTypeOverride":[{"issueTypeId":"it_task","statusMappings":[{"oldStatusId":"st_inprogress","newStatusId":"st_todo"}]}]}`
	call(actor, "POST", "/rest/api/3/workflowscheme/update", mappedBulkUpdate, 303)
	var bulkMigratedStatus string
	if err := st.Pool.QueryRow(ctx, `SELECT status_id FROM issues WHERE id=$1`, issueID).Scan(&bulkMigratedStatus); err != nil || bulkMigratedStatus != "st_todo" {
		t.Fatalf("bulk migrated status=%q err=%v", bulkMigratedStatus, err)
	}
	safeBulkUpdate := `{"id":"` + schemeID + `","name":"Delivery bulk","description":"Bulk updated","defaultWorkflowId":"wf_default","version":{"versionNumber":` + fmt.Sprint(version+1) + `},"workflowsForIssueTypes":[]}`
	call(actor, "POST", "/rest/api/3/workflowscheme/update", safeBulkUpdate, 303)
	call(actor, "POST", "/rest/api/3/workflowscheme/update", safeBulkUpdate, 409)
	exec(`UPDATE issues SET status_id='st_inprogress' WHERE id=$1`, issueID)
	call(actor, "PUT", "/rest/api/3/workflowscheme/"+schemeID+"/default", `{"workflow":"Simple API lifecycle"}`, 409)
	switchable := call(actor, "POST", "/rest/api/3/workflowscheme", `{"name":"Switch target","defaultWorkflow":"Simple API lifecycle"}`, 201)
	if err := json.Unmarshal(switchable.Body.Bytes(), &scheme); err != nil {
		t.Fatal(err)
	}
	targetSchemeID := scheme["id"].(string)
	call(actor, "POST", "/rest/api/3/workflowscheme/project/switch", `{"projectId":"`+projectID+`","targetSchemeId":"`+targetSchemeID+`"}`, 409)
	switchResponse := call(actor, "POST", "/rest/api/3/workflowscheme/project/switch", `{"projectId":"`+projectID+`","targetSchemeId":"`+targetSchemeID+`","mappingsByIssueTypeOverride":[{"issueTypeId":"it_task","statusMappings":[{"oldStatusId":"st_inprogress","newStatusId":"st_todo"}]}]}`, 303)
	if switchResponse.Header().Get("Location") == "" || !strings.Contains(switchResponse.Body.String(), `"status":"COMPLETE"`) || !strings.Contains(switchResponse.Body.String(), `"progress":100`) {
		t.Fatalf("switch task = %s, location = %q", switchResponse.Body.String(), switchResponse.Header().Get("Location"))
	}
	var task map[string]any
	if err := json.Unmarshal(switchResponse.Body.Bytes(), &task); err != nil {
		t.Fatal(err)
	}
	taskPath := "/rest/api/3/task/" + task["id"].(string)
	polledTask := call(actor, "GET", taskPath, "", 200)
	if !strings.Contains(polledTask.Body.String(), targetSchemeID) {
		t.Fatal(polledTask.Body.String())
	}
	call(member, "GET", taskPath, "", 403)
	call(actor, "POST", taskPath+"/cancel", "", 400)
	call(actor, "GET", "/rest/api/3/task/task_missing", "", 404)
	var issueStatus, assignedScheme string
	if err := st.Pool.QueryRow(ctx, `SELECT i.status_id,p.workflow_scheme_id FROM issues i JOIN projects p ON p.id=i.project_id WHERE i.id=$1`, issueID).Scan(&issueStatus, &assignedScheme); err != nil || issueStatus != "st_todo" || assignedScheme != targetSchemeID {
		t.Fatalf("switch result status=%q scheme=%q err=%v", issueStatus, assignedScheme, err)
	}
	call(actor, "DELETE", "/rest/api/3/workflowscheme/"+targetSchemeID, "", 409)
	unused := call(actor, "POST", "/rest/api/3/workflowscheme", `{"name":"Unused scheme","defaultWorkflow":"Default"}`, 201)
	if err := json.Unmarshal(unused.Body.Bytes(), &scheme); err != nil {
		t.Fatal(err)
	}
	call(actor, "DELETE", "/rest/api/3/workflowscheme/"+scheme["id"].(string), "", 204)
}
