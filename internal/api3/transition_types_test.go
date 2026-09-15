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
)

// TestInitialAndGlobalTransitions covers Jira's non-directed transitions: the
// initial transition decides where work items start and runs its validators
// and post functions on creation, a global transition runs from every status,
// both keep their designer ports, and the classic workflow search shows them
// with lowercase types, rules and operations.
func TestInitialAndGlobalTransitions(t *testing.T) {
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
	workspaceID, adminID := store.NewID("ws"), store.NewID("usr")
	projectKey := fmt.Sprintf("TT%06d", time.Now().UnixNano()%1000000)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Transition types')`, workspaceID)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Types admin')`, adminID, adminID+"@example.test")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'admin')`, workspaceID, adminID)
	exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, adminID, store.HashToken(adminID))
	t.Cleanup(func() {
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM boards WHERE project_id IN (SELECT id FROM projects WHERE workspace_id=$1)`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workflow_schemes WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workflows WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		exec(`DELETE FROM api_tokens WHERE user_id=$1`, adminID)
		exec(`DELETE FROM users WHERE id=$1`, adminID)
	})
	h := &Handler{Store: st, Commands: &commands.Service{Store: st}, WorkspaceSlug: workspaceID, BaseURL: "https://zzira.test"}
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
	project := map[string]any{}
	if err = json.Unmarshal([]byte(call(http.MethodPost, "/rest/api/3/project", `{"key":"`+projectKey+`","name":"Types `+projectKey+`","projectTypeKey":"software","leadAccountId":"`+adminID+`","assigneeType":"UNASSIGNED"}`, http.StatusCreated)), &project); err != nil {
		t.Fatal(err)
	}

	statuses := `"statuses":[{"id":"st_todo","name":"To Do","statusCategory":"TODO","statusReference":"todo"},{"id":"st_inprogress","name":"In Progress","statusCategory":"IN_PROGRESS","statusReference":"progress"},{"id":"st_done","name":"Done","statusCategory":"DONE","statusReference":"done"}]`
	payload := func(name, transitions string) string {
		return `{"scope":{"type":"GLOBAL"},` + statuses + `,"workflows":[{"name":"` + name + `","description":"","statuses":[{"statusReference":"todo","properties":{}},{"statusReference":"progress","properties":{}},{"statusReference":"done","properties":{}}],"transitions":[` + transitions + `]}]}`
	}
	validate := func(transitions, wantCode string) {
		t.Helper()
		result := call(http.MethodPost, "/rest/api/3/workflows/create/validation", `{"payload":`+payload("Invalid "+projectKey, transitions)+`,"validationOptions":{"levels":["ERROR"]}}`, http.StatusOK)
		if !strings.Contains(result, wantCode) {
			t.Fatalf("validation of %s = %s, want %s", transitions, result, wantCode)
		}
	}
	finish := `{"id":"11","name":"Finish","type":"DIRECTED","toStatusReference":"done","links":[{"fromStatusReference":"progress","fromPort":1,"toPort":3}]}`
	validate(`{"id":"1","name":"Create","type":"INITIAL","toStatusReference":"todo","links":[]},{"id":"2","name":"Again","type":"INITIAL","toStatusReference":"done","links":[]},`+finish, "TRANSITION_INITIAL_DUPLICATE")
	validate(`{"id":"21","name":"Anywhere","type":"GLOBAL","toStatusReference":"done","links":[{"fromStatusReference":"todo"}]},`+finish, "TRANSITION_SOURCE_INVALID")
	validate(`{"id":"21","name":"Loop","type":"LOOPED","toStatusReference":"done","links":[]},`+finish, "TRANSITION_TYPE_INVALID")

	// Work starts In Progress, needs a label and is assigned to its creator.
	workflowName := "Types " + projectKey
	call(http.MethodPost, "/rest/api/3/workflows/create", payload(workflowName,
		`{"id":"1","name":"Create","type":"INITIAL","toStatusReference":"progress","links":[{"fromPort":0,"toPort":2}],
		  "validators":[{"ruleKey":"system:validate-field-value","parameters":{"ruleType":"fieldRequired","fieldsRequired":"labels","errorMessage":"Label the work"}}],
		  "actions":[{"ruleKey":"system:change-assignee","parameters":{"type":"to-current-user"}}]},`+finish+`,
		 {"id":"21","name":"Back to do","type":"GLOBAL","description":"Return from anywhere","toStatusReference":"todo","links":[],"properties":{"jira.i18n.title":"back"}}`), http.StatusOK)
	scheme := map[string]any{}
	if err = json.Unmarshal([]byte(call(http.MethodPost, "/rest/api/3/workflowscheme", `{"name":"Types scheme `+projectKey+`","defaultWorkflow":"`+workflowName+`"}`, http.StatusCreated)), &scheme); err != nil {
		t.Fatal(err)
	}
	call(http.MethodPut, "/rest/api/3/workflowscheme/project", `{"projectId":"`+fmt.Sprint(project["id"])+`","workflowSchemeId":"`+fmt.Sprint(scheme["id"])+`"}`, http.StatusNoContent)

	search := call(http.MethodGet, "/rest/api/3/workflows/search?queryString="+projectKey+"&expand=values.transitions", "", http.StatusOK)
	for _, want := range []string{`"type":"INITIAL"`, `"type":"GLOBAL"`, `"fromPort":1`, `"toPort":2`, `"description":"Return from anywhere"`, `"jira.i18n.title":"back"`} {
		if !strings.Contains(search, want) {
			t.Fatalf("workflow search lacks %s: %s", want, search)
		}
	}

	issue := `{"fields":{"project":{"key":"` + projectKey + `"},"issuetype":{"name":"Task"},"summary":"Typed work"%s}}`
	if refused := call(http.MethodPost, "/rest/api/3/issue", fmt.Sprintf(issue, ""), http.StatusBadRequest); !strings.Contains(refused, "Label the work") {
		t.Fatalf("the Create validator did not run: %s", refused)
	}
	var created struct {
		Key string `json:"key"`
	}
	if err = json.Unmarshal([]byte(call(http.MethodPost, "/rest/api/3/issue", fmt.Sprintf(issue, `,"labels":["typed"]`), http.StatusCreated)), &created); err != nil {
		t.Fatal(err)
	}
	var stored struct{ status, assignee string }
	if err = st.Pool.QueryRow(ctx, `SELECT status_id,COALESCE(assignee_id,'') FROM issues WHERE workspace_id=$1 AND key=$2`, workspaceID, created.Key).Scan(&stored.status, &stored.assignee); err != nil {
		t.Fatal(err)
	}
	if stored.status != "st_inprogress" || stored.assignee != adminID {
		t.Fatalf("created work item = %+v", stored)
	}

	names := func() []string {
		t.Helper()
		var page struct {
			Transitions []struct {
				Name string `json:"name"`
			} `json:"transitions"`
		}
		if err := json.Unmarshal([]byte(call(http.MethodGet, "/rest/api/3/issue/"+created.Key+"/transitions", "", http.StatusOK)), &page); err != nil {
			t.Fatal(err)
		}
		out := []string{}
		for _, transition := range page.Transitions {
			out = append(out, transition.Name)
		}
		return out
	}
	if got := strings.Join(names(), ","); got != "Finish,Back to do" {
		t.Fatalf("transitions from In Progress = %s", got)
	}
	call(http.MethodPost, "/rest/api/3/issue/"+created.Key+"/transitions", `{"transition":{"id":"21"}}`, http.StatusNoContent)
	if got := strings.Join(names(), ","); got != "Back to do" {
		t.Fatalf("transitions from To Do = %s", got)
	}
	call(http.MethodPost, "/rest/api/3/issue/"+created.Key+"/transitions", `{"transition":{"id":"1"}}`, http.StatusBadRequest)

	// The classic search shows the same transitions with lowercase types.
	legacy := call(http.MethodGet, "/rest/api/3/workflow/search?workflowName="+strings.ReplaceAll(workflowName, " ", "+")+"&expand=transitions.rules,statuses,default,schemes,projects,operations,hasDraftWorkflow", "", http.StatusOK)
	for _, want := range []string{`"type":"initial"`, `"type":"global"`, `"type":"directed"`, `"postFunctions":[{"configuration":{"type":"to-current-user"},"type":"system:change-assignee"}]`,
		`"operations":{"canDelete":false,"canEdit":true}`, `"isDefault":false`, `"hasDraftWorkflow":false`, `"key":"` + projectKey + `"`, `"name":"Types scheme ` + projectKey + `"`, `"total":1`, `"name":"In Progress"`} {
		if !strings.Contains(legacy, want) {
			t.Fatalf("classic workflow search lacks %s: %s", want, legacy)
		}
	}
	if strings.Contains(legacy, `"st_inprogress"`) {
		t.Fatalf("classic workflow search exposes stored status ids: %s", legacy)
	}
	if inactive := call(http.MethodGet, "/rest/api/3/workflow/search?workflowName="+strings.ReplaceAll(workflowName, " ", "+")+"&isActive=false", "", http.StatusOK); !strings.Contains(inactive, `"total":0`) {
		t.Fatalf("an active workflow matched isActive=false: %s", inactive)
	}
	call(http.MethodGet, "/rest/api/3/workflow/search?orderBy=position", "", http.StatusBadRequest)
	call(http.MethodPost, "/rest/api/3/workflow", `{"id":"wf","name":"Legacy"}`, http.StatusNotFound)
}
