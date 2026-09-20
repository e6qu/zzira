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

// The JQL fields that are not columns on the work item run real SQL against
// real rows: what is written about it, who follows it, what hangs off it and
// where it sits.
func TestSearchByAttachedFields(t *testing.T) {
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
	ws, admin, follower := store.NewID("ws"), store.NewID("usr"), store.NewID("usr")
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, sql, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Attached fields')`, ws)
	for _, user := range []string{admin, follower} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Search user')`, user, user+"@example.test")
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'admin')`, ws, user)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, user, store.HashToken(user))
	}
	t.Cleanup(func() {
		for _, sql := range []string{
			`DELETE FROM attachments WHERE workspace_id=$1`,
			`DELETE FROM issue_links WHERE workspace_id=$1`,
			`DELETE FROM issues WHERE workspace_id=$1`,
			`DELETE FROM projects WHERE workspace_id=$1`,
			`DELETE FROM project_categories WHERE workspace_id=$1`,
			`DELETE FROM actions WHERE workspace_id=$1`,
			`DELETE FROM memberships WHERE workspace_id=$1`,
			`DELETE FROM workspaces WHERE id=$1`,
		} {
			exec(sql, ws)
		}
		for _, user := range []string{admin, follower} {
			exec(`DELETE FROM api_tokens WHERE user_id=$1`, user)
			exec(`DELETE FROM users WHERE id=$1`, user)
		}
	})

	h := &Handler{Store: st, Commands: &commands.Service{Store: st}, WorkspaceSlug: ws, BaseURL: "https://zzira.test"}
	call := func(method, path, body string, want int) string {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.SetBasicAuth(admin+"@example.test", admin)
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Code != want {
			t.Fatalf("%s %s: %d want %d: %s", method, path, rec.Code, want, rec.Body.String())
		}
		return rec.Body.String()
	}
	keysFor := func(jql string) []string {
		t.Helper()
		var page struct {
			Issues []struct {
				Key string `json:"key"`
			} `json:"issues"`
		}
		if err := json.Unmarshal([]byte(call(http.MethodGet, "/rest/api/3/search?jql="+url.QueryEscape(jql), "", http.StatusOK)), &page); err != nil {
			t.Fatalf("%s: %v", jql, err)
		}
		keys := make([]string, 0, len(page.Issues))
		for _, issue := range page.Issues {
			keys = append(keys, issue.Key)
		}
		return keys
	}

	stamp := fmt.Sprintf("%06d", time.Now().UnixNano()%1000000)
	key := "ATT" + stamp
	call(http.MethodPost, "/rest/api/3/project",
		`{"key":"`+key+`","name":"Attached fields","projectTypeKey":"software","leadAccountId":"`+admin+`"}`, http.StatusCreated)
	var category map[string]any
	if err := json.Unmarshal([]byte(call(http.MethodPost, "/rest/api/3/projectCategory",
		`{"name":"Delivery `+stamp+`","description":"Work that ships"}`, http.StatusCreated)), &category); err != nil {
		t.Fatal(err)
	}
	call(http.MethodPut, "/rest/api/3/project/"+key, fmt.Sprintf(`{"categoryId":%v}`, category["id"]), http.StatusOK)

	created := func(summary string) string {
		t.Helper()
		var issue struct {
			ID  string `json:"id"`
			Key string `json:"key"`
		}
		if err := json.Unmarshal([]byte(call(http.MethodPost, "/rest/api/3/issue",
			`{"fields":{"project":{"key":"`+key+`"},"summary":"`+summary+`","issuetype":{"name":"Task"}}}`, http.StatusCreated)), &issue); err != nil {
			t.Fatal(err)
		}
		return issue.Key
	}
	subject, other := created("Release notes "+stamp), created("Quiet work "+stamp)

	// What is written about it, who follows it, what hangs off it.
	call(http.MethodPost, "/rest/api/3/issue/"+subject+"/comment",
		`{"body":{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"Shipped on `+stamp+`"}]}]}}`, http.StatusCreated)
	// Creating a work item already watches it, so the watcher that tells the
	// two apart is someone else.
	call(http.MethodPost, "/rest/api/3/issue/"+subject+"/watchers", `"`+follower+`"`, http.StatusNoContent)
	call(http.MethodPost, "/rest/api/3/issue/"+subject+"/votes", "", http.StatusNoContent)
	call(http.MethodPost, "/rest/api/3/issueLink",
		`{"type":{"name":"Blocks"},"inwardIssue":{"key":"`+subject+`"},"outwardIssue":{"key":"`+other+`"}}`, http.StatusCreated)
	// Uploading is exercised by the attachment tests; the search only needs a
	// work item that has one.
	exec(`INSERT INTO attachments(id,issue_id,workspace_id,filename,mime_type,size,blob_ref,author_id)
		SELECT $1,i.id,$2,'notes.txt','text/plain',5,'blob_'||$1,$3 FROM issues i WHERE i.workspace_id=$2 AND i.key=$4`,
		store.NewID("att"), ws, admin, subject)

	only := func(jql, want string) {
		t.Helper()
		if keys := keysFor(jql); len(keys) != 1 || keys[0] != want {
			t.Fatalf("%s matched %v, want only %s", jql, keys, want)
		}
	}
	both := func(jql string) {
		t.Helper()
		if keys := keysFor(jql); len(keys) != 2 {
			t.Fatalf("%s matched %v, want both work items", jql, keys)
		}
	}

	only(`text ~ "Shipped on `+stamp+`"`, subject)
	only(`comment ~ "Shipped on `+stamp+`"`, subject)
	only(`text ~ "Quiet work `+stamp+`"`, other)
	only(`watcher = `+follower, subject)
	both(`watcher = currentUser()`)
	only(`voter = `+admin, subject)
	only(`votes > 0`, subject)
	only(`attachments IS NOT EMPTY`, subject)
	only(`attachments IS EMPTY`, other)
	both(`issueLinkType = Blocks`)
	both(`hierarchyLevel = 0`)
	both(`category = "Delivery ` + stamp + `"`)
	both(`level IS EMPTY`)
	// Neither work item is a service request, so the fields a request has
	// answer for them as absent rather than failing.
	both(`"Request participants" IS EMPTY`)
	both(`request-channel-type IS EMPTY`)
	if keys := keysFor(`project = ` + key + ` ORDER BY votes DESC`); len(keys) != 2 || keys[0] != subject {
		t.Fatalf("order by votes = %v, want the voted work item first", keys)
	}
	if keys := keysFor(`watcher IS EMPTY`); len(keys) != 0 {
		t.Fatalf("watcher IS EMPTY matched %v, and creating work watches it", keys)
	}
	if keys := keysFor(`comment !~ "Shipped on ` + stamp + `"`); len(keys) != 1 || keys[0] != other {
		t.Fatalf("comment !~ matched %v", keys)
	}
}
