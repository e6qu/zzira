package api3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
)

// TestSearchRenderedFields covers Jira's renderedFields expansion: rich text
// and comment bodies as HTML, dates as Jira displays them, and null for fields
// without a rendered form.
func TestSearchRenderedFields(t *testing.T) {
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
	projectKey := fmt.Sprintf("RF%05d", time.Now().UnixNano()%100000)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Rendered fields')`, workspaceID)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Renderer')`, adminID, adminID+"@example.test")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'admin')`, workspaceID, adminID)
	exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, adminID, store.HashToken(adminID))
	t.Cleanup(func() {
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM boards WHERE project_id IN (SELECT id FROM projects WHERE workspace_id=$1)`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
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
	call(http.MethodPost, "/rest/api/3/project", `{"key":"`+projectKey+`","name":"Rendered `+projectKey+`","projectTypeKey":"software","leadAccountId":"`+adminID+`","assigneeType":"UNASSIGNED"}`, http.StatusCreated)
	document := func(text string) string {
		return `{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"` + text + `"}]}]}`
	}
	created := map[string]any{}
	if err = json.Unmarshal([]byte(call(http.MethodPost, "/rest/api/3/issue", `{"fields":{"project":{"key":"`+projectKey+`"},"issuetype":{"id":"10001"},"summary":"Rendered <work>","description":`+document("Steps to reproduce")+`}}`, http.StatusCreated)), &created); err != nil {
		t.Fatal(err)
	}
	key := fmt.Sprint(created["key"])
	call(http.MethodPost, "/rest/api/3/issue/"+key+"/comment", `{"body":`+document("Looks fixed")+`}`, http.StatusCreated)

	var page struct {
		Issues []struct {
			RenderedFields map[string]any `json:"renderedFields"`
		} `json:"issues"`
	}
	body := call(http.MethodGet, "/rest/api/3/search/jql?jql="+url.QueryEscape("key = "+key)+"&fields=summary,description,comment,created,labels&expand=renderedFields", "", http.StatusOK)
	if err = json.Unmarshal([]byte(body), &page); err != nil || len(page.Issues) != 1 {
		t.Fatalf("search = %s err=%v", body, err)
	}
	rendered := page.Issues[0].RenderedFields
	if rendered["summary"] != "Rendered &lt;work&gt;" {
		t.Fatalf("rendered summary = %v", rendered["summary"])
	}
	if description, _ := rendered["description"].(string); !strings.Contains(description, "<p>Steps to reproduce</p>") {
		t.Fatalf("rendered description = %v", rendered["description"])
	}
	comments, _ := rendered["comment"].(map[string]any)["comments"].([]any)
	if len(comments) != 1 || !strings.Contains(fmt.Sprint(comments[0].(map[string]any)["body"]), "<p>Looks fixed</p>") {
		t.Fatalf("rendered comments = %v", rendered["comment"])
	}
	if createdAt, _ := rendered["created"].(string); !regexp.MustCompile(`^\d{2}/[A-Z][a-z]{2}/\d{2} \d{1,2}:\d{2} (AM|PM)$`).MatchString(createdAt) {
		t.Fatalf("rendered created = %v", rendered["created"])
	}
	if value, present := rendered["labels"]; !present || value != nil {
		t.Fatalf("labels have no rendered form: %v", rendered)
	}

	// The look and feel's complete date format decides how times render, and
	// its values must be ones the site can apply.
	call(http.MethodPut, "/rest/api/3/application-properties/jira.lf.date.complete", `{"id":"jira.lf.date.complete","value":"qq"}`, http.StatusBadRequest)
	call(http.MethodPut, "/rest/api/3/application-properties/jira.lf.navigation.bgcolour", `{"id":"jira.lf.navigation.bgcolour","value":"blue; background:url(x)"}`, http.StatusBadRequest)
	call(http.MethodPut, "/rest/api/3/application-properties/jira.lf.logo.url", `{"id":"jira.lf.logo.url","value":"javascript:alert(1)"}`, http.StatusBadRequest)
	call(http.MethodPut, "/rest/api/3/application-properties/jira.lf.date.complete", `{"id":"jira.lf.date.complete","value":"yyyy-MM-dd HH:mm"}`, http.StatusOK)
	body = call(http.MethodGet, "/rest/api/3/search/jql?jql="+url.QueryEscape("key = "+key)+"&fields=created&expand=renderedFields", "", http.StatusOK)
	page.Issues = nil
	if err = json.Unmarshal([]byte(body), &page); err != nil || len(page.Issues) != 1 {
		t.Fatalf("search = %s err=%v", body, err)
	}
	if createdAt, _ := page.Issues[0].RenderedFields["created"].(string); !regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}$`).MatchString(createdAt) {
		t.Fatalf("created in the configured format = %v", page.Issues[0].RenderedFields["created"])
	}
}
