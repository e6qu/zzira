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

// TestProjectExpansionsAndSearch covers the project bean each project read
// returns: full single-project details, list and recent expansions, and
// project search by action, status, property query and ordering.
func TestProjectExpansionsAndSearch(t *testing.T) {
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
	workspaceID, adminID, memberID := store.NewID("ws"), store.NewID("usr"), store.NewID("usr")
	suffix := time.Now().UnixNano() % 1000000
	alphaKey, betaKey, gammaKey := fmt.Sprintf("AL%06d", suffix), fmt.Sprintf("BE%06d", suffix), fmt.Sprintf("GA%06d", suffix)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Project expansions')`, workspaceID)
	for _, identity := range []struct{ id, role, name string }{{adminID, "admin", "Zed Admin"}, {memberID, "member", "Amy Member"}} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test',$3)`, identity.id, identity.id+"@example.test", identity.name)
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,$3)`, workspaceID, identity.id, identity.role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, identity.id, store.HashToken(identity.id))
	}
	t.Cleanup(func() {
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM boards WHERE project_id IN (SELECT id FROM projects WHERE workspace_id=$1)`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM project_categories WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		for _, id := range []string{adminID, memberID} {
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
			t.Fatalf("%s %s: got %d want %d: %s", method, path, response.Code, want, response.Body.String())
		}
		return response.Body.String()
	}
	decode := func(body string, target any) {
		t.Helper()
		if decodeErr := json.Unmarshal([]byte(body), target); decodeErr != nil {
			t.Fatalf("decode %s: %v", body, decodeErr)
		}
	}

	var category struct {
		ID string `json:"id"`
	}
	decode(call(adminID, http.MethodPost, "/rest/api/3/projectCategory", `{"name":"Delivery `+alphaKey+`","description":"Shipping teams"}`, http.StatusCreated), &category)
	for _, project := range []struct{ key, name, lead string }{{alphaKey, "Alpha", adminID}, {betaKey, "Beta", memberID}, {gammaKey, "Gamma", adminID}} {
		call(adminID, http.MethodPost, "/rest/api/3/project", `{"key":"`+project.key+`","name":"`+project.name+` `+project.key+`","description":"About `+project.name+`","url":"https://example.test/`+project.key+`","projectTypeKey":"software","leadAccountId":"`+project.lead+`","assigneeType":"PROJECT_LEAD"}`, http.StatusCreated)
	}
	call(adminID, http.MethodPut, "/rest/api/3/project/"+betaKey, `{"categoryId":`+category.ID+`}`, http.StatusOK)
	call(adminID, http.MethodPut, "/rest/api/3/project/"+alphaKey+"/properties/team", `{"size":8,"tags":["core","web"]}`, http.StatusCreated)
	call(adminID, http.MethodPost, "/rest/api/3/component", `{"name":"Backend","project":"`+alphaKey+`"}`, http.StatusCreated)
	call(adminID, http.MethodPost, "/rest/api/3/version", `{"name":"1.0","project":"`+alphaKey+`"}`, http.StatusCreated)
	for i := 0; i < 2; i++ {
		call(adminID, http.MethodPost, "/rest/api/3/issue", `{"fields":{"project":{"key":"`+gammaKey+`"},"summary":"Gamma work","issuetype":{"name":"Task"}}}`, http.StatusCreated)
	}

	var full map[string]any
	decode(call(memberID, http.MethodGet, "/rest/api/3/project/"+alphaKey+"?expand=issueTypeHierarchy,projectKeys&properties=team", "", http.StatusOK), &full)
	for _, field := range []string{"description", "lead", "issueTypes", "components", "versions", "roles", "issueTypeHierarchy", "projectKeys", "properties", "url"} {
		if _, ok := full[field]; !ok {
			t.Fatalf("full project lacks %s: %v", field, full)
		}
	}
	if full["expand"] != projectExpandOptions || full["isPrivate"] != false || len(full["components"].([]any)) != 1 || len(full["versions"].([]any)) != 1 {
		t.Fatalf("full project = %v", full)
	}
	if _, leaked := full["lead"].(map[string]any)["emailAddress"]; leaked {
		t.Fatalf("a member sees the lead's email address: %v", full["lead"])
	}
	if team := full["properties"].(map[string]any)["team"].(map[string]any); team["size"] != float64(8) {
		t.Fatalf("selected properties = %v", full["properties"])
	}
	levels := full["issueTypeHierarchy"].(map[string]any)["levels"].([]any)
	if len(levels) == 0 || levels[0].(map[string]any)["issueTypeIds"] == nil {
		t.Fatalf("hierarchy = %v", full["issueTypeHierarchy"])
	}

	var listed []map[string]any
	decode(call(memberID, http.MethodGet, "/rest/api/3/project", "", http.StatusOK), &listed)
	for _, project := range listed {
		if _, ok := project["description"]; ok {
			t.Fatalf("unexpanded list carries description: %v", project)
		}
	}
	decode(call(memberID, http.MethodGet, "/rest/api/3/project?expand=description,lead,projectKeys,issueTypes", "", http.StatusOK), &listed)
	if len(listed) < 3 || listed[0]["description"] == nil || listed[0]["lead"] == nil || listed[0]["projectKeys"] == nil || listed[0]["issueTypes"] == nil {
		t.Fatalf("expanded list = %v", listed)
	}
	decode(call(memberID, http.MethodGet, "/rest/api/3/project?recent=1", "", http.StatusOK), &listed)
	if len(listed) != 1 || listed[0]["key"] != alphaKey {
		t.Fatalf("recent list = %v", listed)
	}

	type page struct {
		Total  int              `json:"total"`
		Values []map[string]any `json:"values"`
	}
	keys := func(p page) string {
		out := []string{}
		for _, value := range p.Values {
			out = append(out, value["key"].(string))
		}
		return strings.Join(out, ",")
	}
	var search page
	decode(call(memberID, http.MethodGet, "/rest/api/3/project/search?query="+fmt.Sprint(suffix)+"&orderBy=-issueCount&expand=insight,lead,url,description", "", http.StatusOK), &search)
	if keys(search) != gammaKey+","+alphaKey+","+betaKey {
		t.Fatalf("issue count ordering = %s", keys(search))
	}
	insight := search.Values[0]["insight"].(map[string]any)
	if insight["totalIssueCount"] != float64(2) || !strings.Contains(insight["lastIssueUpdateTime"].(string), "T") {
		t.Fatalf("insight = %v", insight)
	}
	decode(call(memberID, http.MethodGet, "/rest/api/3/project/search?query="+fmt.Sprint(suffix)+"&orderBy=owner", "", http.StatusOK), &search)
	if !strings.HasPrefix(keys(search), betaKey+",") {
		t.Fatalf("owner ordering = %s", keys(search))
	}
	decode(call(memberID, http.MethodGet, "/rest/api/3/project/search?query="+fmt.Sprint(suffix)+"&orderBy=-category", "", http.StatusOK), &search)
	if !strings.HasPrefix(keys(search), betaKey+",") {
		t.Fatalf("category ordering = %s", keys(search))
	}
	decode(call(memberID, http.MethodGet, "/rest/api/3/project/search?propertyQuery=[team].size=8&properties=team", "", http.StatusOK), &search)
	if keys(search) != alphaKey || search.Values[0]["properties"].(map[string]any)["team"] == nil {
		t.Fatalf("property query = %v", search.Values)
	}
	decode(call(memberID, http.MethodGet, "/rest/api/3/project/search?propertyQuery=[team].tags=web", "", http.StatusOK), &search)
	if keys(search) != alphaKey {
		t.Fatalf("array property query = %s", keys(search))
	}
	call(memberID, http.MethodGet, "/rest/api/3/project/search?propertyQuery=team.size=8", "", http.StatusBadRequest)
	decode(call(memberID, http.MethodGet, "/rest/api/3/project/search?query="+fmt.Sprint(suffix)+"&action=edit", "", http.StatusOK), &search)
	// The lead joins the project's Administrators role, which holds Administer
	// Projects in the default scheme.
	if keys(search) != betaKey {
		t.Fatalf("a member edits %s", keys(search))
	}
	decode(call(adminID, http.MethodGet, "/rest/api/3/project/search?query="+fmt.Sprint(suffix)+"&action=edit", "", http.StatusOK), &search)
	if search.Total != 3 {
		t.Fatalf("an administrator edits %s", keys(search))
	}
	call(memberID, http.MethodGet, "/rest/api/3/project/search?action=destroy", "", http.StatusBadRequest)

	call(adminID, http.MethodPost, "/rest/api/3/project/"+betaKey+"/archive", "", http.StatusNoContent)
	decode(call(memberID, http.MethodGet, "/rest/api/3/project/search?query="+fmt.Sprint(suffix)+"&status=archived", "", http.StatusOK), &search)
	if search.Total != 0 {
		t.Fatalf("a member sees archived %s", keys(search))
	}
	decode(call(adminID, http.MethodGet, "/rest/api/3/project/search?query="+fmt.Sprint(suffix)+"&status=archived&status=live&orderBy=archivedDate", "", http.StatusOK), &search)
	if search.Total != 3 || search.Values[len(search.Values)-1]["key"] != betaKey || search.Values[len(search.Values)-1]["archived"] != true || search.Values[len(search.Values)-1]["archivedBy"] == nil {
		t.Fatalf("archived search = %v", search.Values)
	}
	call(adminID, http.MethodGet, "/rest/api/3/project/search?status=gone", "", http.StatusBadRequest)

	var recent []map[string]any
	decode(call(memberID, http.MethodGet, "/rest/api/3/project/recent?expand=insight,permissions", "", http.StatusOK), &recent)
	if len(recent) != 1 || recent[0]["permissions"].(map[string]any)["canEdit"] != false {
		t.Fatalf("recent projects = %v", recent)
	}
}
