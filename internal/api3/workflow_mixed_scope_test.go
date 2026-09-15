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

// TestWorkflowUpdateAcrossScopes covers one workflow update changing a
// project's workflow and a global workflow together: each new status takes
// the scope of the workflows using it.
func TestWorkflowUpdateAcrossScopes(t *testing.T) {
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
	projectKey := fmt.Sprintf("MS%05d", time.Now().UnixNano()%100000)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Mixed scopes')`, workspaceID)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Workflow admin')`, adminID, adminID+"@example.test")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'admin')`, workspaceID, adminID)
	exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, adminID, store.HashToken(adminID))
	t.Cleanup(func() {
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM boards WHERE project_id IN (SELECT id FROM projects WHERE workspace_id=$1)`, workspaceID)
		exec(`DELETE FROM workflows WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM statuses WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
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
	if err = json.Unmarshal([]byte(call(http.MethodPost, "/rest/api/3/project", `{"key":"`+projectKey+`","name":"Mixed `+projectKey+`","projectTypeKey":"software","leadAccountId":"`+adminID+`","assigneeType":"UNASSIGNED"}`, http.StatusCreated)), &project); err != nil {
		t.Fatal(err)
	}
	type createdWorkflows struct {
		Workflows []struct {
			ID      string `json:"id"`
			Version struct {
				ID            string `json:"id"`
				VersionNumber int    `json:"versionNumber"`
			} `json:"version"`
		} `json:"workflows"`
	}
	create := func(scope, name string) createdWorkflows {
		t.Helper()
		body := `{"scope":` + scope + `,"statuses":[{"id":"st_todo","name":"To Do","statusCategory":"TODO","statusReference":"todo"},{"id":"st_done","name":"Done","statusCategory":"DONE","statusReference":"done"}],
			"workflows":[{"name":"` + name + `","description":"","statuses":[{"statusReference":"todo","properties":{}},{"statusReference":"done","properties":{}}],
			"transitions":[{"id":"1","name":"Create","type":"INITIAL","toStatusReference":"todo","links":[]},{"id":"11","name":"Finish","type":"DIRECTED","toStatusReference":"done","links":[{"fromStatusReference":"todo"}]}]}]}`
		var created createdWorkflows
		if err := json.Unmarshal([]byte(call(http.MethodPost, "/rest/api/3/workflows/create", body, http.StatusOK)), &created); err != nil || len(created.Workflows) != 1 {
			t.Fatalf("created workflows = %+v err=%v", created, err)
		}
		return created
	}
	team := create(`{"type":"PROJECT","project":{"id":"`+fmt.Sprint(project["id"])+`"}}`, "Team flow "+projectKey)
	site := create(`{"type":"GLOBAL"}`, "Site flow "+projectKey)

	workflowItem := func(created createdWorkflows, review string) string {
		item := created.Workflows[0]
		return `{"id":"` + item.ID + `","version":{"id":"` + item.Version.ID + `","versionNumber":` + fmt.Sprint(item.Version.VersionNumber) + `},
			"statuses":[{"statusReference":"st_todo","properties":{}},{"statusReference":"` + review + `","properties":{}},{"statusReference":"st_done","properties":{}}],
			"transitions":[{"id":"1","name":"Create","type":"INITIAL","toStatusReference":"st_todo","links":[]},
				{"id":"11","name":"Review","type":"DIRECTED","toStatusReference":"` + review + `","links":[{"fromStatusReference":"st_todo"}]},
				{"id":"21","name":"Finish","type":"DIRECTED","toStatusReference":"st_done","links":[{"fromStatusReference":"` + review + `"}]}]}`
	}
	payload := `{"statuses":[{"name":"Team review ` + projectKey + `","statusCategory":"IN_PROGRESS","statusReference":"team-review"},{"name":"Site review ` + projectKey + `","statusCategory":"IN_PROGRESS","statusReference":"site-review"}],
		"workflows":[` + workflowItem(team, "team-review") + `,` + workflowItem(site, "site-review") + `]}`
	if validation := call(http.MethodPost, "/rest/api/3/workflows/update/validation", `{"payload":`+payload+`,"validationOptions":{"levels":["ERROR"]}}`, http.StatusOK); strings.Contains(validation, `"code"`) {
		t.Fatalf("mixed-scope validation = %s", validation)
	}
	call(http.MethodPost, "/rest/api/3/workflows/update", payload, http.StatusOK)

	scopeOf := func(name string) string {
		t.Helper()
		var projectID *string
		if err := st.Pool.QueryRow(ctx, `SELECT project_id FROM statuses WHERE workspace_id=$1 AND name=$2`, workspaceID, name).Scan(&projectID); err != nil {
			t.Fatalf("status %q: %v", name, err)
		}
		if projectID == nil {
			return ""
		}
		return *projectID
	}
	var storedProject string
	if err = st.Pool.QueryRow(ctx, `SELECT id FROM projects WHERE workspace_id=$1 AND key=$2`, workspaceID, projectKey).Scan(&storedProject); err != nil {
		t.Fatal(err)
	}
	if scope := scopeOf("Team review " + projectKey); scope != storedProject {
		t.Fatalf("the team workflow's new status has scope %q, want the project", scope)
	}
	if scope := scopeOf("Site review " + projectKey); scope != "" {
		t.Fatalf("the global workflow's new status has scope %q, want global", scope)
	}
}
