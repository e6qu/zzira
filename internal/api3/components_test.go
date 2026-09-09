package api3

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
)

func TestProjectComponentContractJourney(t *testing.T) {
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
	workspaceID, adminID, memberID, projectID := store.NewID("ws"), store.NewID("usr"), store.NewID("usr"), "98"+strings.TrimPrefix(store.NewID("n"), "n_")[:8]
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Component contract')`, workspaceID)
	for _, entry := range []struct{ id, role string }{{adminID, "admin"}, {memberID, "member"}} {
		displayName := "Member user"
		if entry.role == "admin" {
			displayName = "Admin user"
		}
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test',$3)`, entry.id, entry.id+"@example.test", displayName)
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,$3)`, workspaceID, entry.id, entry.role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, entry.id, store.HashToken(entry.id))
	}
	exec(`INSERT INTO projects(id,workspace_id,key,name,workflow_id,lead_account_id,assignee_type,project_type_key) VALUES($1,$2,'CMP','Components','wf_default',$3,'PROJECT_LEAD','software')`, projectID, workspaceID, adminID)
	t.Cleanup(func() {
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM project_components WHERE project_id=$1`, projectID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM projects WHERE id=$1`, projectID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		for _, id := range []string{adminID, memberID} {
			exec(`DELETE FROM api_tokens WHERE user_id=$1`, id)
			exec(`DELETE FROM users WHERE id=$1`, id)
		}
	})

	h := &Handler{Store: st, Commands: &commands.Service{Store: st}, WorkspaceSlug: workspaceID, BaseURL: "https://zzira.test"}
	call := func(userID, method, path, body string, want int) map[string]any {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.SetBasicAuth(userID+"@example.test", userID)
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, response.Code, want, response.Body.String())
		}
		var value map[string]any
		if response.Body.Len() > 0 && strings.HasPrefix(response.Header().Get("Content-Type"), "application/json") {
			if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil {
				t.Fatal(err)
			}
		}
		return value
	}

	componentBody := `{"project":"CMP","name":"Runtime","description":"Runtime ownership","leadAccountId":"` + memberID + `","assigneeType":"COMPONENT_LEAD"}`
	call(memberID, http.MethodPost, "/rest/api/3/component", componentBody, http.StatusForbidden)
	first := call(adminID, http.MethodPost, "/rest/api/3/component", componentBody, http.StatusCreated)
	firstID := first["id"].(string)
	if first["project"] != "CMP" || first["realAssigneeType"] != "COMPONENT_LEAD" || first["isAssigneeTypeValid"] != true {
		t.Fatalf("created component = %#v", first)
	}
	call(adminID, http.MethodPost, "/rest/api/3/component", componentBody, http.StatusBadRequest)
	second := call(adminID, http.MethodPost, "/rest/api/3/component", `{"projectId":"`+projectID+`","name":"Platform","assigneeType":"PROJECT_LEAD"}`, http.StatusCreated)
	secondID := second["id"].(string)

	created := call(adminID, http.MethodPost, "/rest/api/3/issue", `{"fields":{"project":{"key":"CMP"},"summary":"Uses runtime","issuetype":{"id":"it_task"},"components":[{"id":"`+firstID+`"}]}}`, http.StatusCreated)
	issueKey := created["key"].(string)
	issue := call(adminID, http.MethodGet, "/rest/api/3/issue/"+issueKey, "", http.StatusOK)
	components := issue["fields"].(map[string]any)["components"].([]any)
	if len(components) != 1 || components[0].(map[string]any)["id"] != firstID {
		t.Fatalf("issue components = %#v", issue["fields"])
	}
	if issue["fields"].(map[string]any)["assignee"].(map[string]any)["accountId"] != memberID {
		t.Fatalf("component default assignee = %#v", issue["fields"])
	}
	count := call(memberID, http.MethodGet, "/rest/api/3/component/"+firstID+"/relatedIssueCounts", "", http.StatusOK)
	if count["issueCount"] != float64(1) {
		t.Fatalf("component count = %#v", count)
	}
	page := call(memberID, http.MethodGet, "/rest/api/3/component?projectIds="+projectID+"&maxResults=1", "", http.StatusOK)
	if page["total"] != float64(2) || page["isLast"] != false || page["nextPage"] == "" {
		t.Fatalf("component page = %#v", page)
	}
	projectPage := call(memberID, http.MethodGet, "/rest/api/3/project/CMP/component?maxResults=1&orderBy=-name", "", http.StatusOK)
	if projectPage["total"] != float64(2) || len(projectPage["values"].([]any)) != 1 {
		t.Fatalf("project component page = %#v", projectPage)
	}
	request := httptest.NewRequest(http.MethodGet, "/rest/api/3/project/CMP/components", nil)
	request.SetBasicAuth(memberID+"@example.test", memberID)
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	var all []map[string]any
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &all) != nil || len(all) != 2 {
		t.Fatalf("all project components: %d %s", response.Code, response.Body.String())
	}

	updated := call(adminID, http.MethodPut, "/rest/api/3/component/"+firstID, `{"name":"Runtime services","description":"Owned runtime"}`, http.StatusOK)
	if updated["name"] != "Runtime services" || updated["lead"].(map[string]any)["accountId"] != memberID {
		t.Fatalf("updated component = %#v", updated)
	}
	issue = call(adminID, http.MethodGet, "/rest/api/3/issue/"+issueKey, "", http.StatusOK)
	components = issue["fields"].(map[string]any)["components"].([]any)
	if len(components) != 1 || components[0].(map[string]any)["name"] != "Runtime services" {
		t.Fatalf("component snapshot was not refreshed = %#v", components)
	}
	for _, query := range []string{`component = "Runtime services"`, `components in componentsLeadByUser(` + memberID + `)`, `component in componentsLeadByUser()`} {
		result := call(memberID, http.MethodGet, "/rest/api/3/search/jql?jql="+url.QueryEscape(query), "", http.StatusOK)
		if len(result["issues"].([]any)) != 1 {
			t.Fatalf("%s = %#v", query, result)
		}
	}
	suggestions := call(memberID, http.MethodGet, "/rest/api/3/jql/autocompletedata/suggestions?fieldName=component&fieldValue=runtime", "", http.StatusOK)
	if len(suggestions["results"].([]any)) != 1 {
		t.Fatalf("component suggestions = %#v", suggestions)
	}
	call(adminID, http.MethodDelete, "/rest/api/3/component/"+firstID+"?moveIssuesTo="+secondID, "", http.StatusNoContent)
	call(memberID, http.MethodGet, "/rest/api/3/component/"+firstID, "", http.StatusNotFound)
	count = call(memberID, http.MethodGet, "/rest/api/3/component/"+secondID+"/relatedIssueCounts", "", http.StatusOK)
	if count["issueCount"] != float64(1) {
		t.Fatalf("moved component count = %#v", count)
	}
	call(adminID, http.MethodDelete, "/rest/api/3/component/"+secondID, "", http.StatusNoContent)
	issue = call(adminID, http.MethodGet, "/rest/api/3/issue/"+issueKey, "", http.StatusOK)
	components = issue["fields"].(map[string]any)["components"].([]any)
	if len(components) != 0 {
		t.Fatalf("deleted component remained = %#v", issue["fields"])
	}
}
