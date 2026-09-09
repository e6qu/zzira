package api3

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
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
		for _, sql := range []string{`DELETE FROM groups WHERE directory_id IN (SELECT d.id FROM directories d JOIN sites s ON s.organization_id=d.organization_id WHERE s.workspace_id=$1)`, `DELETE FROM sprint_issues WHERE sprint_id IN (SELECT s.id FROM sprints s JOIN boards b ON b.id=s.board_id JOIN projects p ON p.id=b.project_id WHERE p.workspace_id=$1)`, `DELETE FROM sprints WHERE board_id IN (SELECT b.id FROM boards b JOIN projects p ON p.id=b.project_id WHERE p.workspace_id=$1)`, `DELETE FROM issues WHERE workspace_id=$1`, `DELETE FROM boards WHERE project_id IN (SELECT id FROM projects WHERE workspace_id=$1)`, `DELETE FROM projects WHERE workspace_id=$1`, `DELETE FROM actions WHERE workspace_id=$1`, `DELETE FROM memberships WHERE workspace_id=$1`, `DELETE FROM workspaces WHERE id=$1`} {
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
	var createdIssue struct {
		ID  string
		Key string
	}
	if err := json.Unmarshal(issue.Body.Bytes(), &createdIssue); err != nil {
		t.Fatal(err)
	}
	createdJiraID, err := strconv.ParseInt(createdIssue.ID, 10, 64)
	if err != nil || createdJiraID < 1 {
		t.Fatalf("create issue did not return a numeric Jira id: %s", issue.Body.String())
	}
	assigned, err := st.IssueByIDOrKey(ctx, ws, createdIssue.Key)
	if err != nil || assigned.JiraID != createdJiraID || assigned.Assignee == nil || assigned.Assignee.ID != actor {
		t.Fatalf("default assignee: %v %v", assigned, err)
	}
	byJiraID, err := st.IssueByIDOrKey(ctx, ws, createdIssue.ID)
	if err != nil || byJiraID.ID != assigned.ID {
		t.Fatalf("numeric Jira id lookup: %v %v", byJiraID, err)
	}
	readByJiraID := call(actor, "GET", "/rest/api/3/issue/"+createdIssue.ID, "", 200)
	if !strings.Contains(readByJiraID.Body.String(), `"id":"`+createdIssue.ID+`"`) || strings.Contains(readByJiraID.Body.String(), assigned.ID) {
		t.Fatalf("numeric Jira id was not preserved at the REST boundary: %s", readByJiraID.Body.String())
	}
	document := `{"type":"doc","version":1,"content":[{"type":"heading","attrs":{"level":2},"content":[{"type":"text","text":"Release","marks":[{"type":"strong"}]}]},{"type":"bulletList","content":[{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"Ready"}]}]}]}]}`
	rich := call(actor, "POST", "/rest/api/3/issue", `{"fields":{"project":{"key":"TEAM"},"summary":"Rich content","issuetype":{"name":"Task"},"assignee":null,"labels":["release-ready"],"description":`+document+`}}`, 201)
	if err := json.Unmarshal(rich.Body.Bytes(), &createdIssue); err != nil {
		t.Fatal(err)
	}
	saved, err := st.IssueByIDOrKey(ctx, ws, createdIssue.Key)
	if err != nil || saved.Assignee != nil || !jsonEqual(saved.Description, []byte(document)) {
		t.Fatalf("rich description or explicit unassignment was lost: %v %v", saved, err)
	}
	if _, err := st.SetIssueProperty(ctx, saved.ID, "release.flag", json.RawMessage(`{"ready":true}`)); err != nil {
		t.Fatal(err)
	}
	teamBoards, err := st.BoardsByWorkspace(ctx, ws)
	if err != nil {
		t.Fatal(err)
	}
	var teamBoardID string
	for _, board := range teamBoards {
		if board.ProjectID == saved.ProjectID {
			teamBoardID = board.ID
		}
	}
	if teamBoardID == "" {
		t.Fatal("TEAM board was not provisioned")
	}
	sprintID := store.NewID("spr")
	exec(`INSERT INTO sprints(id,board_id,name,state) VALUES($1,$2,'Current sprint','active')`, sprintID, teamBoardID)
	exec(`INSERT INTO sprint_issues(sprint_id,issue_id) VALUES($1,$2)`, sprintID, saved.ID)
	if _, _, err := st.CreateIssueLink(ctx, actor, ws, "lt_relates", saved.ID, assigned.ID); err != nil {
		t.Fatal(err)
	}
	var directoryID, groupID string
	if err := st.Pool.QueryRow(ctx, `SELECT d.id::text FROM sites s JOIN directories d ON d.organization_id=s.organization_id AND d.active WHERE s.workspace_id=$1 ORDER BY d.created_at LIMIT 1`, ws).Scan(&directoryID); err != nil {
		t.Fatal(err)
	}
	if err := st.Pool.QueryRow(ctx, `INSERT INTO groups(directory_id,name) VALUES($1,'Release managers') RETURNING id::text`, directoryID).Scan(&groupID); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO group_members(group_id,user_id) VALUES($1,$2)`, groupID, actor)
	searchCount := func(query string) int {
		t.Helper()
		response := call(actor, "GET", "/rest/api/3/search/jql?jql="+url.QueryEscape(query), "", 200)
		var result struct{ Issues []json.RawMessage }
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return len(result.Issues)
	}
	for query, want := range map[string]int{
		"id = " + strconv.FormatInt(saved.JiraID, 10): 1,
		"sprint IN openSprints()":                     1,
		`assignee IN membersOf("Release managers")`:   1,
		"issue IN linkedIssues(" + saved.Key + ")":    1,
		"issuetype IN standardIssueTypes()":           2,
	} {
		if got := searchCount(query); got != want {
			t.Fatalf("%s returned %d issues, want %d", query, got, want)
		}
	}
	call(actor, "PUT", "/rest/api/3/issue/"+saved.Key, `{"fields":{"summary":"Rich content revised"}}`, 204)
	call(actor, "POST", "/rest/api/3/issue", `{"fields":{"project":{"key":"TEAM"},"summary":"Invalid content","issuetype":{"name":"Task"},"description":{"type":"paragraph"}}}`, 400)
	autoComplete := call(actor, "GET", "/rest/api/3/jql/autocompletedata", "", 200)
	if !strings.Contains(autoComplete.Body.String(), `"value":"status"`) || !strings.Contains(autoComplete.Body.String(), `"value":"currentUser()"`) {
		t.Fatal(autoComplete.Body.String())
	}
	call(actor, "POST", "/rest/api/3/jql/autocompletedata", `{"includeCollapsedFields":true,"projectIds":[]}`, 200)
	suggestions := call(actor, "GET", "/rest/api/3/jql/autocompletedata/suggestions?fieldName=project&fieldValue=del", "", 200)
	if !strings.Contains(suggestions.Body.String(), `"value":"TEAM"`) || !strings.Contains(suggestions.Body.String(), `\u003cb\u003eDel\u003c/b\u003eivery`) {
		t.Fatal(suggestions.Body.String())
	}
	labelSuggestions := call(actor, "GET", "/rest/api/3/jql/autocompletedata/suggestions?fieldName=labels&fieldValue=ready", "", 200)
	if !strings.Contains(labelSuggestions.Body.String(), `"value":"release-ready"`) {
		t.Fatal(labelSuggestions.Body.String())
	}
	parsed := call(actor, "POST", "/rest/api/3/jql/parse?validation=strict", `{"queries":["status WAS \"To Do\" ORDER BY updated DESC, key ASC","status ="]}`, 200)
	if !strings.Contains(parsed.Body.String(), `"structure"`) || !strings.Contains(parsed.Body.String(), `"errors":["JQL syntax error`) {
		t.Fatal(parsed.Body.String())
	}
	matched := call(actor, "POST", "/rest/api/3/jql/match", `{"issueIds":[`+strconv.FormatInt(saved.JiraID, 10)+`],"jqls":["project=TEAM","project=NEXT","status ="]}`, 200)
	if !strings.Contains(matched.Body.String(), `"matchedIssues":[`+strconv.FormatInt(saved.JiraID, 10)+`]`) || !strings.Contains(matched.Body.String(), `"matchedIssues":[]`) || !strings.Contains(matched.Body.String(), `"errors":["Error in the JQL Query`) {
		t.Fatal(matched.Body.String())
	}
	call(actor, "POST", "/rest/api/3/jql/match", `{"issueIds":["`+saved.ID+`"],"jqls":["project=TEAM"]}`, 400)
	cleaned := call(actor, "POST", "/rest/api/3/jql/pdcleaner", `{"queryStrings":["assignee = currentUser()"]}`, 200)
	if !strings.Contains(cleaned.Body.String(), `"queryStrings":["assignee = currentUser()"]`) {
		t.Fatal(cleaned.Body.String())
	}
	sanitized := call(actor, "POST", "/rest/api/3/jql/sanitize", `{"queries":[{"query":"project=TEAM"},{"accountId":"`+actor+`","query":"unknown = value"}]}`, 200)
	if !strings.Contains(sanitized.Body.String(), `"sanitizedQuery":"project=TEAM"`) || !strings.Contains(sanitized.Body.String(), `"sanitizedQuery":null`) {
		t.Fatal(sanitized.Body.String())
	}
	legacy := call(actor, "GET", "/rest/api/3/search?jql=project%3DTEAM&maxResults=1", "", 200)
	if !strings.Contains(legacy.Body.String(), `"total":2`) || !strings.Contains(legacy.Body.String(), `"maxResults":1`) {
		t.Fatal(legacy.Body.String())
	}
	legacyPost := call(actor, "POST", "/rest/api/3/search", `{"jql":"project=TEAM","startAt":1,"maxResults":1}`, 200)
	if !strings.Contains(legacyPost.Body.String(), `"startAt":1`) || !strings.Contains(legacyPost.Body.String(), `"total":2`) {
		t.Fatal(legacyPost.Body.String())
	}
	expandedSearch := call(actor, "GET", "/rest/api/3/search?jql=key%3D"+saved.Key+"&fields=summary,description&expand=renderedFields,names,schema&properties=release.flag", "", 200)
	var expandedResult struct {
		Expand string
		Names  map[string]string
		Schema map[string]map[string]any
		Issues []map[string]any
	}
	if err := json.Unmarshal(expandedSearch.Body.Bytes(), &expandedResult); err != nil {
		t.Fatal(err)
	}
	expandedIssue := expandedResult.Issues[0]
	expandedFields := expandedIssue["fields"].(map[string]any)
	properties := expandedIssue["properties"].(map[string]any)
	if expandedResult.Expand != "renderedFields,names,schema" || len(expandedFields) != 2 || expandedResult.Names["summary"] != "Summary" || expandedResult.Schema["description"]["type"] != "doc" || properties["release.flag"].(map[string]any)["ready"] != true {
		t.Fatalf("expanded legacy search = %#v", expandedResult)
	}
	renderedFields := expandedIssue["renderedFields"].(map[string]any)
	if renderedFields["description"] == "" || renderedFields["summary"] == nil {
		t.Fatalf("rendered fields = %#v", renderedFields)
	}
	enhancedExpanded := call(actor, "POST", "/rest/api/3/search/jql", `{"jql":"key=`+saved.Key+`","fields":["description"],"expand":"renderedFields,names,schema","properties":["release.flag"]}`, 200)
	var enhancedExpandedResult struct {
		Names  map[string]string
		Schema map[string]map[string]any
		Issues []map[string]any
	}
	if err := json.Unmarshal(enhancedExpanded.Body.Bytes(), &enhancedExpandedResult); err != nil {
		t.Fatal(err)
	}
	enhancedExpandedIssue := enhancedExpandedResult.Issues[0]
	if enhancedExpandedResult.Names["description"] != "Description" || enhancedExpandedResult.Schema["description"]["type"] != "doc" || enhancedExpandedIssue["properties"].(map[string]any)["release.flag"].(map[string]any)["ready"] != true || enhancedExpandedIssue["renderedFields"].(map[string]any)["description"] == "" {
		t.Fatalf("expanded enhanced search = %#v", enhancedExpandedResult)
	}
	lifecycleExpanded := call(actor, "GET", "/rest/api/3/search?jql=key%3D"+saved.Key+"&fields=summary&expand=transitions,operations,editmeta,changelog", "", 200)
	var lifecycleResult struct{ Issues []map[string]any }
	if err := json.Unmarshal(lifecycleExpanded.Body.Bytes(), &lifecycleResult); err != nil {
		t.Fatal(err)
	}
	lifecycleIssue := lifecycleResult.Issues[0]
	changelog := lifecycleIssue["changelog"].(map[string]any)
	operations := lifecycleIssue["operations"].(map[string]any)
	editmeta := lifecycleIssue["editmeta"].(map[string]any)
	if len(lifecycleIssue["transitions"].([]any)) == 0 || changelog["total"].(float64) == 0 || len(operations["linkGroups"].([]any)) == 0 || editmeta["fields"].(map[string]any)["summary"] == nil {
		t.Fatalf("lifecycle expansions = %#v", lifecycleIssue)
	}
	versioned := call(actor, "POST", "/rest/api/3/search/jql", `{"jql":"key=`+saved.Key+`","fields":["summary"],"expand":"versionedRepresentations"}`, 200)
	var versionedResult struct{ Issues []map[string]any }
	if err := json.Unmarshal(versioned.Body.Bytes(), &versionedResult); err != nil {
		t.Fatal(err)
	}
	versionedIssue := versionedResult.Issues[0]
	if versionedIssue["fields"] != nil || versionedIssue["versionedRepresentations"].(map[string]any)["summary"].(map[string]any)["1"] != "Rich content revised" {
		t.Fatalf("versioned representations = %#v", versionedIssue)
	}
	call(actor, "GET", "/rest/api/3/search?properties=1,2,3,4,5,6", "", 400)
	call(actor, "GET", "/rest/api/3/search?fieldsByKeys=maybe", "", 400)
	call(actor, "GET", "/rest/api/3/search?expand=widgets", "", 400)
	call(actor, "POST", "/rest/api/3/search", `{"jql":"project=TEAM","unknown":true}`, 400)
	call(actor, "POST", "/rest/api/3/search/approximate-count", `{"jql":"project=TEAM","unknown":true}`, 400)
	call(actor, "POST", "/rest/api/3/search/approximate-count", `{"jql":""}`, 400)
	counted := call(actor, "POST", "/rest/api/3/search/approximate-count", `{"jql":"project=TEAM"}`, 200)
	if !strings.Contains(counted.Body.String(), `"count":2`) {
		t.Fatal(counted.Body.String())
	}
	reconciled := call(actor, "POST", "/rest/api/3/search/jql", `{"jql":"key=`+saved.Key+`","reconcileIssues":[`+strconv.FormatInt(saved.JiraID, 10)+`]}`, 200)
	if !strings.Contains(reconciled.Body.String(), `"id":"`+strconv.FormatInt(saved.JiraID, 10)+`"`) || strings.Contains(reconciled.Body.String(), saved.ID) {
		t.Fatalf("reconciled search leaked the internal issue id: %s", reconciled.Body.String())
	}
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
	call(actor, "GET", "/rest/api/3/search/jql?jql=project%3DNEXT&maxResults=1&nextPageToken="+enhanced.NextPageToken, "", 400)
	lateIssue := call(actor, "POST", "/rest/api/3/issue", `{"fields":{"project":{"key":"TEAM"},"summary":"Created after search snapshot","issuetype":{"name":"Task"}}}`, 201)
	var late struct{ ID string }
	if err := json.Unmarshal(lateIssue.Body.Bytes(), &late); err != nil {
		t.Fatal(err)
	}
	last := call(actor, "POST", "/rest/api/3/search/jql", `{"jql":"project=TEAM","maxResults":1,"nextPageToken":"`+enhanced.NextPageToken+`"}`, 200)
	enhanced.NextPageToken = ""
	enhanced.Issues = nil
	if err := json.Unmarshal(last.Body.Bytes(), &enhanced); err != nil {
		t.Fatal(err)
	}
	if !enhanced.IsLast || len(enhanced.Issues[0]) != 1 || enhanced.Issues[0]["id"] == nil || enhanced.Issues[0]["id"] == late.ID || enhanced.NextPageToken != "" {
		t.Fatal(last.Body.String())
	}
	reconcilePage := call(actor, "POST", "/rest/api/3/search/jql", `{"jql":"project=TEAM","maxResults":1,"reconcileIssues":[`+strconv.FormatInt(saved.JiraID, 10)+`]}`, 200)
	var reconcileCursor struct{ NextPageToken string }
	if err := json.Unmarshal(reconcilePage.Body.Bytes(), &reconcileCursor); err != nil || reconcileCursor.NextPageToken == "" {
		t.Fatal(reconcilePage.Body.String())
	}
	call(actor, "POST", "/rest/api/3/search/jql", `{"jql":"project=TEAM","maxResults":1,"nextPageToken":"`+reconcileCursor.NextPageToken+`"}`, 400)
	call(actor, "GET", "/rest/api/3/search/jql?jql=project%3DTEAM&nextPageToken=LTE%3D", "", 400)
	call(actor, "GET", "/rest/api/3/search/jql?jql=ORDER%20BY%20key", "", 400)
}
