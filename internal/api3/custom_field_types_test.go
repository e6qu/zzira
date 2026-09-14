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

// TestJiraCustomFieldTypes covers cascading select, user and group pickers,
// labels, date and URL custom fields: their type keys and schemas, the value
// forms Jira accepts, the values issue responses describe, search and the
// changelog.
func TestJiraCustomFieldTypes(t *testing.T) {
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
	ws, admin, member := store.NewID("ws"), store.NewID("usr"), store.NewID("usr")
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES ($1,$1,'Field types')`, ws)
	for _, identity := range []struct{ id, role, name string }{{admin, "admin", "Ada Admin"}, {member, "member", "Max Member"}} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES ($1,$2,'test',$3)`, identity.id, identity.id+"@example.test", identity.name)
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES ($1,$2,$3)`, ws, identity.id, identity.role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES ($1,$1,$2)`, identity.id, store.HashToken(identity.id))
	}
	t.Cleanup(func() {
		for _, sql := range []string{`DELETE FROM custom_fields WHERE workspace_id=$1`, `DELETE FROM issues WHERE workspace_id=$1`,
			`DELETE FROM groups WHERE directory_id IN (SELECT d.id FROM directories d JOIN sites s ON s.organization_id=d.organization_id WHERE s.workspace_id=$1)`,
			`DELETE FROM boards WHERE project_id IN (SELECT id FROM projects WHERE workspace_id=$1)`, `DELETE FROM projects WHERE workspace_id=$1`,
			`DELETE FROM actions WHERE workspace_id=$1`, `DELETE FROM memberships WHERE workspace_id=$1`, `DELETE FROM workspaces WHERE id=$1`} {
			_, _ = st.Pool.Exec(ctx, sql, ws)
		}
		for _, id := range []string{admin, member} {
			_, _ = st.Pool.Exec(ctx, `DELETE FROM api_tokens WHERE user_id=$1`, id)
			_, _ = st.Pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, id)
		}
	})
	h := &Handler{Store: st, Commands: &commands.Service{Store: st}, WorkspaceSlug: ws, BaseURL: "https://zzira.test"}
	call := func(user, method, path, body string, want int) map[string]any {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.SetBasicAuth(user+"@example.test", user)
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
	key := fmt.Sprintf("CT%04d", time.Now().UnixNano()%10000)
	call(admin, http.MethodPost, "/rest/api/3/project", `{"key":"`+key+`","name":"Types","projectTypeKey":"software","leadAccountId":"`+admin+`"}`, http.StatusCreated)
	group := call(admin, http.MethodPost, "/rest/api/3/group", `{"name":"reviewers-`+stamp+`"}`, http.StatusCreated)
	groupID := fmt.Sprint(group["groupId"])

	field := func(name, typeKey string) (string, map[string]any) {
		created := call(admin, http.MethodPost, "/rest/api/3/field", `{"name":"`+name+` `+stamp+`","type":"com.atlassian.jira.plugin.system.customfieldtypes:`+typeKey+`"}`, http.StatusCreated)
		return fmt.Sprint(created["id"]), created["schema"].(map[string]any)
	}
	region, regionSchema := field("Region", "cascadingselect")
	reviewer, reviewerSchema := field("Reviewer", "userpicker")
	watchers, watchersSchema := field("Watchers", "multiuserpicker")
	team, teamSchema := field("Team group", "grouppicker")
	topics, topicsSchema := field("Topics", "labels")
	due, dueSchema := field("Target date", "datepicker")
	docs, docsSchema := field("Docs", "url")
	for name, check := range map[string]struct {
		schema map[string]any
		want   string
	}{
		"cascading": {regionSchema, "option-with-child"}, "user": {reviewerSchema, "user"}, "users": {watchersSchema, "array"},
		"group": {teamSchema, "group"}, "labels": {topicsSchema, "array"}, "date": {dueSchema, "datepicker"}, "url": {docsSchema, "string"},
	} {
		if name == "date" {
			if !strings.HasSuffix(fmt.Sprint(check.schema["custom"]), ":datepicker") {
				t.Fatalf("date schema = %v", check.schema)
			}
			continue
		}
		if check.schema["type"] != check.want || !strings.HasPrefix(fmt.Sprint(check.schema["custom"]), "com.atlassian.jira.plugin.system.customfieldtypes:") {
			t.Fatalf("%s schema = %v", name, check.schema)
		}
	}

	// Cascading options.
	contextID := fmt.Sprint(call(admin, http.MethodGet, "/rest/api/3/field/"+region+"/context", "", http.StatusOK)["values"].([]any)[0].(map[string]any)["id"])
	optionPath := "/rest/api/3/field/" + region + "/context/" + contextID + "/option"
	parents := call(admin, http.MethodPost, optionPath, `{"options":[{"value":"Europe"},{"value":"Asia"}]}`, http.StatusOK)["options"].([]any)
	europe := fmt.Sprint(parents[0].(map[string]any)["id"])
	children := call(admin, http.MethodPost, optionPath, `{"options":[{"value":"Berlin","optionId":"`+europe+`"},{"value":"Paris","optionId":"`+europe+`"}]}`, http.StatusOK)["options"].([]any)
	berlin := fmt.Sprint(children[0].(map[string]any)["id"])
	if fmt.Sprint(children[0].(map[string]any)["optionId"]) != europe {
		t.Fatal(children)
	}
	call(admin, http.MethodPost, optionPath, `{"options":[{"value":"Mitte","optionId":"`+berlin+`"}]}`, http.StatusBadRequest)
	selectID := fmt.Sprint(call(admin, http.MethodPost, "/rest/api/3/field", `{"name":"Plain `+stamp+`","type":"select"}`, http.StatusCreated)["id"])
	selectContext := fmt.Sprint(call(admin, http.MethodGet, "/rest/api/3/field/"+selectID+"/context", "", http.StatusOK)["values"].([]any)[0].(map[string]any)["id"])
	call(admin, http.MethodPost, "/rest/api/3/field/"+selectID+"/context/"+selectContext+"/option", `{"options":[{"value":"Child","optionId":"`+europe+`"}]}`, http.StatusBadRequest)

	// Values in, values out.
	body := `{"fields":{"project":{"key":"` + key + `"},"summary":"Typed work","issuetype":{"name":"Task"},
		"` + region + `":{"value":"Europe","child":{"value":"Berlin"}},
		"` + reviewer + `":{"accountId":"` + member + `"},
		"` + watchers + `":[{"accountId":"` + admin + `"},{"accountId":"` + member + `"}],
		"` + team + `":{"name":"reviewers-` + stamp + `"},
		"` + topics + `":["backend","urgent"],
		"` + due + `":"2026-10-01",
		"` + docs + `":"https://docs.example.test/runbook"}}`
	issueKey := call(admin, http.MethodPost, "/rest/api/3/issue", body, http.StatusCreated)["key"].(string)
	fields := call(admin, http.MethodGet, "/rest/api/3/issue/"+issueKey, "", http.StatusOK)["fields"].(map[string]any)
	cascade := fields[region].(map[string]any)
	if cascade["value"] != "Europe" || cascade["id"] != europe || cascade["child"].(map[string]any)["value"] != "Berlin" {
		t.Fatalf("cascading = %v", cascade)
	}
	if fields[reviewer].(map[string]any)["accountId"] != member || fields[reviewer].(map[string]any)["displayName"] != "Max Member" {
		t.Fatalf("reviewer = %v", fields[reviewer])
	}
	if len(fields[watchers].([]any)) != 2 {
		t.Fatalf("watchers = %v", fields[watchers])
	}
	if fields[team].(map[string]any)["groupId"] != groupID || fields[team].(map[string]any)["name"] != "reviewers-"+stamp {
		t.Fatalf("team = %v", fields[team])
	}
	if fmt.Sprint(fields[topics]) != "[backend urgent]" || fields[due] != "2026-10-01" || fields[docs] != "https://docs.example.test/runbook" {
		t.Fatalf("topics=%v due=%v docs=%v", fields[topics], fields[due], fields[docs])
	}

	invalid := map[string]string{
		region:   `{"value":"Europe","child":{"value":"Tokyo"}}`,
		reviewer: `{"accountId":"usr_nobody"}`,
		team:     `{"name":"nobody-here"}`,
		topics:   `["two words"]`,
		due:      `"01/10/2026"`,
		docs:     `"ftp://docs.example.test"`,
	}
	for fieldID, value := range invalid {
		call(admin, http.MethodPost, "/rest/api/3/issue", `{"fields":{"project":{"key":"`+key+`"},"summary":"Invalid","issuetype":{"name":"Task"},"`+fieldID+`":`+value+`}}`, http.StatusBadRequest)
	}

	// Search.
	search := func(user, jql string) int {
		t.Helper()
		result := call(user, http.MethodGet, "/rest/api/3/search/jql?fields=summary&jql="+url.QueryEscape(jql+` AND project = `+key), "", http.StatusOK)
		return len(result["issues"].([]any))
	}
	for jql, want := range map[string]int{
		region + ` = Europe`:                         1,
		region + ` in cascadeOption(Europe, Berlin)`: 1,
		region + ` in cascadeOption(Europe, Paris)`:  0,
		region + ` in cascadeOption(Europe, none)`:   0,
		reviewer + ` = currentUser()`:                0,
		watchers + ` = currentUser()`:                1,
		team + ` = "reviewers-` + stamp + `"`:        1,
		team + ` = ` + groupID:                       1,
		topics + ` = urgent`:                         1,
		topics + ` in (frontend)`:                    0,
		due + ` >= "2026-09-30"`:                     1,
		due + ` < "2026-10-01"`:                      0,
	} {
		if got := search(admin, jql); got != want {
			t.Fatalf("%s matched %d, want %d", jql, got, want)
		}
	}
	if got := search(member, reviewer+` = currentUser()`); got != 1 {
		t.Fatalf("the reviewer's currentUser() matched %d", got)
	}

	// Changelog.
	call(admin, http.MethodPut, "/rest/api/3/issue/"+issueKey, `{"fields":{"`+reviewer+`":"`+admin+`","`+region+`":{"id":"`+europe+`"}}}`, http.StatusNoContent)
	histories := call(admin, http.MethodGet, "/rest/api/3/issue/"+issueKey+"/changelog", "", http.StatusOK)["values"].([]any)
	items := histories[len(histories)-1].(map[string]any)["items"].([]any)
	seen := map[string]string{}
	for _, item := range items {
		entry := item.(map[string]any)
		seen[fmt.Sprint(entry["fieldId"])] = fmt.Sprint(entry["fromString"]) + " -> " + fmt.Sprint(entry["toString"])
	}
	if seen[reviewer] != "Max Member -> Ada Admin" || seen[region] != "Europe - Berlin -> Europe" {
		t.Fatalf("changelog items = %v", seen)
	}

	// Create metadata offers people and groups.
	meta, _ := json.Marshal(call(admin, http.MethodGet, "/rest/api/3/issue/createmeta/"+key+"/issuetypes/it_task?maxResults=100", "", http.StatusOK))
	if !strings.Contains(string(meta), `"type":"option-with-child"`) || !strings.Contains(string(meta), `"Max Member"`) || !strings.Contains(string(meta), `"reviewers-`+stamp+`"`) {
		t.Fatalf("createmeta = %s", meta)
	}
}
