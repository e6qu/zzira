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

func TestFieldConfigurationContract(t *testing.T) {
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
	projectKey := fmt.Sprintf("C%08d", time.Now().UnixNano()%100000000)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Field configuration contract')`, workspaceID)
	for _, identity := range []struct{ id, role string }{{adminID, "admin"}, {memberID, "member"}} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test',$3)`, identity.id, identity.id+"@example.test", "Config "+identity.id)
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

	projectID := decodeID(call(adminID, http.MethodPost, "/rest/api/3/project",
		`{"key":"`+projectKey+`","name":"Fields","projectTypeKey":"software","leadAccountId":"`+adminID+`","assigneeType":"PROJECT_LEAD"}`, http.StatusCreated))

	call(memberID, http.MethodGet, "/rest/api/3/fieldconfiguration", "", http.StatusForbidden)

	// The workspace is provisioned with a default configuration and scheme.
	listed := call(adminID, http.MethodGet, "/rest/api/3/fieldconfiguration", "", http.StatusOK)
	var page struct {
		Total  int `json:"total"`
		Values []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"values"`
	}
	if err = json.Unmarshal(listed.Body.Bytes(), &page); err != nil || page.Total != 1 || page.Values[0].Name != "Default Field Configuration" {
		t.Fatalf("page=%+v err=%v body=%s", page, err, listed.Body.String())
	}
	defaultConfigID := fmt.Sprint(page.Values[0].ID)

	configID := decodeID(call(adminID, http.MethodPost, "/rest/api/3/fieldconfiguration", `{"name":"Strict release fields","description":"Priority and labels are mandatory"}`, http.StatusCreated))
	call(adminID, http.MethodPost, "/rest/api/3/fieldconfiguration", `{"name":"strict RELEASE fields"}`, http.StatusConflict)
	call(adminID, http.MethodPut, "/rest/api/3/fieldconfiguration/"+configID, `{"description":"Priority is mandatory"}`, http.StatusNoContent)

	itemsPath := "/rest/api/3/fieldconfiguration/" + configID + "/fields"
	call(adminID, http.MethodPut, itemsPath, `{"fieldConfigurationItems":[{"id":"not_a_field","isRequired":true}]}`, http.StatusBadRequest)
	call(adminID, http.MethodPut, itemsPath, `{"fieldConfigurationItems":[{"id":"priority","isRequired":true,"isHidden":true}]}`, http.StatusBadRequest)
	call(adminID, http.MethodPut, itemsPath, `{"fieldConfigurationItems":[{"id":"summary","isHidden":true}]}`, http.StatusBadRequest)
	call(adminID, http.MethodPut, itemsPath, `{"fieldConfigurationItems":[
		{"id":"priority","isRequired":true,"description":"Pick the release priority"},
		{"id":"labels","isHidden":true}]}`, http.StatusNoContent)

	items := call(adminID, http.MethodGet, itemsPath, "", http.StatusOK)
	if !strings.Contains(items.Body.String(), `"id":"priority"`) || !strings.Contains(items.Body.String(), `"isHidden":true`) ||
		!strings.Contains(items.Body.String(), `"description":"Pick the release priority"`) {
		t.Fatal(items.Body.String())
	}

	schemeID := decodeID(call(adminID, http.MethodPost, "/rest/api/3/fieldconfigurationscheme", `{"name":"Release field scheme","description":"Strict fields for tasks"}`, http.StatusCreated))
	call(adminID, http.MethodPut, "/rest/api/3/fieldconfigurationscheme/"+schemeID, `{"description":"Strict fields for release tasks"}`, http.StatusNoContent)
	call(adminID, http.MethodGet, "/rest/api/3/fieldconfigurationscheme", "", http.StatusOK)

	call(adminID, http.MethodPut, "/rest/api/3/fieldconfigurationscheme/"+schemeID+"/mapping", `{"mappings":[{"issueTypeId":"it_nope","fieldConfigurationId":"`+configID+`"}]}`, http.StatusBadRequest)
	call(adminID, http.MethodPut, "/rest/api/3/fieldconfigurationscheme/"+schemeID+"/mapping", `{"mappings":[{"issueTypeId":"it_task","fieldConfigurationId":"`+configID+`"}]}`, http.StatusNoContent)
	mappings := call(adminID, http.MethodGet, "/rest/api/3/fieldconfigurationscheme/mapping?fieldConfigurationSchemeId="+schemeID, "", http.StatusOK)
	if strings.Count(mappings.Body.String(), `"issueTypeId"`) != 2 {
		t.Fatal(mappings.Body.String())
	}

	// Before assignment the project keeps the default behaviour.
	createOK := call(adminID, http.MethodPost, "/rest/api/3/issue",
		`{"fields":{"project":{"key":"`+projectKey+`"},"summary":"Before the scheme","issuetype":{"name":"Task"}}}`, http.StatusCreated)
	var firstIssue struct {
		Key string `json:"key"`
	}
	if err = json.Unmarshal(createOK.Body.Bytes(), &firstIssue); err != nil {
		t.Fatal(err)
	}

	call(adminID, http.MethodPut, "/rest/api/3/fieldconfigurationscheme/project",
		`{"fieldConfigurationSchemeId":"`+schemeID+`","projectId":"`+projectID+`"}`, http.StatusNoContent)

	// Required and hidden are enforced by the command path, not just advertised.
	missing := call(adminID, http.MethodPost, "/rest/api/3/issue",
		`{"fields":{"project":{"key":"`+projectKey+`"},"summary":"Missing priority","issuetype":{"name":"Task"}}}`, http.StatusBadRequest)
	if !strings.Contains(missing.Body.String(), "priority") {
		t.Fatal(missing.Body.String())
	}
	hidden := call(adminID, http.MethodPost, "/rest/api/3/issue",
		`{"fields":{"project":{"key":"`+projectKey+`"},"summary":"Hidden labels","issuetype":{"name":"Task"},"priority":{"name":"Medium"},"labels":["nope"]}}`, http.StatusBadRequest)
	if !strings.Contains(hidden.Body.String(), "labels") {
		t.Fatal(hidden.Body.String())
	}
	accepted := call(adminID, http.MethodPost, "/rest/api/3/issue",
		`{"fields":{"project":{"key":"`+projectKey+`"},"summary":"With priority","issuetype":{"name":"Task"},"priority":{"name":"Medium"}}}`, http.StatusCreated)
	var secondIssue struct {
		Key string `json:"key"`
	}
	if err = json.Unmarshal(accepted.Body.Bytes(), &secondIssue); err != nil {
		t.Fatal(err)
	}

	// createmeta and editmeta advertise the same rules.
	meta := call(adminID, http.MethodGet, "/rest/api/3/issue/createmeta/"+projectKey+"/issuetypes/it_task?maxResults=50", "", http.StatusOK)
	// Jira's FieldMetadata carries no description, so the administrator's
	// override reaches the browser form rather than createmeta; the items
	// endpoint above is where REST clients read it.
	var metaPage struct {
		Fields []struct {
			FieldID  string `json:"fieldId"`
			Required bool   `json:"required"`
		} `json:"fields"`
	}
	if err = json.Unmarshal(meta.Body.Bytes(), &metaPage); err != nil {
		t.Fatal(err)
	}
	sawPriority, sawLabels := false, false
	for _, field := range metaPage.Fields {
		if field.FieldID == "priority" {
			sawPriority = true
			if !field.Required {
				t.Fatalf("priority=%+v", field)
			}
		}
		if field.FieldID == "labels" {
			sawLabels = true
		}
	}
	if !sawPriority || sawLabels {
		t.Fatalf("createmeta fields=%+v", metaPage.Fields)
	}
	editMeta := call(adminID, http.MethodGet, "/rest/api/3/issue/"+secondIssue.Key+"/editmeta", "", http.StatusOK)
	if strings.Contains(editMeta.Body.String(), `"labels"`) || !strings.Contains(editMeta.Body.String(), `"priority"`) {
		t.Fatal(editMeta.Body.String())
	}

	projects := call(adminID, http.MethodGet, "/rest/api/3/fieldconfigurationscheme/project?projectId="+projectID, "", http.StatusOK)
	if !strings.Contains(projects.Body.String(), schemeID) {
		t.Fatal(projects.Body.String())
	}

	// Everything still referenced is protected.
	call(adminID, http.MethodDelete, "/rest/api/3/fieldconfiguration/"+configID, "", http.StatusConflict)
	call(adminID, http.MethodDelete, "/rest/api/3/fieldconfiguration/"+defaultConfigID, "", http.StatusConflict)
	call(adminID, http.MethodDelete, "/rest/api/3/fieldconfigurationscheme/"+schemeID, "", http.StatusConflict)

	// The default mapping may be repointed but never removed.
	call(adminID, http.MethodPost, "/rest/api/3/fieldconfigurationscheme/"+schemeID+"/mapping/delete", `{"issueTypeIds":["default"]}`, http.StatusBadRequest)
	call(adminID, http.MethodPost, "/rest/api/3/fieldconfigurationscheme/"+schemeID+"/mapping/delete", `{"issueTypeIds":["it_task"]}`, http.StatusNoContent)
	call(adminID, http.MethodPost, "/rest/api/3/fieldconfigurationscheme/"+schemeID+"/mapping/delete", `{"issueTypeIds":["it_task"]}`, http.StatusNotFound)

	// With the task mapping gone the scheme falls back to the default
	// configuration, so priority is optional again.
	call(adminID, http.MethodPost, "/rest/api/3/issue",
		`{"fields":{"project":{"key":"`+projectKey+`"},"summary":"Fallback default","issuetype":{"name":"Task"}}}`, http.StatusCreated)

	defaultSchemes := call(adminID, http.MethodGet, "/rest/api/3/fieldconfigurationscheme", "", http.StatusOK)
	var schemePage struct {
		Values []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"values"`
	}
	if err = json.Unmarshal(defaultSchemes.Body.Bytes(), &schemePage); err != nil {
		t.Fatal(err)
	}
	restore := ""
	for _, scheme := range schemePage.Values {
		if scheme.Name == "Default Field Configuration Scheme" {
			restore = fmt.Sprint(scheme.ID)
		}
	}
	if restore == "" {
		t.Fatal(defaultSchemes.Body.String())
	}
	call(adminID, http.MethodPut, "/rest/api/3/fieldconfigurationscheme/project",
		`{"fieldConfigurationSchemeId":"`+restore+`","projectId":"`+projectID+`"}`, http.StatusNoContent)
	call(adminID, http.MethodDelete, "/rest/api/3/fieldconfigurationscheme/"+schemeID, "", http.StatusNoContent)
	call(adminID, http.MethodDelete, "/rest/api/3/fieldconfiguration/"+configID, "", http.StatusNoContent)
	call(adminID, http.MethodGet, "/rest/api/3/fieldconfiguration/"+configID+"/fields", "", http.StatusNotFound)

	var actions int
	if err = st.Pool.QueryRow(ctx, `SELECT count(*) FROM actions WHERE workspace_id=$1 AND entity_type LIKE 'field_configuration%'`, workspaceID).Scan(&actions); err != nil || actions < 8 {
		t.Fatalf("actions=%d err=%v", actions, err)
	}
	_ = firstIssue
}
