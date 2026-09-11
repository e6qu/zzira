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

	// A context governs writes, not only what the forms offer: the field is out
	// of context for project B now, so setting it there is rejected on create
	// and on edit, while the project its context reaches still accepts it.
	call(adminID, http.MethodPost, "/rest/api/3/issue",
		`{"fields":{"project":{"key":"`+keyB+`"},"summary":"Out of context","issuetype":{"name":"Task"},"`+fieldID+`":"nope"}}`,
		http.StatusBadRequest)
	accepted := call(adminID, http.MethodPost, "/rest/api/3/issue",
		`{"fields":{"project":{"key":"`+keyA+`"},"summary":"In context","issuetype":{"name":"Task"},"`+fieldID+`":"fine"}}`,
		http.StatusCreated)
	var created struct {
		Key string `json:"key"`
	}
	if err = json.Unmarshal(accepted.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	outside := call(adminID, http.MethodPost, "/rest/api/3/issue",
		`{"fields":{"project":{"key":"`+keyB+`"},"summary":"Plain work","issuetype":{"name":"Task"}}}`,
		http.StatusCreated)
	var plain struct {
		Key string `json:"key"`
	}
	if err = json.Unmarshal(outside.Body.Bytes(), &plain); err != nil {
		t.Fatal(err)
	}
	call(adminID, http.MethodPut, "/rest/api/3/issue/"+plain.Key, `{"fields":{"`+fieldID+`":"nope"}}`, http.StatusBadRequest)
	call(adminID, http.MethodPut, "/rest/api/3/issue/"+created.Key, `{"fields":{"`+fieldID+`":"still fine"}}`, http.StatusNoContent)

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

	// ---- a select field's options belong to the governing context ----

	selectID := decodeID(call(adminID, http.MethodPost, "/rest/api/3/field", `{"name":"Release ring `+fmt.Sprint(stamp)+`","type":"select"}`, http.StatusCreated))
	selectContexts := call(adminID, http.MethodGet, "/rest/api/3/field/"+selectID+"/context", "", http.StatusOK)
	var selectPage struct {
		Values []struct {
			ID int64 `json:"id"`
		} `json:"values"`
	}
	if err = json.Unmarshal(selectContexts.Body.Bytes(), &selectPage); err != nil || len(selectPage.Values) != 1 {
		t.Fatalf("contexts=%+v err=%v body=%s", selectPage, err, selectContexts.Body.String())
	}
	selectContext := fmt.Sprint(selectPage.Values[0].ID)
	optionPath := "/rest/api/3/field/" + selectID + "/context/" + selectContext + "/option"

	// Only a select field has options.
	call(adminID, http.MethodPost, "/rest/api/3/field/"+fieldID+"/context/"+globalContext+"/option", `{"options":[{"value":"Nope"}]}`, http.StatusBadRequest)
	call(adminID, http.MethodPost, optionPath, `{"options":[{"value":"  "}]}`, http.StatusBadRequest)

	made := call(adminID, http.MethodPost, optionPath, `{"options":[{"value":"Canary"},{"value":"Broad"},{"value":"Retired"}]}`, http.StatusOK)
	var optionWire struct {
		Options []struct {
			ID    int64  `json:"id"`
			Value string `json:"value"`
		} `json:"options"`
	}
	if err = json.Unmarshal(made.Body.Bytes(), &optionWire); err != nil || len(optionWire.Options) != 3 {
		t.Fatalf("options=%+v err=%v body=%s", optionWire, err, made.Body.String())
	}
	canary, broad, retired := fmt.Sprint(optionWire.Options[0].ID), fmt.Sprint(optionWire.Options[1].ID), fmt.Sprint(optionWire.Options[2].ID)
	call(adminID, http.MethodPost, optionPath, `{"options":[{"value":"canary"}]}`, http.StatusConflict)

	optionOrder := func() []string {
		t.Helper()
		response := call(adminID, http.MethodGet, optionPath, "", http.StatusOK)
		var listed struct {
			Values []struct {
				Value string `json:"value"`
			} `json:"values"`
		}
		if err = json.Unmarshal(response.Body.Bytes(), &listed); err != nil {
			t.Fatal(err)
		}
		order := []string{}
		for _, option := range listed.Values {
			order = append(order, option.Value)
		}
		return order
	}
	if strings.Join(optionOrder(), ",") != "Canary,Broad,Retired" {
		t.Fatalf("order=%v", optionOrder())
	}
	call(adminID, http.MethodPut, optionPath+"/move", `{"customFieldOptionIds":["`+retired+`"],"position":"First"}`, http.StatusNoContent)
	if strings.Join(optionOrder(), ",") != "Retired,Canary,Broad" {
		t.Fatalf("order=%v", optionOrder())
	}
	call(adminID, http.MethodPut, optionPath+"/move", `{"customFieldOptionIds":["`+retired+`"],"after":"`+broad+`"}`, http.StatusNoContent)
	if strings.Join(optionOrder(), ",") != "Canary,Broad,Retired" {
		t.Fatalf("order=%v", optionOrder())
	}
	call(adminID, http.MethodPut, optionPath+"/move", `{"customFieldOptionIds":["9999"],"position":"First"}`, http.StatusNotFound)

	one := call(adminID, http.MethodGet, "/rest/api/3/customFieldOption/"+canary, "", http.StatusOK)
	if !strings.Contains(one.Body.String(), `"value":"Canary"`) {
		t.Fatal(one.Body.String())
	}
	call(adminID, http.MethodGet, "/rest/api/3/customFieldOption/9999", "", http.StatusNotFound)

	// The options reach createmeta as the field's allowed values.
	optionMeta := call(adminID, http.MethodGet, "/rest/api/3/issue/createmeta/"+keyA+"/issuetypes/it_task?maxResults=60", "", http.StatusOK)
	if !strings.Contains(optionMeta.Body.String(), `"Canary"`) {
		t.Fatal(optionMeta.Body.String())
	}

	// A work item may only take an option the governing context offers.
	call(adminID, http.MethodPost, "/rest/api/3/issue",
		`{"fields":{"project":{"key":"`+keyA+`"},"summary":"Bad ring","issuetype":{"name":"Task"},"`+selectID+`":"9999"}}`, http.StatusBadRequest)
	ringed := call(adminID, http.MethodPost, "/rest/api/3/issue",
		`{"fields":{"project":{"key":"`+keyA+`"},"summary":"Canary ring","issuetype":{"name":"Task"},"`+selectID+`":"`+canary+`"}}`, http.StatusCreated)
	var ringedIssue struct {
		Key string `json:"key"`
	}
	if err = json.Unmarshal(ringed.Body.Bytes(), &ringedIssue); err != nil {
		t.Fatal(err)
	}

	// A disabled option keeps existing values but can no longer be chosen.
	call(adminID, http.MethodPut, optionPath, `{"options":[{"id":"`+broad+`","value":"Broad","disabled":true}]}`, http.StatusNoContent)
	call(adminID, http.MethodPost, "/rest/api/3/issue",
		`{"fields":{"project":{"key":"`+keyA+`"},"summary":"Disabled ring","issuetype":{"name":"Task"},"`+selectID+`":"`+broad+`"}}`, http.StatusBadRequest)

	// An option in use cannot vanish silently; with a replacement it migrates.
	call(adminID, http.MethodDelete, optionPath+"/"+canary, "", http.StatusConflict)
	call(adminID, http.MethodDelete, optionPath+"/"+canary+"/issue?replaceWith="+canary, "", http.StatusBadRequest)
	call(adminID, http.MethodDelete, optionPath+"/"+canary+"/issue?replaceWith="+retired, "", http.StatusNoContent)
	moved := call(adminID, http.MethodGet, "/rest/api/3/issue/"+ringedIssue.Key, "", http.StatusOK)
	if !strings.Contains(moved.Body.String(), retired) {
		t.Fatalf("the work item should hold the replacement option: %s", moved.Body.String())
	}
	call(adminID, http.MethodDelete, optionPath+"/"+broad, "", http.StatusNoContent)
}
