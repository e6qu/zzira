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

// TestMultiSelectCustomField covers a multi-select custom field: its type
// keys, options, the value forms Jira accepts, search and the changelog.
func TestMultiSelectCustomField(t *testing.T) {
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
	ws, admin := store.NewID("ws"), store.NewID("usr")
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES ($1,$1,'Multi-select')`, ws)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES ($1,$2,'test','Admin')`, admin, admin+"@example.test")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES ($1,$2,'admin')`, ws, admin)
	exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES ($1,$1,$2)`, admin, store.HashToken(admin))
	t.Cleanup(func() {
		for _, sql := range []string{`DELETE FROM custom_fields WHERE workspace_id=$1`, `DELETE FROM issues WHERE workspace_id=$1`,
			`DELETE FROM boards WHERE project_id IN (SELECT id FROM projects WHERE workspace_id=$1)`, `DELETE FROM projects WHERE workspace_id=$1`,
			`DELETE FROM actions WHERE workspace_id=$1`, `DELETE FROM memberships WHERE workspace_id=$1`, `DELETE FROM workspaces WHERE id=$1`} {
			_, _ = st.Pool.Exec(ctx, sql, ws)
		}
		_, _ = st.Pool.Exec(ctx, `DELETE FROM api_tokens WHERE user_id=$1`, admin)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, admin)
	})
	h := &Handler{Store: st, Commands: &commands.Service{Store: st}, WorkspaceSlug: ws, BaseURL: "https://zzira.test"}
	call := func(method, path, body string, want int) map[string]any {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.SetBasicAuth(admin+"@example.test", admin)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, w.Code, want, w.Body.String())
		}
		out := map[string]any{}
		if trimmed := strings.TrimSpace(w.Body.String()); strings.HasPrefix(trimmed, "{") {
			if err := json.Unmarshal([]byte(trimmed), &out); err != nil {
				t.Fatal(trimmed, err)
			}
		}
		return out
	}
	stamp := time.Now().UnixNano()
	key := fmt.Sprintf("MS%04d", stamp%10000)
	call(http.MethodPost, "/rest/api/3/project", `{"key":"`+key+`","name":"Rings","projectTypeKey":"software","leadAccountId":"`+admin+`"}`, http.StatusCreated)

	fieldID := fmt.Sprint(call(http.MethodPost, "/rest/api/3/field", `{"name":"Release rings `+fmt.Sprint(stamp)+`","type":"com.atlassian.jira.plugin.system.customfieldtypes:multiselect"}`, http.StatusCreated)["id"])
	if short := call(http.MethodPost, "/rest/api/3/field", `{"name":"Platforms `+fmt.Sprint(stamp)+`","type":"multiselect"}`, http.StatusCreated); short["id"] == nil {
		t.Fatal(short)
	}
	contexts := call(http.MethodGet, "/rest/api/3/field/"+fieldID+"/context", "", http.StatusOK)["values"].([]any)
	contextID := fmt.Sprint(contexts[0].(map[string]any)["id"])
	made := call(http.MethodPost, "/rest/api/3/field/"+fieldID+"/context/"+contextID+"/option", `{"options":[{"value":"Canary"},{"value":"Broad"},{"value":"Retired"}]}`, http.StatusOK)["options"].([]any)
	canary, broad, retired := fmt.Sprint(made[0].(map[string]any)["id"]), fmt.Sprint(made[1].(map[string]any)["id"]), fmt.Sprint(made[2].(map[string]any)["id"])

	created := call(http.MethodPost, "/rest/api/3/issue", `{"fields":{"project":{"key":"`+key+`"},"summary":"Roll out","issuetype":{"name":"Task"},"`+fieldID+`":[{"value":"Canary"},{"id":"`+broad+`"}]}}`, http.StatusCreated)
	issueKey := created["key"].(string)
	stored := func() string {
		fields := call(http.MethodGet, "/rest/api/3/issue/"+issueKey+"?fields="+fieldID, "", http.StatusOK)["fields"].(map[string]any)
		return fmt.Sprint(fields[fieldID])
	}
	if got := stored(); got != "["+canary+" "+broad+"]" {
		t.Fatalf("stored = %s", got)
	}
	call(http.MethodPost, "/rest/api/3/issue", `{"fields":{"project":{"key":"`+key+`"},"summary":"Unknown ring","issuetype":{"name":"Task"},"`+fieldID+`":[{"value":"Nightly"}]}}`, http.StatusBadRequest)
	call(http.MethodPost, "/rest/api/3/issue", `{"fields":{"project":{"key":"`+key+`"},"summary":"Twice","issuetype":{"name":"Task"},"`+fieldID+`":["`+canary+`","`+canary+`"]}}`, http.StatusBadRequest)
	call(http.MethodPut, "/rest/api/3/issue/"+issueKey, `{"fields":{"`+fieldID+`":["`+retired+`"]}}`, http.StatusNoContent)
	if got := stored(); got != "["+retired+"]" {
		t.Fatalf("updated = %s", got)
	}
	call(http.MethodPut, "/rest/api/3/issue/"+issueKey, `{"fields":{"`+fieldID+`":{"value":"Broad"}}}`, http.StatusNoContent)
	if got := stored(); got != "["+broad+"]" {
		t.Fatalf("single value = %s", got)
	}

	search := func(jql string) float64 {
		t.Helper()
		result := call(http.MethodGet, "/rest/api/3/search/jql?fields=summary&jql="+url.QueryEscape(jql), "", http.StatusOK)
		return float64(len(result["issues"].([]any)))
	}
	if n := search(fieldID + ` = ` + broad + ` AND project = ` + key); n != 1 {
		t.Fatalf("= matched %v", n)
	}
	if n := search(fieldID + ` in (` + canary + `, ` + retired + `) AND project = ` + key); n != 0 {
		t.Fatalf("in matched %v", n)
	}
	if n := search(fieldID + ` is EMPTY AND project = ` + key); n != 0 {
		t.Fatalf("empty matched %v", n)
	}

	changelog := call(http.MethodGet, "/rest/api/3/issue/"+issueKey+"/changelog", "", http.StatusOK)["values"].([]any)
	last := changelog[len(changelog)-1].(map[string]any)["items"].([]any)[0].(map[string]any)
	if last["fieldId"] != fieldID || last["fromString"] != "Retired" || last["toString"] != "Broad" {
		t.Fatalf("changelog item = %v", last)
	}

	meta := call(http.MethodGet, "/rest/api/3/issue/createmeta/"+key+"/issuetypes/it_task?maxResults=100", "", http.StatusOK)
	raw, _ := json.Marshal(meta)
	if !strings.Contains(string(raw), `"items":"option"`) || !strings.Contains(string(raw), `"Canary"`) {
		t.Fatalf("createmeta = %s", raw)
	}
}
