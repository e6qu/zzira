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

func TestCustomFieldContextContract(t *testing.T) {
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
	stamp := time.Now().UnixNano() % 100000000
	keyA, keyB := fmt.Sprintf("X%08d", stamp), fmt.Sprintf("Y%08d", stamp)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Field context contract')`, workspaceID)
	for _, identity := range []struct{ id, role string }{{adminID, "admin"}, {memberID, "member"}} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test',$3)`, identity.id, identity.id+"@example.test", "Context "+identity.id)
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,$3)`, workspaceID, identity.id, identity.role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, identity.id, store.HashToken(identity.id))
	}
	t.Cleanup(func() {
		exec(`DELETE FROM custom_fields WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		for _, id := range []string{adminID, memberID} {
			exec(`DELETE FROM api_tokens WHERE user_id=$1`, id)
			exec(`DELETE FROM users WHERE id=$1`, id)
		}
	})

	h := &Handler{Store: st, Commands: &commands.Service{Store: st}, WorkspaceSlug: workspaceID, BaseURL: "https://zzira.test"}
	call := func(user, method, path, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		if user != "" {
			request.SetBasicAuth(user+"@example.test", user)
		}
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, response.Code, want, response.Body.String())
		}
		return response
	}
	decodeID := func(response *httptest.ResponseRecorder) string {
		t.Helper()
		var wire struct {
			ID json.RawMessage `json:"id"`
		}
		if err = json.Unmarshal(response.Body.Bytes(), &wire); err != nil || len(wire.ID) == 0 {
			t.Fatalf("id=%s err=%v body=%s", wire.ID, err, response.Body.String())
		}
		return strings.Trim(string(wire.ID), `"`)
	}

	projectA := decodeID(call(adminID, http.MethodPost, "/rest/api/3/project", `{"key":"`+keyA+`","name":"Alpha","projectTypeKey":"software","leadAccountId":"`+adminID+`","assigneeType":"PROJECT_LEAD"}`, http.StatusCreated))
	projectB := decodeID(call(adminID, http.MethodPost, "/rest/api/3/project", `{"key":"`+keyB+`","name":"Beta","projectTypeKey":"software","leadAccountId":"`+adminID+`","assigneeType":"PROJECT_LEAD"}`, http.StatusCreated))
	fieldID := decodeID(call(adminID, http.MethodPost, "/rest/api/3/field", `{"name":"Release note","type":"text"}`, http.StatusCreated))
	contextPath := "/rest/api/3/field/" + fieldID + "/context"

	call(memberID, http.MethodGet, contextPath, "", http.StatusForbidden)
	call(adminID, http.MethodGet, "/rest/api/3/field/customfield_missing/context", "", http.StatusNotFound)

	// A new custom field is provisioned with one global context.
	listed := call(adminID, http.MethodGet, contextPath, "", http.StatusOK)
	var page struct {
		Total  int `json:"total"`
		Values []struct {
			ID              int64 `json:"id"`
			IsGlobalContext bool  `json:"isGlobalContext"`
			IsAnyIssueType  bool  `json:"isAnyIssueType"`
		} `json:"values"`
	}
	if err = json.Unmarshal(listed.Body.Bytes(), &page); err != nil || page.Total != 1 ||
		!page.Values[0].IsGlobalContext || !page.Values[0].IsAnyIssueType {
		t.Fatalf("page=%+v err=%v body=%s", page, err, listed.Body.String())
	}
	globalContext := fmt.Sprint(page.Values[0].ID)

	// The field applies everywhere while only the global context exists.
	fieldsFor := func(projectKey, issueType string) []string {
		t.Helper()
		response := call(adminID, http.MethodGet, "/rest/api/3/issue/createmeta/"+projectKey+"/issuetypes/"+issueType+"?maxResults=60", "", http.StatusOK)
		var meta struct {
			Fields []struct {
				FieldID string `json:"fieldId"`
			} `json:"fields"`
		}
		if err = json.Unmarshal(response.Body.Bytes(), &meta); err != nil {
			t.Fatal(err)
		}
		ids := []string{}
		for _, field := range meta.Fields {
			ids = append(ids, field.FieldID)
		}
		return ids
	}
	if !contains(fieldsFor(keyA, "it_task"), fieldID) || !contains(fieldsFor(keyB, "it_task"), fieldID) {
		t.Fatal("global context should reach both projects")
	}

	// A second overlapping global context is refused: resolution must stay
	// unambiguous.
	call(adminID, http.MethodPost, contextPath, `{"name":"Second global"}`, http.StatusConflict)
	call(adminID, http.MethodPost, contextPath, `{"name":"  "}`, http.StatusBadRequest)
	call(adminID, http.MethodPost, contextPath, `{"name":"Unknown project","projectIds":["9999"]}`, http.StatusBadRequest)
	call(adminID, http.MethodPost, contextPath, `{"name":"Unknown type","issueTypeIds":["it_nope"]}`, http.StatusBadRequest)

	// Narrowing the global context to project A removes the field from B.
	call(adminID, http.MethodPut, contextPath+"/"+globalContext+"/project", `{"projectIds":["`+projectA+`"]}`, http.StatusNoContent)
	if !contains(fieldsFor(keyA, "it_task"), fieldID) {
		t.Fatal("field should still apply to its own project")
	}
	if contains(fieldsFor(keyB, "it_task"), fieldID) {
		t.Fatal("field should no longer apply outside its context")
	}

	// A second context may now cover project B without overlapping.
	betaContext := decodeID(call(adminID, http.MethodPost, contextPath, `{"name":"Beta only","description":"Beta release notes","projectIds":["`+projectB+`"]}`, http.StatusCreated))
	call(adminID, http.MethodPost, contextPath, `{"name":"beta ONLY","projectIds":["`+projectA+`"]}`, http.StatusConflict)
	call(adminID, http.MethodPut, contextPath+"/"+betaContext, `{"description":"Notes for the beta project"}`, http.StatusNoContent)
	if !contains(fieldsFor(keyB, "it_task"), fieldID) {
		t.Fatal("the second context should bring the field back for beta")
	}

	// Work type scoping narrows further.
	call(adminID, http.MethodPut, contextPath+"/"+betaContext+"/issuetype", `{"issueTypeIds":["it_subtask"]}`, http.StatusNoContent)
	if contains(fieldsFor(keyB, "it_task"), fieldID) {
		t.Fatal("a sub-task context should not reach tasks")
	}
	if !contains(fieldsFor(keyB, "it_subtask"), fieldID) {
		t.Fatal("the context should still reach sub-tasks")
	}

	// Defaults are stored per context and reach createmeta.
	call(adminID, http.MethodPut, contextPath+"/defaultValue", `{"defaultValues":[{"contextId":"`+globalContext+`","value":"Ship it"}]}`, http.StatusNoContent)
	defaults := call(adminID, http.MethodGet, contextPath+"/defaultValue", "", http.StatusOK)
	if !strings.Contains(defaults.Body.String(), `"Ship it"`) {
		t.Fatal(defaults.Body.String())
	}
	call(adminID, http.MethodGet, contextPath+"/defaultValues", "", http.StatusOK)
	meta := call(adminID, http.MethodGet, "/rest/api/3/issue/createmeta/"+keyA+"/issuetypes/it_task?maxResults=60", "", http.StatusOK)
	if !strings.Contains(meta.Body.String(), `"defaultValue":"Ship it"`) {
		t.Fatal(meta.Body.String())
	}
	call(adminID, http.MethodPut, contextPath+"/defaultValue", `{"defaultValues":[{"contextId":"`+globalContext+`","value":null}]}`, http.StatusNoContent)

	mapping := call(adminID, http.MethodPost, contextPath+"/mapping", `{"mappings":[{"projectId":"`+projectA+`","issueTypeId":"it_task"},{"projectId":"`+projectB+`","issueTypeId":"it_task"}]}`, http.StatusOK)
	if strings.Count(mapping.Body.String(), `"contextId"`) != 1 {
		t.Fatalf("only project A should resolve a context: %s", mapping.Body.String())
	}
	call(adminID, http.MethodGet, contextPath+"/issuetypemapping", "", http.StatusOK)
	call(adminID, http.MethodGet, contextPath+"/projectmapping", "", http.StatusOK)

	// Removing every scope entry returns a context to applying everywhere.
	call(adminID, http.MethodPost, contextPath+"/"+betaContext+"/issuetype/remove", `{"issueTypeIds":["it_subtask"]}`, http.StatusNoContent)
	call(adminID, http.MethodPost, contextPath+"/"+betaContext+"/project/remove", `{"projectIds":["`+projectB+`"]}`, http.StatusConflict)

	// A field keeps at least one context.
	call(adminID, http.MethodDelete, contextPath+"/"+betaContext, "", http.StatusNoContent)
	call(adminID, http.MethodDelete, contextPath+"/"+globalContext, "", http.StatusConflict)

	var actions int
	if err = st.Pool.QueryRow(ctx, `SELECT count(*) FROM actions WHERE workspace_id=$1 AND entity_type LIKE 'custom_field_context%'`, workspaceID).Scan(&actions); err != nil || actions < 6 {
		t.Fatalf("actions=%d err=%v", actions, err)
	}
}
