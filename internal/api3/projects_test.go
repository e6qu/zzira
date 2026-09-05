package api3

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

func TestProjectSearchSelection(t *testing.T) {
	projects := []*models.Project{{ID: "1", Key: "AA", Name: "Zebra"}, {ID: "2", Key: "BB", Name: "Alpha"}, {ID: "3", Key: "CC", Name: "Alpine"}}
	for _, tt := range []struct {
		query, want string
		bad         bool
	}{
		{"query=al&orderBy=-name", "CC,BB", false}, {"keys=AA&keys=CC", "AA,CC", false},
		{"id=2", "BB", false}, {"typeKey=business", "", false}, {"orderBy=owner", "", true}, {"categoryId=1", "", true},
	} {
		got, err := filterProjects(httptest.NewRequest("GET", "/rest/api/3/project/search?"+tt.query, nil), projects)
		if (err != nil) != tt.bad {
			t.Fatalf("%s: %v", tt.query, err)
		}
		keys := []string{}
		for _, p := range got {
			keys = append(keys, p.Key)
		}
		if strings.Join(keys, ",") != tt.want {
			t.Fatalf("%s: got %v", tt.query, keys)
		}
	}
}

func TestProjectAPILifecycle(t *testing.T) {
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
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES ($1,$1,'Project API test')`, ws)
	for _, id := range []string{actor, member} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES ($1,$2,'test','Test User')`, id, id+"@example.test")
		role := "member"
		if id == actor {
			role = "admin"
		}
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES ($1,$2,$3)`, ws, id, role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES ($1,$1,$2)`, id, store.HashToken(id))
	}
	t.Cleanup(func() {
		for _, sql := range []string{`DELETE FROM issues WHERE workspace_id=$1`, `DELETE FROM boards WHERE project_id IN (SELECT id FROM projects WHERE workspace_id=$1)`, `DELETE FROM projects WHERE workspace_id=$1`, `DELETE FROM actions WHERE workspace_id=$1`, `DELETE FROM memberships WHERE workspace_id=$1`, `DELETE FROM workspaces WHERE id=$1`} {
			exec(sql, ws)
		}
		for _, id := range []string{actor, member} {
			exec(`DELETE FROM api_tokens WHERE user_id=$1`, id)
			exec(`DELETE FROM users WHERE id=$1`, id)
		}
	})
	h := &Handler{Store: st, Commands: &commands.Service{Store: st}, WorkspaceSlug: ws, BaseURL: "https://zzira.test"}
	call := func(user, method, path, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.SetBasicAuth(user+"@example.test", user)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, w.Code, want, w.Body.String())
		}
		return w
	}
	body := `{"key":"TEAM","name":"Delivery","projectTypeKey":"software","leadAccountId":"` + actor + `","assigneeType":"PROJECT_LEAD"}`
	call(member, "POST", "/rest/api/3/project", body, 403)
	created := call(actor, "POST", "/rest/api/3/project", body, 201)
	var p struct {
		ID  int64
		Key string
	}
	if err := json.Unmarshal(created.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.ID <= 0 || p.Key != "TEAM" {
		t.Fatal(created.Body.String())
	}
	call(actor, "POST", "/rest/api/3/project", body, 400)
	call(actor, "PUT", "/rest/api/3/project/TEAM", `{"description":"Release planning","url":"https://example.test","name":"Delivery team"}`, 200)
	got := call(actor, "GET", "/rest/api/3/project/TEAM", "", 200)
	if !strings.Contains(got.Body.String(), `"description":"Release planning"`) || !strings.Contains(got.Body.String(), `"accountId":"`+actor+`"`) {
		t.Fatal(got.Body.String())
	}
	call(member, "PUT", "/rest/api/3/project/TEAM", `{"name":"Changed"}`, 403)
	call(actor, "PUT", "/rest/api/3/project/TEAM", `{"url":"javascript:alert(1)"}`, 400)
	call(actor, "PUT", "/rest/api/3/project/TEAM", `{"permissionScheme":1}`, 400)
	call(actor, "PUT", "/rest/api/3/project/TEAM", `{"name":"Changed"} {}`, 400)
	call(actor, "PUT", "/rest/api/3/project/MISSING", `{"name":"Changed"}`, 404)
	call(actor, "POST", "/rest/api/3/project", strings.ReplaceAll(body, "TEAM", "NEXT"), 201)
	page := call(actor, "GET", "/rest/api/3/project/search?maxResults=1&orderBy=key", "", 200)
	var result struct {
		Total    int
		IsLast   bool
		NextPage string
		Values   []models.Project
	}
	if err := json.Unmarshal(page.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Total != 2 || result.IsLast || len(result.Values) != 1 || result.Values[0].Key != "NEXT" || result.NextPage == "" {
		t.Fatal(page.Body.String())
	}
	call(actor, "GET", "/rest/api/3/project/search?maxResults=-1", "", 400)
	empty := call(actor, "GET", "/rest/api/3/project/search?startAt=999999", "", 200)
	if !strings.Contains(empty.Body.String(), `"values":[]`) {
		t.Fatal(empty.Body.String())
	}
	boards, err := st.BoardsByWorkspace(ctx, ws)
	if err != nil || len(boards) != 2 {
		t.Fatalf("boards %v: %v", boards, err)
	}
	for _, board := range boards {
		if _, err := st.BoardIssues(ctx, board.ID, actor); err != nil {
			t.Fatalf("new board cannot render its project filter: %v", err)
		}
	}
	var count int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM actions WHERE workspace_id=$1 AND entity_type='project'`, ws).Scan(&count); err != nil || count != 3 {
		t.Fatalf("project audit count=%d: %v", count, err)
	}
	issue := call(actor, "POST", "/rest/api/3/issue", `{"fields":{"project":{"key":"TEAM"},"summary":"Assigned by default","issuetype":{"name":"Task"}}}`, 201)
	var createdIssue struct{ Key string }
	if err := json.Unmarshal(issue.Body.Bytes(), &createdIssue); err != nil {
		t.Fatal(err)
	}
	assigned, err := st.IssueByIDOrKey(ctx, ws, createdIssue.Key)
	if err != nil || assigned.Assignee == nil || assigned.Assignee.ID != actor {
		t.Fatalf("default assignee: %v %v", assigned, err)
	}
	document := `{"type":"doc","version":1,"content":[{"type":"heading","attrs":{"level":2},"content":[{"type":"text","text":"Release","marks":[{"type":"strong"}]}]},{"type":"bulletList","content":[{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"Ready"}]}]}]}]}`
	rich := call(actor, "POST", "/rest/api/3/issue", `{"fields":{"project":{"key":"TEAM"},"summary":"Rich content","issuetype":{"name":"Task"},"assignee":null,"description":`+document+`}}`, 201)
	if err := json.Unmarshal(rich.Body.Bytes(), &createdIssue); err != nil {
		t.Fatal(err)
	}
	saved, err := st.IssueByIDOrKey(ctx, ws, createdIssue.Key)
	if err != nil || saved.Assignee != nil || !jsonEqual(saved.Description, []byte(document)) {
		t.Fatalf("rich description or explicit unassignment was lost: %v %v", saved, err)
	}
	call(actor, "POST", "/rest/api/3/issue", `{"fields":{"project":{"key":"TEAM"},"summary":"Invalid content","issuetype":{"name":"Task"},"description":{"type":"paragraph"}}}`, 400)
	search := call(actor, "GET", "/rest/api/3/search/jql?jql=project%3DTEAM&maxResults=1&fields=summary", "", 200)
	var enhanced struct {
		Issues        []map[string]any
		IsLast        bool
		NextPageToken string
	}
	if err := json.Unmarshal(search.Body.Bytes(), &enhanced); err != nil {
		t.Fatal(err)
	}
	if enhanced.IsLast || enhanced.NextPageToken == "" || len(enhanced.Issues) != 1 {
		t.Fatal(search.Body.String())
	}
	selected := enhanced.Issues[0]["fields"].(map[string]any)
	if len(selected) != 1 || selected["summary"] == nil {
		t.Fatal(search.Body.String())
	}
	last := call(actor, "POST", "/rest/api/3/search/jql", `{"jql":"project=TEAM","maxResults":1,"nextPageToken":"`+enhanced.NextPageToken+`"}`, 200)
	enhanced.NextPageToken = ""
	enhanced.Issues = nil
	if err := json.Unmarshal(last.Body.Bytes(), &enhanced); err != nil {
		t.Fatal(err)
	}
	if !enhanced.IsLast || len(enhanced.Issues[0]) != 1 || enhanced.Issues[0]["id"] == nil || enhanced.NextPageToken != "" {
		t.Fatal(last.Body.String())
	}
	call(actor, "GET", "/rest/api/3/search/jql?jql=project%3DTEAM&nextPageToken=LTE%3D", "", 400)
	call(actor, "GET", "/rest/api/3/search/jql?jql=ORDER%20BY%20key", "", 400)
}
