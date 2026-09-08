package api3

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/store"
)

func TestIssuePropertyLifecycle(t *testing.T) {
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
	workspaceID, actorID := store.NewID("ws"), store.NewID("usr")
	projectID, issueID := store.NewID("prj"), store.NewID("iss")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Issue property test')`, workspaceID)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Property user')`, actorID, actorID+"@example.test")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'member')`, workspaceID, actorID)
	exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, actorID, store.HashToken(actorID))
	exec(`INSERT INTO projects(id,workspace_id,key,name) VALUES($1,$2,'PROP','Properties')`, projectID, workspaceID)
	exec(`INSERT INTO issues(id,workspace_id,project_id,key,summary,status_id,issuetype_id,reporter_id,updated_seq) VALUES($1,$2,$3,'PROP-1','Property issue','st_todo','it_task',$4,0)`, issueID, workspaceID, projectID, actorID)
	t.Cleanup(func() {
		exec(`DELETE FROM issues WHERE id=$1`, issueID)
		exec(`DELETE FROM projects WHERE id=$1`, projectID)
		exec(`DELETE FROM api_tokens WHERE user_id=$1`, actorID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		exec(`DELETE FROM users WHERE id=$1`, actorID)
	})

	h := &Handler{Store: st, WorkspaceSlug: workspaceID, BaseURL: "https://zzira.test"}
	call := func(method, path, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.SetBasicAuth(actorID+"@example.test", actorID)
		response := httptest.NewRecorder()
		h.ServeHTTP(response, r)
		if response.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, response.Code, want, response.Body.String())
		}
		return response
	}

	base := "/rest/api/3/issue/PROP-1/properties"
	if got := call(http.MethodGet, base, "", http.StatusOK).Body.String(); got != "{\"keys\":[]}\n" {
		t.Fatalf("empty keys = %s", got)
	}
	statusKey := "com.atlassian.jira.issue:example.connect:risk:status"
	path := base + "/" + statusKey
	call(http.MethodPut, path, `{"type":"badge","value":{"label":"7"}}`, http.StatusCreated)
	call(http.MethodPut, path, `{"type":"lozenge","value":{"label":"At risk","type":"inprogress"}}`, http.StatusOK)
	call(http.MethodPut, base+"/alpha", `true`, http.StatusCreated)
	call(http.MethodPut, base+"/path%2Fsegment", `42`, http.StatusCreated)

	var property struct {
		Key   string
		Value map[string]any
	}
	if err := json.Unmarshal(call(http.MethodGet, path, "", http.StatusOK).Body.Bytes(), &property); err != nil {
		t.Fatal(err)
	}
	if property.Key != statusKey || property.Value["type"] != "lozenge" {
		t.Fatalf("property = %#v", property)
	}
	var keys struct {
		Keys []struct{ Key, Self string }
	}
	if err := json.Unmarshal(call(http.MethodGet, base, "", http.StatusOK).Body.Bytes(), &keys); err != nil {
		t.Fatal(err)
	}
	if len(keys.Keys) != 3 || keys.Keys[0].Key != "alpha" || keys.Keys[1].Key != statusKey || keys.Keys[2].Key != "path/segment" || !strings.HasPrefix(keys.Keys[1].Self, "https://zzira.test/rest/api/3/issue/PROP-1/properties/") || !strings.HasSuffix(keys.Keys[2].Self, "path%2Fsegment") {
		t.Fatalf("keys = %#v", keys.Keys)
	}

	call(http.MethodPut, base+"/invalid", "", http.StatusBadRequest)
	call(http.MethodPut, base+"/invalid", `{} {}`, http.StatusBadRequest)
	call(http.MethodPut, base+"/invalid", `"`+strings.Repeat("x", issuePropertyValueLimit)+`"`, http.StatusBadRequest)
	call(http.MethodPut, base+"/"+strings.Repeat("k", 256), `true`, http.StatusBadRequest)
	call(http.MethodGet, base+"/missing", "", http.StatusNotFound)
	call(http.MethodDelete, path, "", http.StatusNoContent)
	call(http.MethodDelete, path, "", http.StatusNotFound)
	call(http.MethodPost, base, "", http.StatusMethodNotAllowed)

	request := httptest.NewRequest(http.MethodGet, base, nil)
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status = %d", response.Code)
	}
}
