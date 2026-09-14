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

	"github.com/e6qu/zzira/internal/apps"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
)

// TestEntityPropertyJQL covers searching issue properties an app indexes
// through a Connect jiraEntityProperties module: each extraction is searchable
// as issue.property[key].path and by its alias, compares by its type, matches
// arrays by any element, orders results, and unindexed paths are refused.
func TestEntityPropertyJQL(t *testing.T) {
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
	projectKey, appKey := fmt.Sprintf("EP%06d", stamp), fmt.Sprintf("delivery-%d", stamp)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Entity properties')`, workspaceID)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Property admin')`, adminID, adminID+"@example.test")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'admin')`, workspaceID, adminID)
	exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, adminID, store.HashToken(adminID))
	principals := []string{}
	t.Cleanup(func() {
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM boards WHERE project_id IN (SELECT id FROM projects WHERE workspace_id=$1)`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM app_installations WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		for _, id := range append([]string{adminID}, principals...) {
			exec(`DELETE FROM api_tokens WHERE user_id=$1`, id)
			exec(`DELETE FROM users WHERE id=$1`, id)
		}
	})
	raw := []byte(`{"key":"` + appKey + `","name":"Delivery","baseUrl":"https://delivery.example.test","authentication":{"type":"jwt"},"scopes":["READ"],
		"modules":{"jiraEntityProperties":[{"key":"delivery-index","name":{"value":"Delivery index"},"entityType":"issue","keyConfigurations":[{"propertyKey":"delivery","extractions":[
		  {"objectName":"stage","type":"string","alias":"deliveryStage"},
		  {"objectName":"points","type":"number"},
		  {"objectName":"shipped","type":"date"},
		  {"objectName":"tags","type":"string"},
		  {"objectName":"notes","type":"text"},
		  {"objectName":"owner.accountId","type":"user"},
		  {"objectName":"summary","type":"string","alias":"summary"}]}]}]}}`)
	descriptor, err := apps.ParseDescriptor(raw)
	if err != nil {
		t.Fatal(err)
	}
	installation, err := st.InstallApp(ctx, workspaceID, adminID, descriptor, raw, []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	principals = append(principals, installation.PrincipalID)

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
	call(http.MethodPost, "/rest/api/3/project", `{"key":"`+projectKey+`","name":"Properties `+projectKey+`","projectTypeKey":"software","leadAccountId":"`+adminID+`","assigneeType":"UNASSIGNED"}`, http.StatusCreated)
	create := func(summary, property string) string {
		t.Helper()
		var created struct {
			Key string `json:"key"`
		}
		if err := json.Unmarshal([]byte(call(http.MethodPost, "/rest/api/3/issue", `{"fields":{"project":{"key":"`+projectKey+`"},"issuetype":{"name":"Task"},"summary":"`+summary+`"}}`, http.StatusCreated)), &created); err != nil {
			t.Fatal(err)
		}
		if property != "" {
			call(http.MethodPut, "/rest/api/3/issue/"+created.Key+"/properties/delivery", property, http.StatusCreated)
		}
		return created.Key
	}
	beta := create("Beta work", `{"stage":"beta","points":5,"shipped":"2026-09-01T10:00:00Z","tags":["mobile","web"],"notes":"Needs a security review","owner":{"accountId":"`+adminID+`"}}`)
	live := create("Live work", `{"stage":"live","points":12.5,"shipped":"2026-06-15","tags":["web"],"notes":"Shipped quietly","owner":{"accountId":"someone-else"}}`)
	odd := create("Odd work", `{"stage":["beta"],"points":"many","shipped":"not a date"}`)
	bare := create("Bare work", "")

	search := func(query string) []string {
		t.Helper()
		var page struct {
			Issues []struct {
				Key string `json:"key"`
			} `json:"issues"`
		}
		if err := json.Unmarshal([]byte(call(http.MethodGet, "/rest/api/3/search/jql?fields=summary&jql="+url.QueryEscape("project = "+projectKey+" AND ("+query+") ORDER BY key ASC"), "", http.StatusOK)), &page); err != nil {
			t.Fatal(err)
		}
		keys := []string{}
		for _, issue := range page.Issues {
			keys = append(keys, issue.Key)
		}
		return keys
	}
	expect := func(query string, want ...string) {
		t.Helper()
		if got := search(query); strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("%s = %v, want %v", query, got, want)
		}
	}
	expect(`issue.property[delivery].stage = beta`, beta, odd)
	expect(`deliveryStage = beta`, beta, odd)
	expect(`issue.property[delivery].stage != beta`, live)
	expect(`issue.property[delivery].stage in (live, beta)`, beta, live, odd)
	expect(`issue.property[delivery].points > 5`, live)
	expect(`issue.property[delivery].points >= 5`, beta, live)
	expect(`issue.property[delivery].points = 12.5`, live)
	expect(`issue.property[delivery].shipped < "2026-07-01"`, live)
	expect(`issue.property[delivery].tags = mobile`, beta)
	expect(`issue.property[delivery].tags = web`, beta, live)
	expect(`issue.property[delivery].notes ~ security`, beta)
	expect(`issue.property[delivery].owner.accountId = currentUser()`, beta)
	expect(`issue.property[delivery].points is EMPTY`, odd, bare)
	expect(`issue.property[delivery].stage is not EMPTY`, beta, live, odd)
	// An alias never hides a system field: summary still searches summaries.
	expect(`summary ~ "Live"`, live)
	if ordered := call(http.MethodGet, "/rest/api/3/search/jql?fields=summary&jql="+url.QueryEscape("project = "+projectKey+" AND issue.property[delivery].points is not EMPTY ORDER BY issue.property[delivery].points DESC"), "", http.StatusOK); strings.Index(ordered, live) > strings.Index(ordered, beta) {
		t.Fatalf("ordering by an indexed number = %s", ordered)
	}
	for _, refused := range []string{`issue.property[delivery].unknown = x`, `issue.property[other].stage = beta`, `issue.property[delivery].notes = security`, `issue.property[delivery].points > many`} {
		call(http.MethodGet, "/rest/api/3/search/jql?jql="+url.QueryEscape(refused), "", http.StatusBadRequest)
	}

	reference := call(http.MethodGet, "/rest/api/3/jql/autocompletedata", "", http.StatusOK)
	for _, want := range []string{`"value":"issue.property[delivery].points","displayName":"issue.property[delivery].points"`, `"value":"deliveryStage"`, `"types":["NUMBER"]`} {
		if !strings.Contains(reference, want) {
			t.Fatalf("JQL reference data lacks %s: %s", want, reference)
		}
	}
	if strings.Contains(reference, `"value":"summary","displayName":"summary - issue.property`) {
		t.Fatalf("an alias shadowing a system field is offered: %s", reference)
	}

	// A suspended app's indexes stop answering.
	exec(`UPDATE app_installations SET status='suspended' WHERE id=$1`, installation.ID)
	call(http.MethodGet, "/rest/api/3/search/jql?jql="+url.QueryEscape(`issue.property[delivery].stage = beta`), "", http.StatusBadRequest)
}
