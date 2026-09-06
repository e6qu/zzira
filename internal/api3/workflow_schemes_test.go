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
	defaultEditor := call(member, "GET", "/rest/api/3/workflows/defaultEditor", "", 200)
	if defaultEditor.Body.String() != "{\"value\":\"NEW\"}\n" {
		t.Fatal(defaultEditor.Body.String())
	}
	call(member, "GET", "/rest/api/3/workflows/capabilities?workflowId="+workflowID, "", 403)
	capabilities := call(actor, "GET", "/rest/api/3/workflows/capabilities?workflowId="+workflowID, "", 200)
	if !strings.Contains(capabilities.Body.String(), `"editorScope":"GLOBAL"`) || !strings.Contains(capabilities.Body.String(), `"projectTypes":["software","business"]`) || !strings.Contains(capabilities.Body.String(), `"systemRules":[]`) {
		t.Fatal(capabilities.Body.String())
	}
	call(actor, "GET", "/rest/api/3/workflows/capabilities?projectId="+projectID+"&issueTypeId=it_task", "", 200)
	call(actor, "GET", "/rest/api/3/workflows/capabilities", "", 400)
	call(actor, "GET", "/rest/api/3/workflows/capabilities?workflowId="+workflowID+"&projectId="+projectID+"&issueTypeId=it_task", "", 400)
	call(actor, "GET", "/rest/api/3/workflows/capabilities?projectId="+projectID+"&issueTypeId=it_missing", "", 400)
	createValidationBody := `{"payload":{"scope":{"type":"GLOBAL"},"statuses":[{"id":"st_todo","name":"To Do","statusCategory":"TODO","statusReference":"todo"},{"id":"st_done","name":"Done","statusCategory":"DONE","statusReference":"done"}],"workflows":[{"name":"Validated workflow","statuses":[{"statusReference":"todo","properties":{}},{"statusReference":"done","properties":{}}],"transitions":[{"id":"1","name":"Complete","type":"DIRECTED","toStatusReference":"done","links":[{"fromStatusReference":"todo"}]}]}]}}`
	call(member, "POST", "/rest/api/3/workflows/create/validation", createValidationBody, 403)
	createValidation := call(actor, "POST", "/rest/api/3/workflows/create/validation", createValidationBody, 200)
	if createValidation.Body.String() != "{\"errors\":[]}\n" {
		t.Fatal(createValidation.Body.String())
	}
	badCreateValidation := call(actor, "POST", "/rest/api/3/workflows/create/validation", `{"payload":{"workflows":[{"name":"","statuses":[],"transitions":[]}]}}`, 200)
	if !strings.Contains(badCreateValidation.Body.String(), `"code":"WORKFLOW_NAME_INVALID"`) || !strings.Contains(badCreateValidation.Body.String(), `"level":"ERROR"`) {
		t.Fatal(badCreateValidation.Body.String())
	}
	warningOnlyValidation := call(actor, "POST", "/rest/api/3/workflows/create/validation", `{"payload":{"workflows":[{"name":"","statuses":[],"transitions":[]}]},"validationOptions":{"levels":["WARNING"]}}`, 200)
	if warningOnlyValidation.Body.String() != "{\"errors\":[]}\n" {
		t.Fatal(warningOnlyValidation.Body.String())
	}
	updateValidationBody := `{"payload":{"workflows":[{"id":"` + workflowID + `","version":{"id":"` + workflowID + `","versionNumber":1},"statuses":[{"statusReference":"st_todo","properties":{}},{"statusReference":"st_done","properties":{}}],"transitions":[{"id":"` + simpleWorkflow.Transitions[0].ID + `","name":"Complete","type":"DIRECTED","toStatusReference":"st_done","links":[{"fromStatusReference":"st_todo"}]},{"id":"` + simpleWorkflow.Transitions[1].ID + `","name":"Reopen","type":"DIRECTED","toStatusReference":"st_todo","links":[{"fromStatusReference":"st_done"}]}]}]}}`
	updateValidation := call(actor, "POST", "/rest/api/3/workflows/update/validation", updateValidationBody, 200)
	if updateValidation.Body.String() != "{\"errors\":[]}\n" {
		t.Fatal(updateValidation.Body.String())
	}
	staleUpdateValidation := strings.Replace(updateValidationBody, `"versionNumber":1`, `"versionNumber":99`, 1)
	staleValidation := call(actor, "POST", "/rest/api/3/workflows/update/validation", staleUpdateValidation, 200)
	if !strings.Contains(staleValidation.Body.String(), `"code":"WORKFLOW_VERSION_CONFLICT"`) {
		t.Fatal(staleValidation.Body.String())
	}
	call(actor, "POST", "/rest/api/3/workflows/create/validation", `{`, 400)
	modernCreateBody := `{"scope":{"type":"GLOBAL"},"statuses":[{"id":"st_todo","name":"To Do","statusCategory":"TODO","statusReference":"todo"},{"id":"st_done","name":"Done","statusCategory":"DONE","statusReference":"done"}],"workflows":[{"name":"Modern workflow","statuses":[{"statusReference":"todo","properties":{}},{"statusReference":"done","properties":{}}],"transitions":[{"id":"1","name":"Complete","type":"DIRECTED","toStatusReference":"done","links":[{"fromStatusReference":"todo"}]}]}]}`
	call(member, "POST", "/rest/api/3/workflows/create", modernCreateBody, 403)
	modernCreatedResponse := call(actor, "POST", "/rest/api/3/workflows/create", modernCreateBody, 200)
	var modernCreated map[string]any
	if err := json.Unmarshal(modernCreatedResponse.Body.Bytes(), &modernCreated); err != nil {
		t.Fatal(err)
	}
	modernWorkflows := modernCreated["workflows"].([]any)
	modernWorkflowID := modernWorkflows[0].(map[string]any)["id"].(string)
	if modernWorkflows[0].(map[string]any)["version"].(map[string]any)["versionNumber"].(float64) != 1 {
		t.Fatal(modernCreatedResponse.Body.String())
	}
	call(actor, "POST", "/rest/api/3/workflows/create", modernCreateBody, 409)
	atomicCreateBody := `{"statuses":[{"id":"st_todo","name":"To Do","statusCategory":"TODO","statusReference":"todo"},{"id":"st_done","name":"Done","statusCategory":"DONE","statusReference":"done"}],"workflows":[{"name":"Atomic candidate","statuses":[{"statusReference":"todo","properties":{}},{"statusReference":"done","properties":{}}],"transitions":[{"id":"1","name":"Complete","type":"DIRECTED","toStatusReference":"done","links":[{"fromStatusReference":"todo"}]}]},{"name":"Simple API lifecycle","statuses":[{"statusReference":"todo","properties":{}},{"statusReference":"done","properties":{}}],"transitions":[{"id":"1","name":"Complete","type":"DIRECTED","toStatusReference":"done","links":[{"fromStatusReference":"todo"}]}]}]}`
	call(actor, "POST", "/rest/api/3/workflows/create", atomicCreateBody, 409)
	atomicSearch := call(actor, "GET", "/rest/api/3/workflows/search?queryString=Atomic", "", 200)
	if !strings.Contains(atomicSearch.Body.String(), `"total":0`) {
		t.Fatal(atomicSearch.Body.String())
	}
	modernUpdateBody := `{"workflows":[{"id":"` + modernWorkflowID + `","version":{"id":"` + modernWorkflowID + `","versionNumber":1},"statuses":[{"statusReference":"st_todo","properties":{}},{"statusReference":"st_done","properties":{}}],"transitions":[{"id":"1","name":"Ship","type":"DIRECTED","toStatusReference":"st_done","links":[{"fromStatusReference":"st_todo"}]}]}]}`
	modernUpdatedResponse := call(actor, "POST", "/rest/api/3/workflows/update", modernUpdateBody, 200)
	if !strings.Contains(modernUpdatedResponse.Body.String(), `"name":"Ship"`) || !strings.Contains(modernUpdatedResponse.Body.String(), `"versionNumber":2`) || !strings.Contains(modernUpdatedResponse.Body.String(), `"taskId":null`) {
		t.Fatal(modernUpdatedResponse.Body.String())
	}
	call(actor, "POST", "/rest/api/3/workflows/update", modernUpdateBody, 409)
	atomicUpdateBody := `{"workflows":[{"id":"` + modernWorkflowID + `","version":{"id":"` + modernWorkflowID + `","versionNumber":2},"statuses":[{"statusReference":"st_todo","properties":{}},{"statusReference":"st_done","properties":{}}],"transitions":[{"id":"1","name":"Must roll back","type":"DIRECTED","toStatusReference":"st_done","links":[{"fromStatusReference":"st_todo"}]}]},{"id":"` + workflowID + `","version":{"id":"` + workflowID + `","versionNumber":99},"statuses":[{"statusReference":"st_todo","properties":{}},{"statusReference":"st_done","properties":{}}],"transitions":[{"id":"1","name":"Stale","type":"DIRECTED","toStatusReference":"st_done","links":[{"fromStatusReference":"st_todo"}]}]}]}`
	call(actor, "POST", "/rest/api/3/workflows/update", atomicUpdateBody, 409)
	modernAfterRollback, err := st.WorkflowByID(ctx, ws, modernWorkflowID)
	if err != nil || modernAfterRollback.Version != 2 || modernAfterRollback.Transitions[0].Name != "Ship" {
		t.Fatalf("workflow after atomic rollback=%+v err=%v", modernAfterRollback, err)
	}
	var modernWorkflowAudits int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM organization_audit_events WHERE actor_id=$1 AND target_id=$2 AND action IN ('workflow.created','workflow.updated')`, actor, modernWorkflowID).Scan(&modernWorkflowAudits); err != nil || modernWorkflowAudits != 2 {
		t.Fatalf("modern workflow audits=%d err=%v", modernWorkflowAudits, err)
	}
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
	projectUsage := call(actor, "GET", "/rest/api/3/workflow/"+workflowID+"/projectUsages?maxResults=1", "", 200)
	if !strings.Contains(projectUsage.Body.String(), projectID) {
		t.Fatal(projectUsage.Body.String())
	}
	schemeUsage := call(actor, "GET", "/rest/api/3/workflow/"+workflowID+"/workflowSchemes", "", 200)
	if !strings.Contains(schemeUsage.Body.String(), targetSchemeID) {
		t.Fatal(schemeUsage.Body.String())
	}
	issueTypeUsage := call(actor, "GET", "/rest/api/3/workflow/"+workflowID+"/project/"+projectID+"/issueTypeUsages", "", 200)
	if !strings.Contains(issueTypeUsage.Body.String(), `"it_task"`) {
		t.Fatal(issueTypeUsage.Body.String())
	}
	workflowSearch := call(actor, "GET", "/rest/api/3/workflows/search?queryString=Simple&projectId="+projectID+"&isActive=true&orderBy=name&expand=values.transitions", "", 200)
	if !strings.Contains(workflowSearch.Body.String(), `"id":"`+workflowID+`"`) || !strings.Contains(workflowSearch.Body.String(), `"toStatusReference":"st_done"`) || !strings.Contains(workflowSearch.Body.String(), `"statusCategory":"DONE"`) {
		t.Fatal(workflowSearch.Body.String())
	}
	call(member, "POST", "/rest/api/3/workflows/preview", `{"projectId":"`+projectID+`","workflowIds":["`+workflowID+`"]}`, 403)
	workflowPreview := call(actor, "POST", "/rest/api/3/workflows/preview", `{"projectId":"`+projectID+`","workflowIds":["`+workflowID+`"],"workflowNames":["Simple API lifecycle"],"issueTypeIds":["it_task"]}`, 200)
	if !strings.Contains(workflowPreview.Body.String(), `"id":"`+workflowID+`"`) || !strings.Contains(workflowPreview.Body.String(), `"issueTypes":["it_task"]`) || !strings.Contains(workflowPreview.Body.String(), `"rawName":"Done"`) || !strings.Contains(workflowPreview.Body.String(), `"toStatusReference":"st_done"`) {
		t.Fatal(workflowPreview.Body.String())
	}
	call(actor, "POST", "/rest/api/3/workflows/preview", `{"projectId":"`+projectID+`","workflowIds":["`+modernWorkflowID+`"]}`, 404)
	call(actor, "POST", "/rest/api/3/workflows/preview", `{"projectId":"`+projectID+`","issueTypeIds":["it_missing"]}`, 400)
	call(actor, "POST", "/rest/api/3/workflows/preview", `{"projectId":"`+projectID+`"}`, 400)
	workflowPage := call(actor, "GET", "/rest/api/3/workflows/search?maxResults=1", "", 200)
	if !strings.Contains(workflowPage.Body.String(), `"nextPage":"https://zzira.test/rest/api/3/workflows/search?maxResults=1\u0026startAt=1"`) {
		t.Fatal(workflowPage.Body.String())
	}
	projectScope := call(actor, "GET", "/rest/api/3/workflows/search?scope=PROJECT", "", 200)
	if !strings.Contains(projectScope.Body.String(), `"total":0`) {
		t.Fatal(projectScope.Body.String())
	}
	unsafeActiveWorkflowUpdate := `{"workflows":[{"id":"` + workflowID + `","version":{"id":"` + workflowID + `","versionNumber":1},"statuses":[{"statusReference":"st_done","properties":{}}],"transitions":[{"id":"1","name":"Stay done","type":"DIRECTED","toStatusReference":"st_done","links":[{"fromStatusReference":"st_done"}]}]}]}`
	call(actor, "POST", "/rest/api/3/workflows/update", unsafeActiveWorkflowUpdate, 409)
	call(actor, "GET", "/rest/api/3/workflows/search?maxResults=0", "", 400)
	call(actor, "GET", "/rest/api/3/workflows/search?expand=transitions", "", 400)
	call(actor, "GET", "/rest/api/3/workflows/search?projectId=project_missing", "", 400)
	call(actor, "GET", "/rest/api/3/workflow/"+workflowID+"/project/prj_missing/issueTypeUsages", "", 404)
	call(actor, "GET", "/rest/api/3/workflow/"+workflowID+"/projectUsages?maxResults=0", "", 400)
	call(member, "DELETE", "/rest/api/3/workflow/"+workflowID, "", 403)
	call(actor, "DELETE", "/rest/api/3/workflow/"+workflowID, "", 400)
	call(actor, "DELETE", "/rest/api/3/workflow/wf_default", "", 400)
	call(actor, "DELETE", "/rest/api/3/workflow/workflow_missing", "", 404)
	call(actor, "DELETE", "/rest/api/3/workflowscheme/"+targetSchemeID, "", 409)
	unused := call(actor, "POST", "/rest/api/3/workflowscheme", `{"name":"Unused scheme","defaultWorkflow":"Default"}`, 201)
	if err := json.Unmarshal(unused.Body.Bytes(), &scheme); err != nil {
		t.Fatal(err)
	}
	unusedSchemeID := scheme["id"].(string)
	deletableWorkflow := simpleWorkflow
	deletableWorkflow.ID, deletableWorkflow.Name = store.NewID("workflow"), "Delete API lifecycle"
	if err := st.CreateWorkflow(ctx, ws, deletableWorkflow); err != nil {
		t.Fatal(err)
	}
	call(actor, "POST", "/rest/api/3/workflowscheme/"+unusedSchemeID+"/createdraft", "", 201)
	call(actor, "PUT", "/rest/api/3/workflowscheme/"+unusedSchemeID+"/draft/default", `{"workflow":"Delete API lifecycle"}`, 200)
	call(actor, "DELETE", "/rest/api/3/workflow/"+deletableWorkflow.ID, "", 400)
	call(actor, "DELETE", "/rest/api/3/workflowscheme/"+unusedSchemeID+"/draft", "", 204)
	call(actor, "DELETE", "/rest/api/3/workflowscheme/"+unusedSchemeID, "", 204)
	call(actor, "DELETE", "/rest/api/3/workflow/"+deletableWorkflow.ID, "", 204)
	if _, err := st.WorkflowByID(ctx, ws, deletableWorkflow.ID); err == nil {
		t.Fatal("deleted workflow remains readable")
	}
	var workflowDeleteAudits int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM organization_audit_events WHERE actor_id=$1 AND action='workflow.deleted' AND target_id=$2`, actor, deletableWorkflow.ID).Scan(&workflowDeleteAudits); err != nil || workflowDeleteAudits != 1 {
		t.Fatalf("workflow delete audits=%d err=%v", workflowDeleteAudits, err)
	}
}
