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

// TestDueDate covers Jira's Due date system field: set on create, edited as a
// field, with the update operation and during a transition, cleared with
// null, validated, recorded in the changelog, described by the metadata
// resources and searchable with JQL.
func TestDueDate(t *testing.T) {
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
	projectKey := fmt.Sprintf("DD%06d", time.Now().UnixNano()%1000000)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Due dates')`, workspaceID)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test',$1)`, adminID, adminID+"@example.test")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'admin')`, workspaceID, adminID)
	exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, adminID, store.HashToken(adminID))
	t.Cleanup(func() {
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM boards WHERE project_id IN (SELECT id FROM projects WHERE workspace_id=$1)`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		exec(`DELETE FROM api_tokens WHERE user_id=$1`, adminID)
		exec(`DELETE FROM users WHERE id=$1`, adminID)
	})
	h := &Handler{Store: st, Commands: &commands.Service{Store: st}, WorkspaceSlug: workspaceID, BaseURL: "https://zzira.test"}
	call := func(method, path, body string, want int) map[string]any {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.SetBasicAuth(adminID+"@example.test", adminID)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, response.Code, want, response.Body.String())
		}
		decoded := map[string]any{}
		_ = json.Unmarshal(response.Body.Bytes(), &decoded)
		return decoded
	}
	dueDate := func(key string) any {
		t.Helper()
		return call(http.MethodGet, "/rest/api/3/issue/"+key, "", http.StatusOK)["fields"].(map[string]any)["duedate"]
	}

	call(http.MethodPost, "/rest/api/3/project", `{"key":"`+projectKey+`","name":"Due `+projectKey+`","projectTypeKey":"software","leadAccountId":"`+adminID+`","assigneeType":"PROJECT_LEAD"}`, http.StatusCreated)
	create := func(extra string, want int) map[string]any {
		t.Helper()
		return call(http.MethodPost, "/rest/api/3/issue", `{"fields":{"project":{"key":"`+projectKey+`"},"summary":"Dated work","issuetype":{"name":"Task"}`+extra+`}}`, want)
	}

	// Creating takes a yyyy-MM-dd day and refuses anything else.
	if errors := create(`,"duedate":"next week"`, http.StatusBadRequest)["errors"].(map[string]any); errors["duedate"] != "Error parsing date string: next week" {
		t.Fatalf("bad due date errors = %v", errors)
	}
	create(`,"duedate":42`, http.StatusBadRequest)
	key := create(`,"duedate":"2026-10-01"`, http.StatusCreated)["key"].(string)
	if got := dueDate(key); got != "2026-10-01" {
		t.Fatalf("created due date = %v", got)
	}
	undated := create(``, http.StatusCreated)["key"].(string)
	if got, present := call(http.MethodGet, "/rest/api/3/issue/"+undated, "", http.StatusOK)["fields"].(map[string]any)["duedate"]; !present || got != nil {
		t.Fatalf("undated due date = %v present=%v", got, present)
	}

	// Editing sets it as a field or with set, and null clears it.
	call(http.MethodPut, "/rest/api/3/issue/"+key, `{"fields":{"duedate":"2026-10-15"}}`, http.StatusNoContent)
	if got := dueDate(key); got != "2026-10-15" {
		t.Fatalf("edited due date = %v", got)
	}
	call(http.MethodPut, "/rest/api/3/issue/"+key, `{"update":{"duedate":[{"set":"2026-11-02"}]}}`, http.StatusNoContent)
	if got := dueDate(key); got != "2026-11-02" {
		t.Fatalf("set due date = %v", got)
	}
	call(http.MethodPut, "/rest/api/3/issue/"+key, `{"update":{"duedate":[{"add":"2026-11-02"}]}}`, http.StatusBadRequest)
	call(http.MethodPut, "/rest/api/3/issue/"+key, `{"fields":{"duedate":"2026-11-03"},"update":{"duedate":[{"set":"2026-11-04"}]}}`, http.StatusBadRequest)
	call(http.MethodPut, "/rest/api/3/issue/"+key, `{"fields":{"duedate":"02/11/2026"}}`, http.StatusBadRequest)
	call(http.MethodPut, "/rest/api/3/issue/"+undated, `{"fields":{"duedate":"2026-09-20"}}`, http.StatusNoContent)
	call(http.MethodPut, "/rest/api/3/issue/"+undated, `{"fields":{"duedate":null}}`, http.StatusNoContent)
	if got := dueDate(undated); got != nil {
		t.Fatalf("cleared due date = %v", got)
	}

	// A transition sets it too, and refuses a value that is not a day.
	transitions := call(http.MethodGet, "/rest/api/3/issue/"+undated+"/transitions", "", http.StatusOK)["transitions"].([]any)
	if len(transitions) == 0 {
		t.Fatal("no transitions available")
	}
	transitionID := transitions[0].(map[string]any)["id"].(string)
	call(http.MethodPost, "/rest/api/3/issue/"+undated+"/transitions", `{"transition":{"id":"`+transitionID+`"},"fields":{"duedate":"tomorrow"}}`, http.StatusBadRequest)
	call(http.MethodPost, "/rest/api/3/issue/"+undated+"/transitions", `{"transition":{"id":"`+transitionID+`"},"fields":{"duedate":"2026-12-24"}}`, http.StatusNoContent)
	if got := dueDate(undated); got != "2026-12-24" {
		t.Fatalf("transitioned due date = %v", got)
	}

	// The changelog records each change as Jira does.
	histories := call(http.MethodGet, "/rest/api/3/issue/"+key+"/changelog", "", http.StatusOK)["values"].([]any)
	found := false
	for _, history := range histories {
		for _, item := range history.(map[string]any)["items"].([]any) {
			change := item.(map[string]any)
			if change["field"] == "duedate" && change["from"] == "2026-10-15" && change["to"] == "2026-11-02" {
				found = change["fieldtype"] == "jira" && change["toString"] == "2026-11-02 00:00:00.0"
			}
		}
	}
	if !found {
		t.Fatalf("changelog = %v", histories)
	}

	// Metadata describes the system field.
	editFields := call(http.MethodGet, "/rest/api/3/issue/"+key+"/editmeta", "", http.StatusOK)["fields"].(map[string]any)
	if field, ok := editFields["duedate"].(map[string]any); !ok || field["name"] != "Due date" || fmt.Sprint(field["schema"]) != "map[system:duedate type:date]" {
		t.Fatalf("editmeta duedate = %v", editFields["duedate"])
	}

	// JQL compares the day.
	result := call(http.MethodPost, "/rest/api/3/search/jql", `{"jql":"project = `+projectKey+` AND duedate <= \"2026-11-02\" AND duedate >= \"2026-11-01\"","fields":["duedate"]}`, http.StatusOK)
	issues := result["issues"].([]any)
	if len(issues) != 1 || issues[0].(map[string]any)["key"] != key {
		t.Fatalf("due date search = %v", result)
	}
	// Both work items now have a due date, so none is empty.
	if empty := call(http.MethodPost, "/rest/api/3/search/jql", `{"jql":"project = `+projectKey+` AND duedate is EMPTY"}`, http.StatusOK)["issues"].([]any); len(empty) != 0 {
		t.Fatalf("undated search = %v", empty)
	}
	if dated := call(http.MethodPost, "/rest/api/3/search/jql", `{"jql":"project = `+projectKey+` AND due is not EMPTY"}`, http.StatusOK)["issues"].([]any); len(dated) != 2 {
		t.Fatalf("dated search = %v", dated)
	}
}
