package api3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
)

// documentedProperties reads the properties one pinned schema publishes. The
// schemas that matter here refuse any other property, so a bean carrying one
// is a bean a client cannot read.
func documentedProperties(t *testing.T, schema string) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "api", "specs", "jira-v3.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Components struct {
			Schemas map[string]struct {
				AdditionalProperties any                        `json:"additionalProperties"`
				Properties           map[string]json.RawMessage `json:"properties"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	found, ok := document.Components.Schemas[schema]
	if !ok {
		t.Fatalf("%s is not in the pinned specification", schema)
	}
	if allowed, isBool := found.AdditionalProperties.(bool); !isBool || allowed {
		t.Fatalf("%s no longer refuses undocumented properties", schema)
	}
	names := make([]string, 0, len(found.Properties))
	for name := range found.Properties {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// TestExpansionsCarryWhatTheyName pins the expansions a client asks for by
// name: an expansion the contract publishes has to change the response, a
// list it does not name stays collapsed, and what an expansion adds has to be
// the shape the contract publishes for it. The filter name search is pinned
// here too because the search is how a client reaches the filters it expands.
func TestExpansionsCarryWhatTheyName(t *testing.T) {
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
	if err := store.Migrate(ctx, st.Pool); err != nil {
		t.Fatal(err)
	}
	workspaceID, ownerID, memberID := store.NewID("ws"), store.NewID("usr"), store.NewID("usr")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Expansions')`, workspaceID)
	for _, user := range []struct{ id, role string }{{ownerID, "admin"}, {memberID, "member"}} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test',$3)`,
			user.id, user.id+"@example.test", strings.TrimPrefix(user.id, "usr_"))
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,$3)`, workspaceID, user.id, user.role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, user.id, store.HashToken(user.id))
	}
	t.Cleanup(func() {
		exec(`DELETE FROM worklog_properties WHERE worklog_id IN (SELECT id FROM worklogs WHERE workspace_id=$1)`, workspaceID)
		exec(`DELETE FROM worklogs WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM filter_subscriptions WHERE filter_id IN (SELECT id FROM filters WHERE workspace_id=$1)`, workspaceID)
		exec(`DELETE FROM filters WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM boards WHERE project_id IN (SELECT id FROM projects WHERE workspace_id=$1)`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		for _, id := range []string{ownerID, memberID} {
			exec(`DELETE FROM api_tokens WHERE user_id=$1`, id)
			exec(`DELETE FROM users WHERE id=$1`, id)
		}
	})
	handler := &Handler{Store: st, Commands: &commands.Service{Store: st}, WorkspaceSlug: workspaceID, BaseURL: "https://zzira.test"}
	call := func(userID, method, path string, body any, want int) map[string]any {
		t.Helper()
		raw := ""
		if body != nil {
			encoded, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			raw = string(encoded)
		}
		request := httptest.NewRequest(method, path, strings.NewReader(raw))
		request.SetBasicAuth(userID+"@example.test", userID)
		if raw != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s: %d want %d: %s", method, path, response.Code, want, response.Body.String())
		}
		out := map[string]any{}
		if response.Body.Len() > 0 && response.Body.Bytes()[0] == '{' {
			if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
				t.Fatalf("%s %s: decode: %v", method, path, err)
			}
		}
		return out
	}
	list := func(userID, method, path string, body any) []any {
		t.Helper()
		raw := ""
		if body != nil {
			encoded, _ := json.Marshal(body)
			raw = string(encoded)
		}
		request := httptest.NewRequest(method, path, strings.NewReader(raw))
		request.SetBasicAuth(userID+"@example.test", userID)
		if raw != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s %s: %d: %s", method, path, response.Code, response.Body.String())
		}
		var out []any
		if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
			t.Fatalf("%s %s: decode: %v", method, path, err)
		}
		return out
	}

	call(ownerID, http.MethodPost, "/rest/api/3/project", map[string]any{
		"key": "EXP", "name": "Expansion work", "projectTypeKey": "software", "leadAccountId": ownerID,
	}, http.StatusCreated)
	issue := call(ownerID, http.MethodPost, "/rest/api/3/issue", map[string]any{
		"fields": map[string]any{"project": map[string]any{"key": "EXP"}, "summary": "Logged work", "issuetype": map[string]any{"name": "Task"}},
	}, http.StatusCreated)
	issueKey, _ := issue["key"].(string)

	// A worklog with a property, which several operations offer to expand.
	worklog := call(ownerID, http.MethodPost, "/rest/api/3/issue/"+issueKey+"/worklog",
		map[string]any{"timeSpent": "1h", "started": "2026-01-02T03:04:05.000+0000"}, http.StatusCreated)
	worklogID, _ := worklog["id"].(string)
	call(ownerID, http.MethodPut, "/rest/api/3/issue/"+issueKey+"/worklog/"+worklogID+"/properties/tier",
		map[string]any{"level": "gold"}, http.StatusCreated)

	// Every worklog operation that offers the properties expansion answers to
	// it, and none of them volunteers the properties unasked.
	property := func(row map[string]any) any { return row["properties"] }
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/rest/api/3/issue/" + issueKey + "/worklog"},
		{http.MethodGet, "/rest/api/3/issue/" + issueKey + "/worklog/" + worklogID},
		{http.MethodGet, "/rest/api/3/worklog/updated"},
		{http.MethodPost, "/rest/api/3/worklog/list"},
	} {
		var body any
		if c.method == http.MethodPost {
			body = map[string]any{"ids": []string{worklogID}}
		}
		row := func(path string) map[string]any {
			if c.path == "/rest/api/3/issue/"+issueKey+"/worklog/"+worklogID {
				return call(ownerID, c.method, path, body, http.StatusOK)
			}
			if c.method == http.MethodPost {
				rows := list(ownerID, c.method, path, body)
				return rows[0].(map[string]any)
			}
			answered := call(ownerID, c.method, path, body, http.StatusOK)
			rows, _ := answered["worklogs"].([]any)
			if rows == nil {
				rows, _ = answered["values"].([]any)
			}
			for _, candidate := range rows {
				row := candidate.(map[string]any)
				// The change feed names a worklog by the numeric id clients
				// know, where the worklog operations answer it as text.
				if fmt.Sprint(row["id"]) == worklogID || fmt.Sprint(row["worklogId"]) == worklogID {
					return row
				}
			}
			t.Fatalf("%s %s: the worklog is not in the answer", c.method, path)
			return nil
		}
		separator := "?"
		if strings.Contains(c.path, "?") {
			separator = "&"
		}
		if value := property(row(c.path)); value != nil {
			t.Errorf("%s %s: answered properties %v without being asked", c.method, c.path, value)
		}
		expanded := property(row(c.path + separator + "expand=properties"))
		values, ok := expanded.([]any)
		if !ok || len(values) != 1 {
			t.Errorf("%s %s: expanding properties answered %v, want the one property the worklog holds", c.method, c.path, expanded)
			continue
		}
		if held := values[0].(map[string]any); held["key"] != "tier" {
			t.Errorf("%s %s: expanded property is %v", c.method, c.path, held)
		}
	}

	// A filter with a subscriber, which the filter operations offer to expand.
	filter := call(ownerID, http.MethodPost, "/rest/api/3/filter", map[string]any{
		"name": "Quarterly review board", "jql": "project = EXP",
		"sharePermissions": []any{map[string]any{"type": "authenticated"}},
	}, http.StatusOK)
	filterID, _ := filter["id"].(string)
	var storeFilterID string
	if err := st.Pool.QueryRow(ctx, `SELECT id FROM filters WHERE workspace_id=$1 AND jira_id=$2`, workspaceID, filterID).Scan(&storeFilterID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveFilterSubscription(ctx, workspaceID, ownerID, storeFilterID, store.FilterSubscriptionInput{
		Expression: "0 8 * * *", Timezone: "UTC", Recipients: []string{ownerID},
	}); err != nil {
		t.Fatal(err)
	}

	// Unexpanded, the lists report their size and carry no items; expanded,
	// they carry what they counted.
	collapsed := call(ownerID, http.MethodGet, "/rest/api/3/filter/"+filterID, nil, http.StatusOK)
	for _, name := range []string{"subscriptions", "sharedUsers"} {
		held, _ := collapsed[name].(map[string]any)
		if items, _ := held["items"].([]any); len(items) != 0 {
			t.Errorf("%s carried %d items without being expanded", name, len(items))
		}
	}
	if size := collapsed["subscriptions"].(map[string]any)["size"]; size != float64(1) {
		t.Errorf("unexpanded subscriptions report size %v, want the one subscription", size)
	}
	expanded := call(ownerID, http.MethodGet, "/rest/api/3/filter/"+filterID+"?expand=subscriptions,sharedUsers", nil, http.StatusOK)
	subscriptions := expanded["subscriptions"].(map[string]any)["items"].([]any)
	if len(subscriptions) != 1 {
		t.Fatalf("expanded subscriptions: %v", subscriptions)
	}
	if users := expanded["sharedUsers"].(map[string]any)["items"].([]any); len(users) == 0 {
		t.Error("expanding sharedUsers on a filter shared with everyone answered nobody")
	}
	// A published subscription names one subscriber and carries nothing the
	// contract does not publish for it.
	subscription := subscriptions[0].(map[string]any)
	allowed := documentedProperties(t, "FilterSubscription")
	for name := range subscription {
		if !slices.Contains(allowed, name) {
			t.Errorf("a subscription carries %q, which the contract does not publish; it publishes %v", name, allowed)
		}
	}
	subscriber, ok := subscription["user"].(map[string]any)
	if !ok || subscriber["accountId"] != ownerID {
		t.Errorf("the subscription names %v, want the subscribing user", subscription)
	}

	// The filter name search is a case-insensitive partial match, and with
	// isSubstringMatch the query is one run of text the name must contain.
	for _, c := range []struct {
		query string
		want  bool
	}{
		{"filterName=Quarterly+review+board", true},
		{"filterName=Quarterly", true},
		{"filterName=quarterly", true},
		{"filterName=review", true},
		{"filterName=board+Quarterly", true},
		{"filterName=board+Quarterly&isSubstringMatch=true", false},
		{"filterName=Quarterly+review&isSubstringMatch=true", true},
		{"filterName=quarterly+review&isSubstringMatch=true", true},
		{"filterName=absent", false},
	} {
		found := call(ownerID, http.MethodGet, "/rest/api/3/filter/search?"+c.query, nil, http.StatusOK)
		values, _ := found["values"].([]any)
		named := false
		for _, value := range values {
			if value.(map[string]any)["id"] == filterID {
				named = true
			}
		}
		if named != c.want {
			t.Errorf("filter search %s found the filter: %v, want %v", c.query, named, c.want)
		}
	}
}
