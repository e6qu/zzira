package api3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
)

// TestJQLAutocompleteFollowsJiraReferenceData covers Jira's JQL reference data
// and suggestions for custom fields: cf[N] identifiers, project filtering
// through contexts, collapsed fields that search every field sharing a name
// and type, and suggestions for option fields and CHANGED BY predicates.
func TestJQLAutocompleteFollowsJiraReferenceData(t *testing.T) {
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
	stamp := time.Now().UnixNano() % 1000000
	projectKey, otherKey := fmt.Sprintf("JA%06d", stamp), fmt.Sprintf("JB%06d", stamp)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'JQL autocomplete')`, workspaceID)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Autocomplete Admin')`, adminID, adminID+"@example.test")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'admin')`, workspaceID, adminID)
	exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, adminID, store.HashToken(adminID))
	t.Cleanup(func() {
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM boards WHERE project_id IN (SELECT id FROM projects WHERE workspace_id=$1)`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM custom_fields WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		exec(`DELETE FROM api_tokens WHERE user_id=$1`, adminID)
		exec(`DELETE FROM users WHERE id=$1`, adminID)
	})
	h := &Handler{Store: st, Commands: &commands.Service{Store: st}, WorkspaceSlug: workspaceID, BaseURL: "https://zzira.test"}
	call := func(method, path, body string, want int) string {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.SetBasicAuth(adminID+"@example.test", adminID)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, response.Code, want, response.Body.String())
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

	project := object(call(http.MethodPost, "/rest/api/3/project", `{"key":"`+projectKey+`","name":"Autocomplete `+projectKey+`","projectTypeKey":"software","leadAccountId":"`+adminID+`","assigneeType":"PROJECT_LEAD"}`, http.StatusCreated))
	other := object(call(http.MethodPost, "/rest/api/3/project", `{"key":"`+otherKey+`","name":"Other `+otherKey+`","projectTypeKey":"software","leadAccountId":"`+adminID+`","assigneeType":"PROJECT_LEAD"}`, http.StatusCreated))
	projectID, otherID := fmt.Sprintf("%.0f", project["id"]), fmt.Sprintf("%.0f", other["id"])

	// Two dropdowns share the name Component; Scoped note applies to one project.
	firstComponent := object(call(http.MethodPost, "/rest/api/3/field", `{"name":"Component","type":"select"}`, http.StatusCreated))["id"].(string)
	secondComponent := object(call(http.MethodPost, "/rest/api/3/field", `{"name":"Component","type":"select"}`, http.StatusCreated))["id"].(string)
	scoped := object(call(http.MethodPost, "/rest/api/3/field", `{"name":"Scoped note `+projectKey+`","type":"text"}`, http.StatusCreated))["id"].(string)
	var scopedContexts struct {
		Values []struct {
			ID any `json:"id"`
		} `json:"values"`
	}
	if err = json.Unmarshal([]byte(call(http.MethodGet, "/rest/api/3/field/"+scoped+"/context", "", http.StatusOK)), &scopedContexts); err != nil {
		t.Fatal(err)
	}
	// Assigning the global context to one project scopes the field there.
	if len(scopedContexts.Values) != 1 {
		t.Fatalf("scoped field contexts = %+v", scopedContexts.Values)
	}
	call(http.MethodPut, fmt.Sprintf("/rest/api/3/field/%s/context/%v/project", scoped, scopedContexts.Values[0].ID), `{"projectIds":["`+projectID+`"]}`, http.StatusNoContent)
	addOption := func(fieldID, value string) {
		t.Helper()
		var contexts struct {
			Values []struct {
				ID any `json:"id"`
			} `json:"values"`
		}
		if decodeErr := json.Unmarshal([]byte(call(http.MethodGet, "/rest/api/3/field/"+fieldID+"/context", "", http.StatusOK)), &contexts); decodeErr != nil || len(contexts.Values) == 0 {
			t.Fatalf("contexts for %s: %v", fieldID, decodeErr)
		}
		call(http.MethodPost, fmt.Sprintf("/rest/api/3/field/%s/context/%v/option", fieldID, contexts.Values[0].ID), `{"options":[{"value":"`+value+`"}]}`, http.StatusOK)
	}
	addOption(firstComponent, "Backend")
	addOption(secondComponent, "Frontend")

	type reference struct {
		Value, DisplayName, CFID, Auto string
		Types                          []string
	}
	references := func(body string) map[string]reference {
		t.Helper()
		var data struct {
			VisibleFieldNames []reference `json:"visibleFieldNames"`
		}
		if decodeErr := json.Unmarshal([]byte(body), &data); decodeErr != nil {
			t.Fatal(decodeErr)
		}
		byDisplay := map[string]reference{}
		for _, field := range data.VisibleFieldNames {
			byDisplay[field.DisplayName] = field
		}
		return byDisplay
	}
	cf := func(id string) string { return "cf[" + strings.TrimPrefix(id, "customfield_") + "]" }

	all := references(call(http.MethodGet, "/rest/api/3/jql/autocompletedata", "", http.StatusOK))
	first := all["Component - "+cf(firstComponent)]
	if first.Value != cf(firstComponent) || first.CFID != cf(firstComponent) || first.Auto != "true" || len(first.Types) != 1 || first.Types[0] != "OPTION" {
		t.Fatalf("shared-name field = %+v", first)
	}
	if note := all["Scoped note "+projectKey+" - "+cf(scoped)]; note.Value != "Scoped note "+projectKey || note.Types[0] != "TEXT" {
		t.Fatalf("unique-name field = %+v", note)
	}
	if _, present := all["Component - Component[Dropdown]"]; present {
		t.Fatal("collapsed fields appeared without being requested")
	}

	collapsed := references(call(http.MethodPost, "/rest/api/3/jql/autocompletedata", `{"includeCollapsedFields":true,"projectIds":[`+otherID+`, 999999999]}`, http.StatusOK))
	if field := collapsed["Component - Component[Dropdown]"]; field.Value != `"Component[Dropdown]"` || field.CFID != "" {
		t.Fatalf("collapsed field = %+v", field)
	}
	if _, present := collapsed["Scoped note "+projectKey+" - "+cf(scoped)]; present {
		t.Fatal("a field scoped to another project was listed")
	}
	if _, present := collapsed["Summary"]; !present {
		t.Fatal("system fields must always be listed")
	}
	if scopedIn := references(call(http.MethodPost, "/rest/api/3/jql/autocompletedata", `{"projectIds":[`+projectID+`]}`, http.StatusOK)); scopedIn["Scoped note "+projectKey+" - "+cf(scoped)].Value == "" {
		t.Fatal("a field scoped to the selected project was left out")
	}

	// Suggestions for an option field by cf[N], by name, and for CHANGED BY.
	if options := call(http.MethodGet, "/rest/api/3/jql/autocompletedata/suggestions?fieldName="+url.QueryEscape(cf(firstComponent)), "", http.StatusOK); !strings.Contains(options, `"value":"Backend"`) || strings.Contains(options, "Frontend") {
		t.Fatalf("cf suggestions = %s", options)
	}
	if byName := call(http.MethodGet, "/rest/api/3/jql/autocompletedata/suggestions?fieldName="+url.QueryEscape("Component[Dropdown]"), "", http.StatusOK); !strings.Contains(byName, `"value":"Backend"`) {
		t.Fatalf("collapsed-name suggestions = %s", byName)
	}
	if people := call(http.MethodGet, "/rest/api/3/jql/autocompletedata/suggestions?fieldName=status&predicateName=by&predicateValue=autocomplete", "", http.StatusOK); !strings.Contains(people, `"value":"`+adminID+`"`) {
		t.Fatalf("changed-by suggestions = %s", people)
	}
	call(http.MethodGet, "/rest/api/3/jql/autocompletedata/suggestions?fieldName=status&predicateName=during", "", http.StatusBadRequest)

	// The collapsed name searches both dropdowns.
	issue := object(call(http.MethodPost, "/rest/api/3/issue", `{"fields":{"project":{"key":"`+projectKey+`"},"summary":"Collapsed search","issuetype":{"name":"Task"},"`+secondComponent+`":{"value":"Frontend"}}}`, http.StatusCreated))
	encoded, _ := json.Marshal(`project = ` + projectKey + ` AND "Component[Dropdown]" = Frontend`)
	if found := call(http.MethodPost, "/rest/api/3/search/jql", `{"jql":`+string(encoded)+`,"fields":["summary"]}`, http.StatusOK); !strings.Contains(found, issue["key"].(string)) {
		t.Fatalf("collapsed search = %s", found)
	}
}
