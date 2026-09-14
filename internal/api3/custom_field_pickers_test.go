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

// TestProjectAndVersionPickerFields covers project, version and multi-version
// picker custom fields.
func TestProjectAndVersionPickerFields(t *testing.T) {
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
	exec(`INSERT INTO workspaces(id,slug,name) VALUES ($1,$1,'Pickers')`, ws)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES ($1,$2,'test','Picker Admin')`, admin, admin+"@example.test")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES ($1,$2,'admin')`, ws, admin)
	exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES ($1,$1,$2)`, admin, store.HashToken(admin))
	t.Cleanup(func() {
		for _, sql := range []string{`DELETE FROM custom_fields WHERE workspace_id=$1`, `DELETE FROM issues WHERE workspace_id=$1`,
			`DELETE FROM project_versions WHERE project_id IN (SELECT id FROM projects WHERE workspace_id=$1)`,
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
	stamp := fmt.Sprint(time.Now().UnixNano())
	suffix := stamp[len(stamp)-4:]
	home, other := "PH"+suffix, "PO"+suffix
	call(http.MethodPost, "/rest/api/3/project", `{"key":"`+home+`","name":"Home `+suffix+`","projectTypeKey":"software","leadAccountId":"`+admin+`"}`, http.StatusCreated)
	otherID := fmt.Sprint(call(http.MethodPost, "/rest/api/3/project", `{"key":"`+other+`","name":"Other `+suffix+`","projectTypeKey":"software","leadAccountId":"`+admin+`"}`, http.StatusCreated)["id"])
	version := func(project, name string) string {
		return fmt.Sprint(call(http.MethodPost, "/rest/api/3/version", `{"project":"`+project+`","name":"`+name+`"}`, http.StatusCreated)["id"])
	}
	v1, v2 := version(home, "1.0"), version(home, "2.0")
	foreign := version(other, "9.0")
	field := func(name, typeKey string) (string, map[string]any) {
		created := call(http.MethodPost, "/rest/api/3/field", `{"name":"`+name+` `+stamp+`","type":"com.atlassian.jira.plugin.system.customfieldtypes:`+typeKey+`"}`, http.StatusCreated)
		return fmt.Sprint(created["id"]), created["schema"].(map[string]any)
	}
	sourceID, sourceSchema := field("Source project", "project")
	targetID, targetSchema := field("Target release", "version")
	shippedID, shippedSchema := field("Shipped in", "multiversion")
	if sourceSchema["type"] != "project" || targetSchema["type"] != "version" || shippedSchema["type"] != "array" || shippedSchema["items"] != "version" ||
		!strings.HasSuffix(fmt.Sprint(shippedSchema["custom"]), ":multiversion") {
		t.Fatalf("schemas %v %v %v", sourceSchema, targetSchema, shippedSchema)
	}

	issueKey := call(http.MethodPost, "/rest/api/3/issue", `{"fields":{"project":{"key":"`+home+`"},"summary":"Picked","issuetype":{"name":"Task"},
		"`+sourceID+`":{"key":"`+other+`"},"`+targetID+`":{"name":"2.0"},"`+shippedID+`":[{"id":"`+v1+`"},{"name":"2.0"}]}}`, http.StatusCreated)["key"].(string)
	fields := call(http.MethodGet, "/rest/api/3/issue/"+issueKey, "", http.StatusOK)["fields"].(map[string]any)
	source := fields[sourceID].(map[string]any)
	if source["key"] != other || fmt.Sprint(source["id"]) != otherID {
		t.Fatalf("source = %v", source)
	}
	if target := fields[targetID].(map[string]any); target["name"] != "2.0" || target["id"] != v2 {
		t.Fatalf("target = %v", target)
	}
	if shipped := fields[shippedID].([]any); len(shipped) != 2 || shipped[0].(map[string]any)["name"] != "1.0" || shipped[1].(map[string]any)["name"] != "2.0" {
		t.Fatalf("shipped = %v", shipped)
	}
	call(http.MethodPost, "/rest/api/3/issue", `{"fields":{"project":{"key":"`+home+`"},"summary":"Foreign","issuetype":{"name":"Task"},"`+targetID+`":{"id":"`+foreign+`"}}}`, http.StatusBadRequest)
	call(http.MethodPost, "/rest/api/3/issue", `{"fields":{"project":{"key":"`+home+`"},"summary":"Missing","issuetype":{"name":"Task"},"`+sourceID+`":{"key":"NOPE"}}}`, http.StatusBadRequest)

	for jql, want := range map[string]int{
		sourceID + ` = ` + other:    1,
		sourceID + ` = ` + otherID:  1,
		sourceID + ` = ` + home:     0,
		targetID + ` = "2.0"`:       1,
		shippedID + ` in ("1.0")`:   1,
		shippedID + ` = ` + foreign: 0,
		shippedID + ` is not EMPTY`: 1,
	} {
		result := call(http.MethodGet, "/rest/api/3/search/jql?fields=summary&jql="+url.QueryEscape(jql+` AND project = `+home), "", http.StatusOK)
		if got := len(result["issues"].([]any)); got != want {
			t.Fatalf("%s matched %d, want %d", jql, got, want)
		}
	}

	call(http.MethodPut, "/rest/api/3/issue/"+issueKey, `{"fields":{"`+targetID+`":"`+v1+`"}}`, http.StatusNoContent)
	histories := call(http.MethodGet, "/rest/api/3/issue/"+issueKey+"/changelog", "", http.StatusOK)["values"].([]any)
	item := histories[len(histories)-1].(map[string]any)["items"].([]any)[0].(map[string]any)
	if item["fieldId"] != targetID || item["fromString"] != "2.0" || item["toString"] != "1.0" {
		t.Fatalf("changelog item = %v", item)
	}
}
