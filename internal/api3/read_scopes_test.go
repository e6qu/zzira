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

// TestScreenTabAndOptionReadScopes covers reads Jira opens beyond its
// administrators: a project's administrators read the tabs of a screen their
// project uses when they name the project, bulk tab reads filter and page,
// and a custom field option is readable by those who browse a project the
// option's field is used and shown in.
func TestScreenTabAndOptionReadScopes(t *testing.T) {
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
	workspaceID, adminID, leadID, viewerID := store.NewID("ws"), store.NewID("usr"), store.NewID("usr"), store.NewID("usr")
	stamp := time.Now().UnixNano() % 1000000
	projectKey := fmt.Sprintf("RS%06d", stamp)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	users := []struct{ id, role string }{{adminID, "admin"}, {leadID, "member"}, {viewerID, "member"}}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Read scopes')`, workspaceID)
	for _, identity := range users {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test',$3)`, identity.id, identity.id+"@example.test", "Scope "+identity.id)
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,$3)`, workspaceID, identity.id, identity.role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, identity.id, store.HashToken(identity.id))
	}
	t.Cleanup(func() {
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM boards WHERE project_id IN (SELECT id FROM projects WHERE workspace_id=$1)`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM custom_fields WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		for _, identity := range users {
			exec(`DELETE FROM api_tokens WHERE user_id=$1`, identity.id)
			exec(`DELETE FROM users WHERE id=$1`, identity.id)
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
			t.Fatalf("%s %s as %s: got %d want %d: %s", method, path, user, response.Code, want, response.Body.String())
		}
		return response.Body.String()
	}
	object := func(body string) map[string]any {
		t.Helper()
		decoded := map[string]any{}
		if decodeErr := json.Unmarshal([]byte(body), &decoded); decodeErr != nil {
			t.Fatalf("decode %s: %v", body, decodeErr)
		}
		return decoded
	}
	wireID := func(value any) string {
		if number, ok := value.(float64); ok {
			return fmt.Sprintf("%.0f", number)
		}
		return fmt.Sprint(value)
	}

	// The lead administers the project; the viewer browses it.
	project := object(call(adminID, http.MethodPost, "/rest/api/3/project", `{"key":"`+projectKey+`","name":"Scopes `+projectKey+`","projectTypeKey":"software","leadAccountId":"`+leadID+`","assigneeType":"PROJECT_LEAD"}`, http.StatusCreated))
	projectID := wireID(project["id"])
	call(adminID, http.MethodPost, "/rest/api/3/project/"+projectKey+"/role/10001", `{"user":["`+viewerID+`"]}`, http.StatusOK)

	// Screen tabs through the project's issue type screen scheme.
	screenID := wireID(object(call(adminID, http.MethodPost, "/rest/api/3/screens", `{"name":"Scoped screen `+projectKey+`"}`, http.StatusCreated))["id"])
	tabsPath := "/rest/api/3/screens/" + screenID + "/tabs"
	firstTab := wireID(object(call(adminID, http.MethodPost, tabsPath, `{"name":"Details"}`, http.StatusOK))["id"])
	secondTab := wireID(object(call(adminID, http.MethodPost, tabsPath, `{"name":"Release"}`, http.StatusOK))["id"])
	call(leadID, http.MethodGet, tabsPath, "", http.StatusForbidden)
	call(leadID, http.MethodGet, tabsPath+"?projectKey="+projectKey, "", http.StatusForbidden)
	screenScheme := wireID(object(call(adminID, http.MethodPost, "/rest/api/3/screenscheme", `{"name":"Scoped scheme `+projectKey+`","screens":{"default":"`+screenID+`"}}`, http.StatusCreated))["id"])
	typeScheme := object(call(adminID, http.MethodPost, "/rest/api/3/issuetypescreenscheme", `{"name":"Scoped types `+projectKey+`","issueTypeMappings":[{"issueTypeId":"default","screenSchemeId":"`+screenScheme+`"}]}`, http.StatusCreated))
	typeSchemeID := wireID(typeScheme["issueTypeScreenSchemeId"])
	if typeScheme["issueTypeScreenSchemeId"] == nil {
		typeSchemeID = wireID(typeScheme["id"])
	}
	call(adminID, http.MethodPut, "/rest/api/3/issuetypescreenscheme/project", `{"issueTypeScreenSchemeId":"`+typeSchemeID+`","projectId":"`+projectID+`"}`, http.StatusNoContent)
	if tabs := call(leadID, http.MethodGet, tabsPath+"?projectKey="+projectKey, "", http.StatusOK); !strings.Contains(tabs, `"name":"Release"`) {
		t.Fatalf("lead tabs = %s", tabs)
	}
	call(viewerID, http.MethodGet, tabsPath+"?projectKey="+projectKey, "", http.StatusForbidden)
	call(leadID, http.MethodGet, tabsPath+"?projectKey=NOPE"+projectKey, "", http.StatusForbidden)
	call(leadID, http.MethodPost, tabsPath+"?projectKey="+projectKey, `{"name":"Lead tab"}`, http.StatusForbidden)

	// Bulk tab reads filter by tab and page with startAt and maxResult.
	page := func(query string) []map[string]any {
		t.Helper()
		var values []map[string]any
		if decodeErr := json.Unmarshal([]byte(call(adminID, http.MethodGet, "/rest/api/3/screens/tabs?screenId="+screenID+query, "", http.StatusOK)), &values); decodeErr != nil {
			t.Fatal(decodeErr)
		}
		return values
	}
	if all := page(""); len(all) < 2 {
		t.Fatalf("bulk tabs = %v", all)
	}
	if first := page("&maxResult=1"); len(first) != 1 {
		t.Fatalf("first page = %v", first)
	}
	if only := page("&tabId=" + secondTab); len(only) != 1 || wireID(only[0]["id"]) != secondTab {
		t.Fatalf("tabId filter = %v", only)
	}
	if both := page("&tabId=" + firstTab + "&tabId=" + secondTab + "&startAt=1"); len(both) != 1 || wireID(both[0]["id"]) != secondTab {
		t.Fatalf("second page = %v", both)
	}
	call(adminID, http.MethodGet, "/rest/api/3/screens/tabs?screenId=abc", "", http.StatusBadRequest)
	call(leadID, http.MethodGet, "/rest/api/3/screens/tabs?screenId="+screenID, "", http.StatusForbidden)

	// Custom field options follow browse access and field configurations.
	fieldID := fmt.Sprint(object(call(adminID, http.MethodPost, "/rest/api/3/field", `{"name":"Ring `+projectKey+`","type":"select"}`, http.StatusCreated))["id"])
	var contexts struct {
		Values []struct {
			ID int64 `json:"id"`
		} `json:"values"`
	}
	if err = json.Unmarshal([]byte(call(adminID, http.MethodGet, "/rest/api/3/field/"+fieldID+"/context", "", http.StatusOK)), &contexts); err != nil || len(contexts.Values) != 1 {
		t.Fatalf("contexts = %+v err=%v", contexts, err)
	}
	var created struct {
		Options []struct {
			ID any `json:"id"`
		} `json:"options"`
	}
	if err = json.Unmarshal([]byte(call(adminID, http.MethodPost, fmt.Sprintf("/rest/api/3/field/%s/context/%d/option", fieldID, contexts.Values[0].ID), `{"options":[{"value":"Canary"}]}`, http.StatusOK)), &created); err != nil || len(created.Options) != 1 {
		t.Fatalf("options = %+v err=%v", created, err)
	}
	optionPath := "/rest/api/3/customFieldOption/" + wireID(created.Options[0].ID)
	option := object(call(viewerID, http.MethodGet, optionPath, "", http.StatusOK))
	if len(option) != 2 || option["value"] != "Canary" || option["self"] != "https://zzira.test"+optionPath {
		t.Fatalf("option bean = %v", option)
	}
	// Every site member browses a new project through its Members role, so
	// the option is hidden only from a caller who browses nothing.
	anonymous := httptest.NewRecorder()
	h.ServeHTTP(anonymous, httptest.NewRequest(http.MethodGet, optionPath, nil))
	if anonymous.Code != http.StatusNotFound {
		t.Fatalf("anonymous option read: %d %s", anonymous.Code, anonymous.Body.String())
	}

	// Hidden in the only field configuration the project uses, the option is
	// administrators' alone.
	var configurations struct {
		Values []struct {
			ID        any  `json:"id"`
			IsDefault bool `json:"isDefault"`
		} `json:"values"`
	}
	if err = json.Unmarshal([]byte(call(adminID, http.MethodGet, "/rest/api/3/fieldconfiguration", "", http.StatusOK)), &configurations); err != nil {
		t.Fatal(err)
	}
	for _, configuration := range configurations.Values {
		if configuration.IsDefault {
			call(adminID, http.MethodPut, "/rest/api/3/fieldconfiguration/"+wireID(configuration.ID)+"/fields", `{"fieldConfigurationItems":[{"id":"`+fieldID+`","isHidden":true}]}`, http.StatusNoContent)
		}
	}
	call(viewerID, http.MethodGet, optionPath, "", http.StatusNotFound)
	call(adminID, http.MethodGet, optionPath, "", http.StatusOK)
}
