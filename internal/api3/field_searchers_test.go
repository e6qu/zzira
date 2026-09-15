package api3

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
)

// TestCustomFieldSearchers covers a custom field's searcher: Jira accepts only
// the searchers a field type allows, and the searcher decides which JQL
// operators search the field and which autocomplete offers.
func TestCustomFieldSearchers(t *testing.T) {
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
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Searchers')`, workspaceID)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Searcher admin')`, adminID, adminID+"@example.test")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'admin')`, workspaceID, adminID)
	exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, adminID, store.HashToken(adminID))
	t.Cleanup(func() {
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
	const prefix = "com.atlassian.jira.plugin.system.customfieldtypes:"

	// A searcher must suit the field's type, on creation and on update.
	call(http.MethodPost, "/rest/api/3/field", `{"name":"Wrong searcher","type":"`+prefix+`float","searcherKey":"`+prefix+`textsearcher"}`, http.StatusBadRequest)
	created := map[string]any{}
	if err = json.Unmarshal([]byte(call(http.MethodPost, "/rest/api/3/field", `{"name":"Budget","type":"`+prefix+`float","searcherKey":"`+prefix+`exactnumber"}`, http.StatusCreated)), &created); err != nil {
		t.Fatal(err)
	}
	fieldID := created["id"].(string)
	if field, fieldErr := st.CustomFieldByID(ctx, workspaceID, fieldID); fieldErr != nil || field.SearcherKey != prefix+"exactnumber" {
		t.Fatalf("created field searcher = %+v err=%v", field, fieldErr)
	}
	call(http.MethodPut, "/rest/api/3/field/"+fieldID, `{"searcherKey":"`+prefix+`labelsearcher"}`, http.StatusBadRequest)

	search := func(query string, want int) {
		t.Helper()
		call(http.MethodGet, "/rest/api/3/search/jql?jql="+url.QueryEscape(query), "", want)
	}
	reference := "cf[" + strings.TrimPrefix(fieldID, "customfield_") + "]"
	operators := func() []string {
		t.Helper()
		var data struct {
			VisibleFieldNames []struct {
				CFID      string   `json:"cfid"`
				Operators []string `json:"operators"`
			} `json:"visibleFieldNames"`
		}
		if err := json.Unmarshal([]byte(call(http.MethodGet, "/rest/api/3/jql/autocompletedata", "", http.StatusOK)), &data); err != nil {
			t.Fatal(err)
		}
		for _, field := range data.VisibleFieldNames {
			if field.CFID == reference {
				return field.Operators
			}
		}
		t.Fatalf("autocomplete does not list %s", reference)
		return nil
	}

	// The exact number searcher matches values, not ranges.
	search(reference+" = 5", http.StatusOK)
	search(reference+" > 5", http.StatusBadRequest)
	if offered := operators(); slices.Contains(offered, ">") || !slices.Contains(offered, "=") {
		t.Fatalf("exact number operators = %v", offered)
	}

	// The number range searcher also searches ranges.
	call(http.MethodPut, "/rest/api/3/field/"+fieldID, `{"searcherKey":"`+prefix+`numberrange"}`, http.StatusNoContent)
	search(reference+" > 5", http.StatusOK)
	if offered := operators(); !slices.Contains(offered, ">") {
		t.Fatalf("number range operators = %v", offered)
	}
}
