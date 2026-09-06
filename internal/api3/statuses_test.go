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

func TestStatusAPILifecycleAndWorkspaceScope(t *testing.T) {
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
	ws, actor, member := store.NewID("ws"), store.NewID("usr"), store.NewID("usr")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Status API test')`, ws)
	for _, user := range []string{actor, member} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Status user')`, user, user+"@example.test")
		role := "member"
		if user == actor {
			role = "admin"
		}
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,$3)`, ws, user, role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, user, store.HashToken(user))
	}
	projectID, otherProjectID, issueID := store.NewID("project"), store.NewID("project"), store.NewID("issue")
	exec(`INSERT INTO projects(id,workspace_id,key,name) VALUES($1,$2,'STAT','Status project')`, projectID, ws)
	exec(`INSERT INTO projects(id,workspace_id,key,name) VALUES($1,$2,'OTHER','Other status project')`, otherProjectID, ws)
	exec(`INSERT INTO issues(id,workspace_id,project_id,key,summary,status_id,issuetype_id,updated_seq) VALUES($1,$2,$3,'STAT-1','Status usage','st_todo','it_task',0)`, issueID, ws, projectID)
	t.Cleanup(func() {
		exec(`DELETE FROM issues WHERE workspace_id=$1`, ws)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, ws)
		exec(`DELETE FROM statuses WHERE workspace_id=$1`, ws)
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
	body := `{"scope":{"type":"GLOBAL"},"statuses":[{"name":"Peer review","description":"Review pending","statusCategory":"IN_PROGRESS"}]}`
	call(member, "POST", "/rest/api/3/statuses", body, 403)
	created := call(actor, "POST", "/rest/api/3/statuses", body, 200)
	var createdStatuses []map[string]any
	if err := json.Unmarshal(created.Body.Bytes(), &createdStatuses); err != nil || len(createdStatuses) != 1 {
		t.Fatalf("created response: %s (%v)", created.Body.String(), err)
	}
	id := createdStatuses[0]["id"].(string)
	if createdStatuses[0]["statusCategory"] != "IN_PROGRESS" || createdStatuses[0]["description"] != "Review pending" {
		t.Fatalf("created status = %#v", createdStatuses[0])
	}
	call(member, "GET", "/rest/api/3/statuses?id="+id, "", 200)
	call(member, "GET", "/rest/api/3/status/Peer%20review", "", 200)
	search := call(member, "GET", "/rest/api/3/statuses/search?searchString=peer&statusCategory=IN_PROGRESS&maxResults=1", "", 200)
	if !strings.Contains(search.Body.String(), `"total":1`) || !strings.Contains(search.Body.String(), id) {
		t.Fatal(search.Body.String())
	}
	update := `{"statuses":[{"id":"` + id + `","name":"Review complete","description":"Reviewed","statusCategory":"DONE"}]}`
	call(actor, "PUT", "/rest/api/3/statuses", update, 204)
	call(member, "GET", "/rest/api/3/statuses/byNames?name=Review%20complete", "", 200)
	call(member, "PUT", "/rest/api/3/statuses", update, 403)
	projectBody := `{"scope":{"type":"PROJECT","project":{"id":"` + projectID + `"}},"statuses":[{"name":"Awaiting customer","description":"Waiting for a reply","statusCategory":"IN_PROGRESS"}]}`
	projectCreated := call(actor, "POST", "/rest/api/3/statuses", projectBody, 200)
	var projectStatuses []map[string]any
	if err := json.Unmarshal(projectCreated.Body.Bytes(), &projectStatuses); err != nil || len(projectStatuses) != 1 {
		t.Fatalf("project status response: %s (%v)", projectCreated.Body.String(), err)
	}
	projectStatusID := projectStatuses[0]["id"].(string)
	projectScope := projectStatuses[0]["scope"].(map[string]any)
	if projectScope["type"] != "PROJECT" || projectScope["project"].(map[string]any)["id"] != projectID {
		t.Fatalf("project scope = %#v", projectScope)
	}
	otherProjectBody := `{"scope":{"type":"PROJECT","project":{"id":"` + otherProjectID + `"}},"statuses":[{"name":"Awaiting customer","statusCategory":"IN_PROGRESS"}]}`
	otherCreated := call(actor, "POST", "/rest/api/3/statuses", otherProjectBody, 200)
	var otherStatuses []map[string]any
	if err := json.Unmarshal(otherCreated.Body.Bytes(), &otherStatuses); err != nil || len(otherStatuses) != 1 {
		t.Fatalf("other project status response: %s (%v)", otherCreated.Body.String(), err)
	}
	otherStatusID := otherStatuses[0]["id"].(string)
	byName := call(member, "GET", "/rest/api/3/statuses/byNames?projectId="+projectID+"&name=Awaiting%20customer", "", 200)
	if !strings.Contains(byName.Body.String(), projectStatusID) || strings.Contains(byName.Body.String(), otherStatusID) {
		t.Fatalf("project byNames leaked status: %s", byName.Body.String())
	}
	projectSearch := call(member, "GET", "/rest/api/3/statuses/search?projectId="+projectID+"&includeGlobalStatuses=false", "", 200)
	if !strings.Contains(projectSearch.Body.String(), projectStatusID) || strings.Contains(projectSearch.Body.String(), otherStatusID) || strings.Contains(projectSearch.Body.String(), id) {
		t.Fatalf("project-only search = %s", projectSearch.Body.String())
	}
	projectAndGlobalSearch := call(member, "GET", "/rest/api/3/statuses/search?projectId="+projectID+"&includeGlobalStatuses=true", "", 200)
	if !strings.Contains(projectAndGlobalSearch.Body.String(), projectStatusID) || !strings.Contains(projectAndGlobalSearch.Body.String(), id) || strings.Contains(projectAndGlobalSearch.Body.String(), otherStatusID) {
		t.Fatalf("project and global search = %s", projectAndGlobalSearch.Body.String())
	}
	bulkProject := call(member, "GET", "/rest/api/3/statuses?id="+projectStatusID, "", 200)
	if !strings.Contains(bulkProject.Body.String(), `"type":"PROJECT"`) || !strings.Contains(bulkProject.Body.String(), projectID) {
		t.Fatalf("bulk project status = %s", bulkProject.Body.String())
	}
	call(actor, "POST", "/rest/api/3/statuses", `{"scope":{"type":"PROJECT","project":{"id":"missing"}},"statuses":[{"name":"Invalid","statusCategory":"TODO"}]}`, 400)
	if _, _, err := st.UpdateIssue(ctx, actor, ws, issueID, store.IssueUpdate{StatusID: &otherStatusID}); err == nil {
		t.Fatal("issue accepted a status owned by another project")
	}
	projectUsage := call(member, "GET", "/rest/api/3/statuses/st_todo/projectUsages?maxResults=10", "", 200)
	if !strings.Contains(projectUsage.Body.String(), projectID) {
		t.Fatal(projectUsage.Body.String())
	}
	workflowUsage := call(member, "GET", "/rest/api/3/statuses/st_todo/workflowUsages", "", 200)
	if !strings.Contains(workflowUsage.Body.String(), "wf_default") {
		t.Fatal(workflowUsage.Body.String())
	}
	issueTypeUsage := call(member, "GET", "/rest/api/3/statuses/st_todo/project/"+projectID+"/issueTypeUsages", "", 200)
	if !strings.Contains(issueTypeUsage.Body.String(), "it_task") {
		t.Fatal(issueTypeUsage.Body.String())
	}
	call(actor, "DELETE", "/rest/api/3/statuses?id=st_todo", "", 409)
	call(actor, "DELETE", "/rest/api/3/statuses?id="+projectStatusID, "", 204)
	call(actor, "DELETE", "/rest/api/3/statuses?id="+otherStatusID, "", 204)
	call(actor, "DELETE", "/rest/api/3/statuses?id="+id, "", 204)
	call(member, "GET", "/rest/api/3/status/"+id, "", 404)
	call(member, "GET", "/rest/api/3/statuscategory/4", "", 200)
}
