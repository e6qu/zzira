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

// TestTeamFieldHoldsAnAtlassianTeam covers Jira's Team field: every site has
// one, work items take a team by id, and the value reads back, searches and
// records history like a team.
func TestTeamFieldHoldsAnAtlassianTeam(t *testing.T) {
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
	exec(`INSERT INTO workspaces(id,slug,name) VALUES ($1,$1,'Teams')`, ws)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES ($1,$2,'test','Team Admin')`, admin, admin+"@example.test")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES ($1,$2,'admin')`, ws, admin)
	exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES ($1,$1,$2)`, admin, store.HashToken(admin))
	t.Cleanup(func() {
		for _, sql := range []string{`DELETE FROM custom_fields WHERE workspace_id=$1`, `DELETE FROM issues WHERE workspace_id=$1`,
			`DELETE FROM atlassian_teams WHERE workspace_id=$1`,
			`DELETE FROM boards WHERE project_id IN (SELECT id FROM projects WHERE workspace_id=$1)`, `DELETE FROM projects WHERE workspace_id=$1`,
			`DELETE FROM actions WHERE workspace_id=$1`, `DELETE FROM memberships WHERE workspace_id=$1`, `DELETE FROM workspaces WHERE id=$1`} {
			_, _ = st.Pool.Exec(ctx, sql, ws)
		}
		_, _ = st.Pool.Exec(ctx, `DELETE FROM api_tokens WHERE user_id=$1`, admin)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, admin)
	})
	h := &Handler{Store: st, Commands: &commands.Service{Store: st}, WorkspaceSlug: ws, BaseURL: "https://zzira.test"}
	call := func(method, path, body string, want int) any {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.SetBasicAuth(admin+"@example.test", admin)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, w.Code, want, w.Body.String())
		}
		var out any
		if trimmed := strings.TrimSpace(w.Body.String()); trimmed != "" {
			if err := json.Unmarshal([]byte(trimmed), &out); err != nil {
				t.Fatal(trimmed, err)
			}
		}
		return out
	}
	platform, err := st.CreateAtlassianTeam(ctx, ws, admin, "Platform", "Runs the platform")
	if err != nil {
		t.Fatal(err)
	}
	mobile, err := st.CreateAtlassianTeam(ctx, ws, admin, "Mobile", "")
	if err != nil {
		t.Fatal(err)
	}

	// The site has its Team field, typed as Jira types it.
	teamFieldID := ""
	for _, raw := range call(http.MethodGet, "/rest/api/3/field", "", http.StatusOK).([]any) {
		field := raw.(map[string]any)
		schema, _ := field["schema"].(map[string]any)
		if field["name"] == "Team" && schema != nil && schema["type"] == "team" && schema["custom"] == "com.atlassian.teams:rm-teams-custom-field-team" {
			teamFieldID = fmt.Sprint(field["id"])
		}
	}
	if teamFieldID == "" {
		t.Fatal("the site has no Team field")
	}

	stamp := fmt.Sprint(time.Now().UnixNano())
	key := "TF" + stamp[len(stamp)-4:]
	call(http.MethodPost, "/rest/api/3/project", `{"key":"`+key+`","name":"Teams `+stamp+`","projectTypeKey":"software","leadAccountId":"`+admin+`"}`, http.StatusCreated)
	created := call(http.MethodPost, "/rest/api/3/issue", `{"fields":{"project":{"key":"`+key+`"},"summary":"Owned","issuetype":{"name":"Task"},"`+teamFieldID+`":{"id":"`+platform+`"}}}`, http.StatusCreated).(map[string]any)
	issueKey := created["key"].(string)
	call(http.MethodPost, "/rest/api/3/issue", `{"fields":{"project":{"key":"`+key+`"},"summary":"Unowned","issuetype":{"name":"Task"}}}`, http.StatusCreated)
	fields := call(http.MethodGet, "/rest/api/3/issue/"+issueKey, "", http.StatusOK).(map[string]any)["fields"].(map[string]any)
	if team, ok := fields[teamFieldID].(map[string]any); !ok || team["id"] != platform || team["name"] != "Platform" || team["title"] != "Platform" {
		t.Fatalf("team value = %v", fields[teamFieldID])
	}
	call(http.MethodPost, "/rest/api/3/issue", `{"fields":{"project":{"key":"`+key+`"},"summary":"Stranger","issuetype":{"name":"Task"},"`+teamFieldID+`":"`+store.NewID("team")+`"}}`, http.StatusBadRequest)

	for jql, want := range map[string]int{
		teamFieldID + ` = "` + platform + `"`: 1,
		teamFieldID + ` = Platform`:           1,
		`Team = Platform`:                     1,
		teamFieldID + ` = Mobile`:             0,
		teamFieldID + ` is EMPTY`:             1,
	} {
		result := call(http.MethodGet, "/rest/api/3/search/jql?fields=summary&jql="+url.QueryEscape(jql+` AND project = `+key), "", http.StatusOK).(map[string]any)
		if got := len(result["issues"].([]any)); got != want {
			t.Fatalf("%s matched %d, want %d", jql, got, want)
		}
	}

	// A team given by its bare id moves the work, and history names both teams.
	call(http.MethodPut, "/rest/api/3/issue/"+issueKey, `{"fields":{"`+teamFieldID+`":"`+mobile+`"}}`, http.StatusNoContent)
	histories := call(http.MethodGet, "/rest/api/3/issue/"+issueKey+"/changelog", "", http.StatusOK).(map[string]any)["values"].([]any)
	item := histories[len(histories)-1].(map[string]any)["items"].([]any)[0].(map[string]any)
	if item["fieldId"] != teamFieldID || item["fromString"] != "Platform" || item["toString"] != "Mobile" || item["to"] != mobile {
		t.Fatalf("changelog item = %v", item)
	}
}
