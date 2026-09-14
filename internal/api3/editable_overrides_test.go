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

	"github.com/e6qu/zzira/internal/apps"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
)

// TestEditableFlagAndOverrides covers Jira's jira.issue.editable status
// property: a work item in a status that sets it to false keeps its fields,
// comments and logged work, it still takes new comments and transitions, and
// only a Connect or Forge app with Administer Jira may override the flag.
func TestEditableFlagAndOverrides(t *testing.T) {
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
	stamp := time.Now().UnixNano() % 1000000
	projectKey, appKey := fmt.Sprintf("ED%06d", stamp), fmt.Sprintf("closer-%d", stamp)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Editable flag')`, workspaceID)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Editable admin')`, adminID, adminID+"@example.test")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'admin')`, workspaceID, adminID)
	exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, adminID, store.HashToken(adminID))
	principals := []string{}
	t.Cleanup(func() {
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM boards WHERE project_id IN (SELECT id FROM projects WHERE workspace_id=$1)`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workflow_schemes WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workflows WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM app_installations WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		for _, id := range append([]string{adminID}, principals...) {
			exec(`DELETE FROM api_tokens WHERE user_id=$1`, id)
			exec(`DELETE FROM users WHERE id=$1`, id)
		}
	})
	raw := []byte(`{"key":"` + appKey + `","name":"Closer","baseUrl":"https://closer.example.test","authentication":{"type":"jwt"},"scopes":["READ","WRITE","ADMIN"],"modules":{}}`)
	descriptor, err := apps.ParseDescriptor(raw)
	if err != nil {
		t.Fatal(err)
	}
	installation, err := st.InstallApp(ctx, workspaceID, adminID, descriptor, raw, []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	principals = append(principals, installation.PrincipalID)

	h := &Handler{Store: st, Commands: &commands.Service{Store: st}, WorkspaceSlug: workspaceID, BaseURL: "https://zzira.test"}
	send := func(asApp bool, method, path, body string, want int) string {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		if asApp {
			request = request.WithContext(apps.ContextWithInstallation(ctx, installation))
		} else {
			request.SetBasicAuth(adminID+"@example.test", adminID)
		}
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s (app=%v): got %d want %d: %s", method, path, asApp, response.Code, want, response.Body.String())
		}
		return response.Body.String()
	}
	call := func(method, path, body string, want int) string {
		t.Helper()
		return send(false, method, path, body, want)
	}
	project := map[string]any{}
	if err = json.Unmarshal([]byte(call(http.MethodPost, "/rest/api/3/project", `{"key":"`+projectKey+`","name":"Editable `+projectKey+`","projectTypeKey":"software","leadAccountId":"`+adminID+`","assigneeType":"UNASSIGNED"}`, http.StatusCreated)), &project); err != nil {
		t.Fatal(err)
	}
	workflowName := "Editable " + projectKey
	call(http.MethodPost, "/rest/api/3/workflows/create", `{"scope":{"type":"GLOBAL"},"statuses":[{"id":"st_todo","name":"To Do","statusCategory":"TODO","statusReference":"todo"},{"id":"st_done","name":"Done","statusCategory":"DONE","statusReference":"done"}],
		"workflows":[{"name":"`+workflowName+`","description":"","statuses":[{"statusReference":"todo","properties":{}},{"statusReference":"done","properties":{"jira.issue.editable":"false"}}],
		"transitions":[{"id":"1","name":"Create","type":"INITIAL","toStatusReference":"todo","links":[]},
		  {"id":"11","name":"Close","type":"DIRECTED","toStatusReference":"done","links":[{"fromStatusReference":"todo"}]},
		  {"id":"21","name":"Reopen","type":"GLOBAL","toStatusReference":"todo","links":[]}]}]}`, http.StatusOK)
	scheme := map[string]any{}
	if err = json.Unmarshal([]byte(call(http.MethodPost, "/rest/api/3/workflowscheme", `{"name":"Editable scheme `+projectKey+`","defaultWorkflow":"`+workflowName+`"}`, http.StatusCreated)), &scheme); err != nil {
		t.Fatal(err)
	}
	call(http.MethodPut, "/rest/api/3/workflowscheme/project", `{"projectId":"`+fmt.Sprint(project["id"])+`","workflowSchemeId":"`+fmt.Sprint(scheme["id"])+`"}`, http.StatusNoContent)

	var created struct {
		Key string `json:"key"`
	}
	if err = json.Unmarshal([]byte(call(http.MethodPost, "/rest/api/3/issue", `{"fields":{"project":{"key":"`+projectKey+`"},"issuetype":{"name":"Task"},"summary":"Ship it"}}`, http.StatusCreated)), &created); err != nil {
		t.Fatal(err)
	}
	issuePath := "/rest/api/3/issue/" + created.Key
	var comment struct {
		ID string `json:"id"`
	}
	if err = json.Unmarshal([]byte(call(http.MethodPost, issuePath+"/comment", `{"body":{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"Before closing"}]}]}}`, http.StatusCreated)), &comment); err != nil {
		t.Fatal(err)
	}
	var worklog struct {
		ID string `json:"id"`
	}
	if err = json.Unmarshal([]byte(call(http.MethodPost, issuePath+"/worklog", `{"timeSpentSeconds":600}`, http.StatusCreated)), &worklog); err != nil {
		t.Fatal(err)
	}
	call(http.MethodPost, issuePath+"/transitions", `{"transition":{"id":"11"}}`, http.StatusNoContent)

	// Closed work keeps its fields, comments and logged work.
	summary := `{"fields":{"summary":"Changed after closing"}}`
	if refused := call(http.MethodPut, issuePath, summary, http.StatusBadRequest); !strings.Contains(refused, "not editable") {
		t.Fatalf("editing a closed work item = %s", refused)
	}
	if meta := call(http.MethodGet, issuePath+"/editmeta", "", http.StatusOK); !strings.Contains(meta, `"fields":{}`) {
		t.Fatalf("edit metadata of a closed work item = %s", meta)
	}
	commentBody := `{"body":{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"Edited"}]}]}}`
	call(http.MethodPut, issuePath+"/comment/"+comment.ID, commentBody, http.StatusBadRequest)
	call(http.MethodPost, issuePath+"/worklog", `{"timeSpentSeconds":60}`, http.StatusBadRequest)
	call(http.MethodPut, issuePath+"/worklog/"+worklog.ID, `{"timeSpentSeconds":60}`, http.StatusBadRequest)
	call(http.MethodDelete, issuePath+"/worklog/"+worklog.ID, "", http.StatusBadRequest)
	// It still takes new comments.
	call(http.MethodPost, issuePath+"/comment", commentBody, http.StatusCreated)

	// A person, even an administrator, cannot override the flag.
	call(http.MethodPut, issuePath+"?overrideEditableFlag=true", summary, http.StatusForbidden)
	call(http.MethodGet, issuePath+"/editmeta?overrideScreenSecurity=true", "", http.StatusForbidden)
	call(http.MethodPut, issuePath+"?overrideEditableFlag=maybe", summary, http.StatusBadRequest)
	// Nor can an app without Administer Jira.
	send(true, http.MethodPut, issuePath+"?overrideEditableFlag=true", summary, http.StatusForbidden)

	// An app with Administer Jira can.
	exec(`UPDATE memberships SET role='admin' WHERE workspace_id=$1 AND user_id=$2`, workspaceID, installation.PrincipalID)
	send(true, http.MethodPut, issuePath+"?overrideEditableFlag=true", summary, http.StatusNoContent)
	if meta := send(true, http.MethodGet, issuePath+"/editmeta?overrideEditableFlag=true", "", http.StatusOK); !strings.Contains(meta, `"summary"`) {
		t.Fatalf("overridden edit metadata = %s", meta)
	}
	send(true, http.MethodPut, issuePath+"/comment/"+comment.ID+"?overrideEditableFlag=true", commentBody, http.StatusOK)
	send(true, http.MethodPost, issuePath+"/worklog?overrideEditableFlag=true", `{"timeSpentSeconds":60}`, http.StatusCreated)
	var stored string
	if err = st.Pool.QueryRow(ctx, `SELECT summary FROM issues WHERE workspace_id=$1 AND key=$2`, workspaceID, created.Key).Scan(&stored); err != nil || stored != "Changed after closing" {
		t.Fatalf("overridden summary = %q err=%v", stored, err)
	}

	// Reopened work is editable again, and the command layer agrees.
	call(http.MethodPost, issuePath+"/transitions", `{"transition":{"id":"21"}}`, http.StatusNoContent)
	call(http.MethodPut, issuePath, `{"fields":{"summary":"Reopened"}}`, http.StatusNoContent)
	issue, err := st.IssueByIDOrKey(ctx, workspaceID, created.Key)
	if err != nil {
		t.Fatal(err)
	}
	if editable, editableErr := h.Commands.IssueEditable(ctx, issue); editableErr != nil || !editable {
		t.Fatalf("reopened work editable = %v err=%v", editable, editableErr)
	}

	// Hidden by the field configuration, labels are out of reach until an app
	// with Administer Jira overrides screen security.
	var configurations struct {
		Values []struct {
			ID        json.RawMessage `json:"id"`
			IsDefault bool            `json:"isDefault"`
		} `json:"values"`
	}
	if err = json.Unmarshal([]byte(call(http.MethodGet, "/rest/api/3/fieldconfiguration", "", http.StatusOK)), &configurations); err != nil {
		t.Fatal(err)
	}
	for _, configuration := range configurations.Values {
		if configuration.IsDefault {
			call(http.MethodPut, "/rest/api/3/fieldconfiguration/"+strings.Trim(string(configuration.ID), `"`)+"/fields", `{"fieldConfigurationItems":[{"id":"labels","isHidden":true}]}`, http.StatusNoContent)
		}
	}
	labels := `{"fields":{"labels":["shipped"]}}`
	if refused := call(http.MethodPut, issuePath, labels, http.StatusBadRequest); !strings.Contains(refused, "hidden") {
		t.Fatalf("setting a hidden field = %s", refused)
	}
	if meta := call(http.MethodGet, issuePath+"/editmeta", "", http.StatusOK); strings.Contains(meta, `"labels"`) {
		t.Fatalf("edit metadata lists a hidden field: %s", meta)
	}
	if meta := send(true, http.MethodGet, issuePath+"/editmeta?overrideScreenSecurity=true", "", http.StatusOK); !strings.Contains(meta, `"labels"`) {
		t.Fatalf("overridden edit metadata lacks the hidden field: %s", meta)
	}
	send(true, http.MethodPut, issuePath+"?overrideScreenSecurity=true", labels, http.StatusNoContent)
	var storedLabels []string
	if err = st.Pool.QueryRow(ctx, `SELECT labels FROM issues WHERE workspace_id=$1 AND key=$2`, workspaceID, created.Key).Scan(&storedLabels); err != nil || len(storedLabels) != 1 || storedLabels[0] != "shipped" {
		t.Fatalf("overridden labels = %v err=%v", storedLabels, err)
	}
}
