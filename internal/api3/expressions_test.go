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

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
)

// TestJiraExpressionsContract pins analysis, evaluation with Jira context
// variables, JQL-loaded issues in both paging styles, custom variables,
// complexity metadata and Jira's error answers.
func TestJiraExpressionsContract(t *testing.T) {
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
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	ws, actor, projectID := store.NewID("ws"), store.NewID("usr"), store.NewID("project")
	epicID, storyID := store.NewID("iss"), store.NewID("iss")
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Expressions')`, ws)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Exa Evaluator')`, actor, actor+"@example.test")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'admin')`, ws, actor)
	exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, actor, store.HashToken(actor))
	exec(`INSERT INTO projects(id,workspace_id,key,name,workflow_id,lead_account_id,project_type_key) VALUES($1,$2,'EXP','Expressions','wf_default',$3,'software')`, projectID, ws, actor)
	exec(`INSERT INTO issues(id,workspace_id,project_id,key,summary,status_id,issuetype_id,reporter_id,labels,updated_seq) VALUES($1,$2,$3,'EXP-1','Launch','st_todo','it_epic',$4,'{}',0)`, epicID, ws, projectID, actor)
	exec(`INSERT INTO issues(id,workspace_id,project_id,key,summary,status_id,issuetype_id,reporter_id,parent_id,labels,updated_seq) VALUES($1,$2,$3,'EXP-2','Sign-up','st_todo','it_story',$4,$5,'{web,growth}',0)`, storyID, ws, projectID, actor, epicID)
	exec(`UPDATE projects SET issue_seq=2 WHERE id=$1`, projectID)
	t.Cleanup(func() {
		for _, query := range []string{
			`DELETE FROM issues WHERE workspace_id=$1`, `DELETE FROM projects WHERE workspace_id=$1`, `DELETE FROM actions WHERE workspace_id=$1`,
			`DELETE FROM memberships WHERE workspace_id=$1`, `DELETE FROM custom_fields WHERE workspace_id=$1`, `DELETE FROM workspaces WHERE id=$1`,
		} {
			exec(query, ws)
		}
		exec(`DELETE FROM api_tokens WHERE user_id=$1`, actor)
		exec(`DELETE FROM users WHERE id=$1`, actor)
	})
	h := &Handler{Store: st, Commands: &commands.Service{Store: st}, BaseURL: "https://zzira.test", WorkspaceSlug: ws}
	call := func(path, body string, want int, anonymous bool) map[string]any {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		if !anonymous {
			request.SetBasicAuth(actor+"@example.test", actor)
		}
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("POST %s %s: got %d want %d: %s", path, body, response.Code, want, response.Body.String())
		}
		out := map[string]any{}
		if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
			t.Fatal(response.Body.String(), err)
		}
		return out
	}
	asJSON := func(value any) string {
		raw, _ := json.Marshal(value)
		return string(raw)
	}
	call("/rest/api/3/issue/EXP-2/comment", `{"body":{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"Looks good"}]}]}}`, http.StatusCreated, false)

	// Analysis: syntax, experimental types and complexity formulae.
	syntax := call("/rest/api/3/expression/analyse", `{"expressions":["issue.key","issue.key +"]}`, http.StatusOK, true)["results"].([]any)
	if syntax[0].(map[string]any)["valid"] != true || syntax[1].(map[string]any)["valid"] != false ||
		syntax[1].(map[string]any)["errors"].([]any)[0].(map[string]any)["type"] != "syntax" {
		t.Fatal(syntax)
	}
	typed := call("/rest/api/3/expression/analyse?check=type", `{"expressions":["issue.comments.map(c => c.body.plainText)","ticket.nope"],"contextVariables":{"ticket":"Issue"}}`, http.StatusOK, false)["results"].([]any)
	if typed[0].(map[string]any)["type"] != "List<String>" || typed[1].(map[string]any)["valid"] != false {
		t.Fatal(typed)
	}
	complexity := call("/rest/api/3/expression/analyse?check=complexity", `{"expressions":["issues.map(i => i.comments)"]}`, http.StatusOK, false)["results"].([]any)
	if formula := complexity[0].(map[string]any)["complexity"].(map[string]any); formula["expensiveOperations"] != "N" || formula["variables"].(map[string]any)["N"] != "issues" {
		t.Fatal(complexity)
	}
	call("/rest/api/3/expression/analyse?check=lint", `{"expressions":["1"]}`, http.StatusBadRequest, false)

	// Evaluation against an issue, with complexity metadata.
	evaluated := call("/rest/api/3/expression/eval?expand=meta.complexity", `{
		"expression": "{ key: issue.key, epic: issue.epic.key, isEpic: issue.epic.isEpic, comments: issue.comments.map(c => c.body.plainText), type: issue.issueType.name, category: issue.status.category.key, labels: issue.labels, me: user.accountId, project: issue.project.key, stories: issue.epic.stories.map(s => s.key) }",
		"context": {"issue": {"key": "EXP-2"}}
	}`, http.StatusOK, false)
	value := evaluated["value"].(map[string]any)
	if value["key"] != "EXP-2" || value["epic"] != "EXP-1" || value["isEpic"] != true || asJSON(value["comments"]) != `["Looks good"]` ||
		value["type"] != "Story" || value["category"] != "new" || asJSON(value["labels"]) != `["web","growth"]` || value["me"] != actor ||
		value["project"] != "EXP" || asJSON(value["stories"]) != `["EXP-2"]` {
		t.Fatal(evaluated)
	}
	used := evaluated["meta"].(map[string]any)["complexity"].(map[string]any)
	if fmt.Sprint(used["steps"].(map[string]any)["limit"]) != "10000" || fmt.Sprint(used["expensiveOperations"].(map[string]any)["value"]) != "3" {
		t.Fatal(used)
	}
	if bean := call("/rest/api/3/expression/eval", `{"expression":"issue","context":{"issue":{"key":"EXP-1"}}}`, http.StatusOK, false)["value"].(map[string]any); bean["key"] != "EXP-1" {
		t.Fatal(bean)
	}

	// JQL-loaded issues, paged by startAt and by token.
	paged := call("/rest/api/3/expression/eval", `{"expression":"issues.map(i => i.key)","context":{"issues":{"jql":{"query":"project = EXP ORDER BY key ASC","startAt":0,"maxResults":1}}}}`, http.StatusOK, false)
	if asJSON(paged["value"]) != `["EXP-1"]` || asJSON(paged["meta"]) != `{"issues":{"jql":{"count":1,"maxResults":1,"startAt":0,"totalCount":2}}}` {
		t.Fatal(paged)
	}
	scrolled := call("/rest/api/3/expression/evaluate", `{"expression":"issues.map(i => i.key)","context":{"issues":{"jql":{"query":"project = EXP ORDER BY key ASC","maxResults":1}}}}`, http.StatusOK, false)
	pageMeta := scrolled["meta"].(map[string]any)["issues"].(map[string]any)["jql"].(map[string]any)
	token, _ := pageMeta["nextPageToken"].(string)
	if asJSON(scrolled["value"]) != `["EXP-1"]` || pageMeta["isLast"] != false || token == "" {
		t.Fatal(scrolled)
	}
	last := call("/rest/api/3/expression/evaluate", `{"expression":"issues.map(i => i.key)","context":{"issues":{"jql":{"query":"project = EXP ORDER BY key ASC","maxResults":1,"nextPageToken":"`+token+`"}}}}`, http.StatusOK, false)
	if asJSON(last["value"]) != `["EXP-2"]` || last["meta"].(map[string]any)["issues"].(map[string]any)["jql"].(map[string]any)["isLast"] != true {
		t.Fatal(last)
	}
	call("/rest/api/3/expression/eval", `{"expression":"issues","context":{"issues":{"jql":{"query":"project in ("}}}}`, http.StatusBadRequest, false)
	if warned := call("/rest/api/3/expression/eval", `{"expression":"issues.length","context":{"issues":{"jql":{"query":"project in (","validation":"warn"}}}}`, http.StatusOK, false); warned["value"] != float64(0) ||
		len(warned["meta"].(map[string]any)["issues"].(map[string]any)["jql"].(map[string]any)["validationWarnings"].([]any)) != 1 {
		t.Fatal(warned)
	}

	// Custom variables.
	custom := call("/rest/api/3/expression/eval", `{"expression":"config.limit + me.displayName.length + tickets.length","context":{"custom":[
		{"type":"json","key":"config","value":{"limit":3}},
		{"type":"user","key":"me","accountId":"`+actor+`"},
		{"type":"list","key":"tickets","value":[{"type":"json","value":1},{"type":"user","accountId":"`+actor+`"}]}
	]}}`, http.StatusOK, false)
	if custom["value"] != float64(3+len("Exa Evaluator")+2) {
		t.Fatal(custom)
	}

	// Anonymous evaluation sees no user and no issues.
	if anonymous := call("/rest/api/3/expression/eval", `{"expression":"user == null && 1 + 1 == 2"}`, http.StatusOK, true); anonymous["value"] != true {
		t.Fatal(anonymous)
	}
	call("/rest/api/3/expression/eval", `{"expression":"issue.key","context":{"issue":{"key":"EXP-1"}}}`, http.StatusNotFound, true)

	// Jira's error answers.
	call("/rest/api/3/expression/eval", `{"expression":"issue.key","context":{"issue":{"id":1,"key":"EXP-1"}}}`, http.StatusBadRequest, false)
	call("/rest/api/3/expression/eval", `{"expression":"issue.key","context":{"issue":{"key":"NOPE-1"}}}`, http.StatusNotFound, false)
	call("/rest/api/3/expression/eval", `{"expression":"issue.key","context":{"sprint":999999999}}`, http.StatusNotFound, false)
	if failed := call("/rest/api/3/expression/eval", `{"expression":"issue.nope","context":{"issue":{"key":"EXP-1"}}}`, http.StatusBadRequest, false); !strings.Contains(asJSON(failed), "Unrecognized property") {
		t.Fatal(failed)
	}
	if failed := call("/rest/api/3/expression/eval", `{"expression":"1 +"}`, http.StatusBadRequest, false); !strings.Contains(asJSON(failed), "syntax error at line 1") {
		t.Fatal(failed)
	}
	call("/rest/api/3/expression/eval", `{"expression":"1","unexpected":true}`, http.StatusBadRequest, false)
	if limited := call("/rest/api/3/expression/eval", `{"expression":"[1,2,3,4,5,6,7,8,9,10,11].map(n => new Issue('EXP-1'))"}`, http.StatusBadRequest, false); !strings.Contains(asJSON(limited), "expensive operations") {
		t.Fatal(limited)
	}
}
