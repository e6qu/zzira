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

func TestScreenSchemeContract(t *testing.T) {
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
	projectKey := fmt.Sprintf("F%08d", time.Now().UnixNano()%100000000)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Screen scheme contract')`, workspaceID)
	for _, identity := range []struct{ id, role string }{{adminID, "admin"}, {memberID, "member"}} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test',$3)`, identity.id, identity.id+"@example.test", "Scheme "+identity.id)
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,$3)`, workspaceID, identity.id, identity.role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, identity.id, store.HashToken(identity.id))
	}
	t.Cleanup(func() {
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

	projectResponse := call(adminID, http.MethodPost, "/rest/api/3/project", `{"key":"`+projectKey+`","name":"Forms","projectTypeKey":"software","leadAccountId":"`+adminID+`","assigneeType":"PROJECT_LEAD"}`, http.StatusCreated)
	projectID := decodeID(projectResponse)

	call(memberID, http.MethodGet, "/rest/api/3/screenscheme", "", http.StatusForbidden)

	// The workspace is provisioned with a default screen scheme and work type
	// screen scheme, and the new project is assigned the latter.
	schemes := call(adminID, http.MethodGet, "/rest/api/3/screenscheme", "", http.StatusOK)
	var schemePage struct {
		Total  int `json:"total"`
		Values []struct {
			ID      int64          `json:"id"`
			Name    string         `json:"name"`
			Screens map[string]any `json:"screens"`
		} `json:"values"`
	}
	if err = json.Unmarshal(schemes.Body.Bytes(), &schemePage); err != nil || schemePage.Total != 1 ||
		schemePage.Values[0].Name != "Default Screen Scheme" || schemePage.Values[0].Screens["default"] == nil {
		t.Fatalf("schemes=%+v err=%v body=%s", schemePage, err, schemes.Body.String())
	}
	defaultScreenSchemeID := fmt.Sprint(schemePage.Values[0].ID)

	// A lean screen shows only summary and labels.
	leanScreen := decodeID(call(adminID, http.MethodPost, "/rest/api/3/screens", `{"name":"Lean create screen"}`, http.StatusCreated))
	leanTabs := call(adminID, http.MethodGet, "/rest/api/3/screens/"+leanScreen+"/tabs", "", http.StatusOK)
	var tabList []struct {
		ID int64 `json:"id"`
	}
	if err = json.Unmarshal(leanTabs.Body.Bytes(), &tabList); err != nil || len(tabList) != 1 {
		t.Fatalf("tabs=%+v err=%v body=%s", tabList, err, leanTabs.Body.String())
	}
	leanTab := fmt.Sprint(tabList[0].ID)
	for _, field := range []string{"summary", "labels"} {
		call(adminID, http.MethodPost, "/rest/api/3/screens/"+leanScreen+"/tabs/"+leanTab+"/fields", `{"fieldId":"`+field+`"}`, http.StatusOK)
	}

	// A screen scheme needs a default screen.
	call(adminID, http.MethodPost, "/rest/api/3/screenscheme", `{"name":"Missing default","screens":{"create":"`+leanScreen+`"}}`, http.StatusBadRequest)
	call(adminID, http.MethodPost, "/rest/api/3/screenscheme", `{"name":"Bad operation","screens":{"default":"`+leanScreen+`","publish":"`+leanScreen+`"}}`, http.StatusBadRequest)
	leanSchemeID := decodeID(call(adminID, http.MethodPost, "/rest/api/3/screenscheme", `{"name":"Lean scheme","description":"Only the essentials","screens":{"default":"`+leanScreen+`"}}`, http.StatusCreated))
	call(adminID, http.MethodPost, "/rest/api/3/screenscheme", `{"name":"lean SCHEME","screens":{"default":"`+leanScreen+`"}}`, http.StatusConflict)
	call(adminID, http.MethodPut, "/rest/api/3/screenscheme/"+leanSchemeID, `{"description":"Summary and labels only"}`, http.StatusOK)

	// A screen a scheme uses cannot be deleted.
	call(adminID, http.MethodDelete, "/rest/api/3/screens/"+leanScreen, "", http.StatusConflict)

	call(adminID, http.MethodPost, "/rest/api/3/issuetypescreenscheme", `{"name":"No default mapping","issueTypeMappings":[{"issueTypeId":"it_task","screenSchemeId":"`+leanSchemeID+`"}]}`, http.StatusBadRequest)
	call(adminID, http.MethodPost, "/rest/api/3/issuetypescreenscheme", `{"name":"Unknown type","issueTypeMappings":[{"issueTypeId":"default","screenSchemeId":"`+leanSchemeID+`"},{"issueTypeId":"it_nope","screenSchemeId":"`+leanSchemeID+`"}]}`, http.StatusBadRequest)
	issueTypeSchemeID := decodeID(call(adminID, http.MethodPost, "/rest/api/3/issuetypescreenscheme",
		`{"name":"Lean forms","description":"Lean create form","issueTypeMappings":[{"issueTypeId":"default","screenSchemeId":"`+defaultScreenSchemeID+`"},{"issueTypeId":"it_task","screenSchemeId":"`+leanSchemeID+`"}]}`, http.StatusCreated))
	call(adminID, http.MethodPut, "/rest/api/3/issuetypescreenscheme/"+issueTypeSchemeID, `{"description":"Lean create form for tasks"}`, http.StatusNoContent)
	call(adminID, http.MethodGet, "/rest/api/3/issuetypescreenscheme", "", http.StatusOK)

	mappings := call(adminID, http.MethodGet, "/rest/api/3/issuetypescreenscheme/mapping?issueTypeScreenSchemeId="+issueTypeSchemeID, "", http.StatusOK)
	if strings.Count(mappings.Body.String(), `"issueTypeId"`) != 2 {
		t.Fatal(mappings.Body.String())
	}

	fieldIDs := func(user string) []string {
		t.Helper()
		response := call(user, http.MethodGet, "/rest/api/3/issue/createmeta/"+projectKey+"/issuetypes/it_task?maxResults=50", "", http.StatusOK)
		var page struct {
			Fields []struct {
				FieldID string `json:"fieldId"`
				Key     string `json:"key"`
			} `json:"fields"`
		}
		if err = json.Unmarshal(response.Body.Bytes(), &page); err != nil {
			t.Fatalf("err=%v body=%s", err, response.Body.String())
		}
		ids := []string{}
		for _, field := range page.Fields {
			id := field.FieldID
			if id == "" {
				id = field.Key
			}
			ids = append(ids, id)
		}
		return ids
	}
	// Before assignment the project uses the default screen, which carries the
	// full system field set.
	before := fieldIDs(adminID)
	if !contains(before, "priority") || !contains(before, "summary") {
		t.Fatalf("default screen fields = %v", before)
	}

	// Assigning the lean scheme narrows the create form for tasks.
	call(adminID, http.MethodPut, "/rest/api/3/issuetypescreenscheme/project", `{"issueTypeScreenSchemeId":"`+issueTypeSchemeID+`","projectId":"`+projectID+`"}`, http.StatusNoContent)
	after := fieldIDs(adminID)
	if contains(after, "priority") || !contains(after, "summary") || !contains(after, "labels") {
		t.Fatalf("lean screen fields = %v", after)
	}
	// Context fields survive any screen: creation needs them.
	if !contains(after, "project") || !contains(after, "issuetype") {
		t.Fatalf("context fields dropped: %v", after)
	}

	// Creating a work item still works, and editmeta follows the same screen.
	issue := call(adminID, http.MethodPost, "/rest/api/3/issue", `{"fields":{"project":{"key":"`+projectKey+`"},"summary":"Lean form work","issuetype":{"name":"Task"}}}`, http.StatusCreated)
	var created struct {
		Key string `json:"key"`
	}
	if err = json.Unmarshal(issue.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	editMeta := call(adminID, http.MethodGet, "/rest/api/3/issue/"+created.Key+"/editmeta", "", http.StatusOK)
	if strings.Contains(editMeta.Body.String(), `"priority"`) || !strings.Contains(editMeta.Body.String(), `"labels"`) {
		t.Fatal(editMeta.Body.String())
	}

	projects := call(adminID, http.MethodGet, "/rest/api/3/issuetypescreenscheme/"+issueTypeSchemeID+"/project", "", http.StatusOK)
	if !strings.Contains(projects.Body.String(), projectKey) {
		t.Fatal(projects.Body.String())
	}
	call(adminID, http.MethodGet, "/rest/api/3/issuetypescreenscheme/project?projectId="+projectID, "", http.StatusOK)

	// A scheme with projects, and the workspace defaults, are all protected.
	call(adminID, http.MethodDelete, "/rest/api/3/issuetypescreenscheme/"+issueTypeSchemeID, "", http.StatusConflict)
	call(adminID, http.MethodDelete, "/rest/api/3/screenscheme/"+leanSchemeID, "", http.StatusConflict)
	call(adminID, http.MethodDelete, "/rest/api/3/screenscheme/"+defaultScreenSchemeID, "", http.StatusConflict)

	// The default mapping may be repointed but never removed.
	call(adminID, http.MethodPut, "/rest/api/3/issuetypescreenscheme/"+issueTypeSchemeID+"/mapping/default", `{"screenSchemeId":"`+leanSchemeID+`"}`, http.StatusNoContent)
	call(adminID, http.MethodPost, "/rest/api/3/issuetypescreenscheme/"+issueTypeSchemeID+"/mapping/remove", `{"issueTypeIds":["default"]}`, http.StatusBadRequest)
	call(adminID, http.MethodPut, "/rest/api/3/issuetypescreenscheme/"+issueTypeSchemeID+"/mapping", `{"issueTypeMappings":[{"issueTypeId":"it_subtask","screenSchemeId":"`+defaultScreenSchemeID+`"}]}`, http.StatusNoContent)
	call(adminID, http.MethodPost, "/rest/api/3/issuetypescreenscheme/"+issueTypeSchemeID+"/mapping/remove", `{"issueTypeIds":["it_subtask"]}`, http.StatusNoContent)
	call(adminID, http.MethodPost, "/rest/api/3/issuetypescreenscheme/"+issueTypeSchemeID+"/mapping/remove", `{"issueTypeIds":["it_subtask"]}`, http.StatusNotFound)

	// Reassigning the project back to the workspace default frees the scheme.
	defaultIssueTypeScheme := call(adminID, http.MethodGet, "/rest/api/3/issuetypescreenscheme", "", http.StatusOK)
	var issueTypeSchemePage struct {
		Values []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"values"`
	}
	if err = json.Unmarshal(defaultIssueTypeScheme.Body.Bytes(), &issueTypeSchemePage); err != nil {
		t.Fatal(err)
	}
	restore := ""
	for _, scheme := range issueTypeSchemePage.Values {
		if scheme.Name == "Default Issue Type Screen Scheme" {
			restore = fmt.Sprint(scheme.ID)
		}
	}
	if restore == "" {
		t.Fatal(defaultIssueTypeScheme.Body.String())
	}
	call(adminID, http.MethodPut, "/rest/api/3/issuetypescreenscheme/project", `{"issueTypeScreenSchemeId":"`+restore+`","projectId":"`+projectID+`"}`, http.StatusNoContent)
	if restored := fieldIDs(adminID); !contains(restored, "priority") {
		t.Fatalf("restored fields = %v", restored)
	}
	call(adminID, http.MethodDelete, "/rest/api/3/issuetypescreenscheme/"+issueTypeSchemeID, "", http.StatusNoContent)
	call(adminID, http.MethodDelete, "/rest/api/3/screenscheme/"+leanSchemeID, "", http.StatusNoContent)
	call(adminID, http.MethodDelete, "/rest/api/3/screens/"+leanScreen, "", http.StatusNoContent)

	var actions int
	if err = st.Pool.QueryRow(ctx, `SELECT count(*) FROM actions WHERE workspace_id=$1 AND entity_type LIKE '%screen_scheme%'`, workspaceID).Scan(&actions); err != nil || actions < 8 {
		t.Fatalf("actions=%d err=%v", actions, err)
	}
}

func contains(values []string, candidate string) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}
