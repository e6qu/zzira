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

// TestProjectWorkflowAccess covers Jira's workflow permissions: a project's
// administrators create, validate, update and inspect its project-scoped
// workflows but not global ones, people who may view its workflows read-only
// search and preview them, and everyone else is refused with 401.
func TestProjectWorkflowAccess(t *testing.T) {
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
	adminID, leadID, viewerID, outsiderID := store.NewID("usr"), store.NewID("usr"), store.NewID("usr"), store.NewID("usr")
	stamp := time.Now().UnixNano() % 1000000
	projectKey, otherKey := fmt.Sprintf("WA%06d", stamp), fmt.Sprintf("WB%06d", stamp)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Workflow access')`, workspaceID)
	for _, identity := range []struct{ id, role string }{{adminID, "admin"}, {leadID, "member"}, {viewerID, "member"}, {outsiderID, "member"}} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test',$3)`, identity.id, identity.id+"@example.test", "Access "+identity.id)
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,$3)`, workspaceID, identity.id, identity.role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, identity.id, store.HashToken(identity.id))
	}
	t.Cleanup(func() {
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM boards WHERE project_id IN (SELECT id FROM projects WHERE workspace_id=$1)`, workspaceID)
		exec(`DELETE FROM role_bindings WHERE scope_type='project' AND scope_id IN (SELECT id FROM projects WHERE workspace_id=$1)`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workflow_schemes WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workflows WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM statuses WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		for _, id := range []string{adminID, leadID, viewerID, outsiderID} {
			exec(`DELETE FROM api_tokens WHERE user_id=$1`, id)
			exec(`DELETE FROM users WHERE id=$1`, id)
		}
	})
	h := &Handler{Store: st, Commands: &commands.Service{Store: st}, WorkspaceSlug: workspaceID, BaseURL: "https://zzira.test"}
	call := func(user, method, path, body string, want int) string {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.SetBasicAuth(user+"@example.test", user)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s as %s: got %d want %d: %s", method, path, user, response.Code, want, response.Body.String())
		}
		return response.Body.String()
	}
	projectID := func(key string) string {
		t.Helper()
		project := map[string]any{}
		if err := json.Unmarshal([]byte(call(adminID, http.MethodPost, "/rest/api/3/project", `{"key":"`+key+`","name":"Access `+key+`","projectTypeKey":"software","leadAccountId":"`+adminID+`","assigneeType":"UNASSIGNED"}`, http.StatusCreated)), &project); err != nil {
			t.Fatal(err)
		}
		return fmt.Sprint(project["id"])
	}
	mainID, otherID := projectID(projectKey), projectID(otherKey)
	// Every member joins a new project's Members role, which may view its
	// workflows read-only. The lead also administers the main project; the lead
	// and the viewer are taken out of the other project, and the outsider out
	// of both.
	call(adminID, http.MethodPost, "/rest/api/3/project/"+projectKey+"/role/10000", `{"user":["`+leadID+`"]}`, http.StatusOK)
	exec(`DELETE FROM role_bindings WHERE scope_type='project' AND scope_id IN (SELECT id FROM projects WHERE workspace_id=$1 AND key=$2) AND principal_id = ANY($3)`, workspaceID, otherKey, []string{leadID, viewerID})
	exec(`DELETE FROM role_bindings WHERE scope_type='project' AND scope_id IN (SELECT id FROM projects WHERE workspace_id=$1) AND principal_id=$2`, workspaceID, outsiderID)

	create := func(scope, name string) string {
		return `{"scope":` + scope + `,"statuses":[{"id":"st_todo","name":"To Do","statusCategory":"TODO","statusReference":"todo"},{"id":"st_done","name":"Done","statusCategory":"DONE","statusReference":"done"}],
			"workflows":[{"name":"` + name + `","description":"","statuses":[{"statusReference":"todo","properties":{}},{"statusReference":"done","properties":{}}],
			"transitions":[{"id":"1","name":"Create","type":"INITIAL","toStatusReference":"todo","links":[]},{"id":"11","name":"Finish","type":"DIRECTED","toStatusReference":"done","links":[{"fromStatusReference":"todo"}]}]}]}`
	}
	projectScope := `{"type":"PROJECT","project":{"id":"` + mainID + `"}}`
	projectName, globalName := "Team flow "+projectKey, "Site flow "+projectKey

	// The project's administrator works on its workflows, not the site's.
	call(leadID, http.MethodPost, "/rest/api/3/workflows/create/validation", `{"payload":`+create(projectScope, projectName)+`,"validationOptions":{"levels":["ERROR"]}}`, http.StatusOK)
	call(leadID, http.MethodPost, "/rest/api/3/workflows/create/validation", `{"payload":`+create(`{"type":"GLOBAL"}`, globalName)+`,"validationOptions":{"levels":["ERROR"]}}`, http.StatusUnauthorized)
	call(leadID, http.MethodPost, "/rest/api/3/workflows/create", create(`{"type":"GLOBAL"}`, globalName), http.StatusUnauthorized)
	call(leadID, http.MethodPost, "/rest/api/3/workflows/create", create(`{"type":"PROJECT","project":{"id":"`+otherID+`"}}`, "Elsewhere "+projectKey), http.StatusUnauthorized)
	call(viewerID, http.MethodPost, "/rest/api/3/workflows/create", create(projectScope, "Viewer "+projectKey), http.StatusUnauthorized)
	var created struct {
		Workflows []struct {
			ID      string `json:"id"`
			Version struct {
				ID            string `json:"id"`
				VersionNumber int    `json:"versionNumber"`
			} `json:"version"`
		} `json:"workflows"`
	}
	if err = json.Unmarshal([]byte(call(leadID, http.MethodPost, "/rest/api/3/workflows/create", create(projectScope, projectName), http.StatusOK)), &created); err != nil || len(created.Workflows) != 1 {
		t.Fatalf("project workflow = %+v err=%v", created, err)
	}
	var global struct {
		Workflows []struct {
			ID      string `json:"id"`
			Version struct {
				ID            string `json:"id"`
				VersionNumber int    `json:"versionNumber"`
			} `json:"version"`
		} `json:"workflows"`
	}
	if err = json.Unmarshal([]byte(call(adminID, http.MethodPost, "/rest/api/3/workflows/create", create(`{"type":"GLOBAL"}`, globalName), http.StatusOK)), &global); err != nil || len(global.Workflows) != 1 {
		t.Fatalf("global workflow = %+v err=%v", global, err)
	}
	teamID, siteID := created.Workflows[0].ID, global.Workflows[0].ID

	update := func(id, versionID string, version int, description string) string {
		return `{"statuses":[],"workflows":[{"id":"` + id + `","description":"` + description + `","version":{"id":"` + versionID + `","versionNumber":` + fmt.Sprint(version) + `},
			"statuses":[{"statusReference":"st_todo","properties":{}},{"statusReference":"st_done","properties":{}}],
			"transitions":[{"id":"1","name":"Create","type":"INITIAL","toStatusReference":"st_todo","links":[]},{"id":"11","name":"Finish","type":"DIRECTED","toStatusReference":"st_done","links":[{"fromStatusReference":"st_todo"}]}]}]}`
	}
	call(leadID, http.MethodPost, "/rest/api/3/workflows/update/validation", `{"payload":`+update(siteID, global.Workflows[0].Version.ID, global.Workflows[0].Version.VersionNumber, "mine now")+`}`, http.StatusUnauthorized)
	call(leadID, http.MethodPost, "/rest/api/3/workflows/update", update(siteID, global.Workflows[0].Version.ID, global.Workflows[0].Version.VersionNumber, "mine now"), http.StatusUnauthorized)
	if updated := call(leadID, http.MethodPost, "/rest/api/3/workflows/update", update(teamID, created.Workflows[0].Version.ID, created.Workflows[0].Version.VersionNumber, "Tuned by the team"), http.StatusOK); !strings.Contains(updated, "Tuned by the team") {
		t.Fatalf("project workflow update = %s", updated)
	}
	call(leadID, http.MethodGet, "/rest/api/3/workflows/capabilities?workflowId="+teamID, "", http.StatusOK)
	call(leadID, http.MethodGet, "/rest/api/3/workflows/capabilities?workflowId="+siteID, "", http.StatusUnauthorized)
	call(viewerID, http.MethodGet, "/rest/api/3/workflows/capabilities?workflowId="+teamID, "", http.StatusUnauthorized)

	// Search and bulk read show project-scoped workflows to the people who
	// may view them, and the site's workflows only to administrators.
	for _, user := range []string{leadID, viewerID} {
		found := call(user, http.MethodGet, "/rest/api/3/workflows/search?queryString="+projectKey, "", http.StatusOK)
		if !strings.Contains(found, teamID) || strings.Contains(found, siteID) {
			t.Fatalf("workflow search as %s = %s", user, found)
		}
		read := call(user, http.MethodPost, "/rest/api/3/workflows", `{"workflowIds":["`+teamID+`","`+siteID+`"]}`, http.StatusOK)
		if !strings.Contains(read, teamID) || strings.Contains(read, siteID) {
			t.Fatalf("bulk workflow read as %s = %s", user, read)
		}
	}
	if found := call(outsiderID, http.MethodGet, "/rest/api/3/workflows/search?queryString="+projectKey, "", http.StatusOK); strings.Contains(found, teamID) || strings.Contains(found, siteID) {
		t.Fatalf("workflow search as an outsider = %s", found)
	}
	if found := call(adminID, http.MethodGet, "/rest/api/3/workflows/search?queryString="+projectKey, "", http.StatusOK); !strings.Contains(found, teamID) || !strings.Contains(found, siteID) {
		t.Fatalf("workflow search as an administrator = %s", found)
	}

	// Deleting a workflow stays with site administrators, and an unused
	// project-scoped workflow can be deleted like a global one.
	var spare struct {
		Workflows []struct {
			ID string `json:"id"`
		} `json:"workflows"`
	}
	if err = json.Unmarshal([]byte(call(leadID, http.MethodPost, "/rest/api/3/workflows/create", create(projectScope, "Spare "+projectKey), http.StatusOK)), &spare); err != nil || len(spare.Workflows) != 1 {
		t.Fatalf("spare project workflow = %+v err=%v", spare, err)
	}
	call(leadID, http.MethodDelete, "/rest/api/3/workflow/"+spare.Workflows[0].ID, "", http.StatusForbidden)
	call(adminID, http.MethodDelete, "/rest/api/3/workflow/"+spare.Workflows[0].ID, "", http.StatusNoContent)
	call(adminID, http.MethodDelete, "/rest/api/3/workflow/"+spare.Workflows[0].ID, "", http.StatusNotFound)

	// A preview needs a project permission on the project it previews.
	var issueTypes []struct {
		ID string `json:"id"`
	}
	if err = json.Unmarshal([]byte(call(adminID, http.MethodGet, "/rest/api/3/issuetype", "", http.StatusOK)), &issueTypes); err != nil || len(issueTypes) == 0 {
		t.Fatalf("issue types = %v err=%v", issueTypes, err)
	}
	preview := func(project string) string {
		return `{"projectId":"` + project + `","issueTypeIds":["` + issueTypes[0].ID + `"]}`
	}
	call(viewerID, http.MethodPost, "/rest/api/3/workflows/preview", preview(mainID), http.StatusOK)
	call(viewerID, http.MethodPost, "/rest/api/3/workflows/preview", preview(otherID), http.StatusUnauthorized)
	call(outsiderID, http.MethodPost, "/rest/api/3/workflows/preview", preview(mainID), http.StatusUnauthorized)
}
