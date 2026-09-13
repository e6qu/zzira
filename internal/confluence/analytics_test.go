package confluence

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/attachments"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

// TestContentAnalytics pins Confluence's content analytics: how many times
// content was viewed, and by how many distinct people.
func TestContentAnalytics(t *testing.T) {
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
	ws, author, reader := store.NewID("ws"), store.NewID("usr"), store.NewID("usr")
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES ($1,$1,'Analytics test')`, ws)
	// The reader is an ordinary member: an administrator sees past a page
	// restriction and would not show whether analytics respect one.
	for _, person := range []struct{ id, role string }{{author, "admin"}, {reader, "member"}} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES ($1,$2,'test','Analytics user')`, person.id, person.id+"@example.test")
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES ($1,$2,$3)`, ws, person.id, person.role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES ($1,$1,$2)`, person.id, store.HashToken(person.id))
	}
	t.Cleanup(func() {
		for _, sql := range []string{
			`DELETE FROM wiki_content_views WHERE workspace_id=$1`,
			`DELETE FROM wiki_page_restrictions WHERE page_id IN (SELECT p.id FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id WHERE s.workspace_id=$1)`,
			`DELETE FROM wiki_blog_post_versions WHERE blog_post_id IN (SELECT b.id FROM wiki_blog_posts b JOIN wiki_spaces s ON s.id=b.space_id WHERE s.workspace_id=$1)`,
			`DELETE FROM wiki_blog_posts WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`,
			`DELETE FROM wiki_page_versions WHERE page_id IN (SELECT p.id FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id WHERE s.workspace_id=$1)`,
			`UPDATE wiki_spaces SET homepage_id=NULL WHERE workspace_id=$1`,
			`DELETE FROM wiki_pages WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`,
			`DELETE FROM wiki_spaces WHERE workspace_id=$1`,
			`DELETE FROM actions WHERE workspace_id=$1`,
			`DELETE FROM memberships WHERE workspace_id=$1`,
			`DELETE FROM workspaces WHERE id=$1`,
		} {
			exec(sql, ws)
		}
		for _, id := range []string{author, reader} {
			exec(`DELETE FROM api_tokens WHERE user_id=$1`, id)
			exec(`DELETE FROM users WHERE id=$1`, id)
		}
	})
	blobs, err := attachments.NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{Store: st, Commands: &commands.Service{Store: st, Blobs: blobs}, Blobs: blobs, WorkspaceSlug: ws, BaseURL: "https://zzira.test"}
	v1 := &V1Handler{Handler: h}
	send := func(handler http.Handler, prefix, user, method, path string, body any, want int) *httptest.ResponseRecorder {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(method, prefix+path, strings.NewReader(string(raw)))
		request.SetBasicAuth(user+"@example.test", user)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, response.Code, want, response.Body.String())
		}
		return response
	}
	callV2 := func(user, method, path string, body any, want int) *httptest.ResponseRecorder {
		t.Helper()
		return send(h, "/wiki/api/v2", user, method, path, body, want)
	}
	object := func(response *httptest.ResponseRecorder) map[string]any {
		t.Helper()
		var out map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v %s", err, response.Body.String())
		}
		return out
	}
	// count reads one analytics number. Analytics are judged by the number a
	// reader gets back, never by the status code alone.
	count := func(user, id, metric, query string) float64 {
		t.Helper()
		path := "/analytics/content/" + id + "/" + metric
		if query != "" {
			path += "?" + query
		}
		body := object(send(v1, "/wiki/rest/api", user, "GET", path, nil, 200))
		if body["id"] == nil {
			t.Fatalf("%s: no id in %v", path, body)
		}
		value, _ := body["count"].(float64)
		return value
	}

	space := object(callV2(author, "POST", "/spaces", map[string]any{"key": "STATS", "name": "Analytics"}, 201))
	spaceID, _ := space["id"].(string)
	page := object(callV2(author, "POST", "/pages", map[string]any{"spaceId": spaceID, "title": "Popular page",
		"status": "current", "body": models.WikiBody{Representation: "storage", Value: "<p>read me</p>"}}, 200))
	pageID, _ := page["id"].(string)

	// Content nobody has opened has no views and no viewers.
	if views, viewers := count(author, pageID, "views", ""), count(author, pageID, "viewers", ""); views != 0 || viewers != 0 {
		t.Fatalf("before anyone read it: views %v viewers %v", views, viewers)
	}
	// Creating the page is not viewing it, so the author's write left no view.

	// Every read is a view; distinct viewers count each person once.
	callV2(reader, "GET", "/pages/"+pageID, nil, 200)
	callV2(reader, "GET", "/pages/"+pageID, nil, 200)
	callV2(author, "GET", "/pages/"+pageID, nil, 200)
	if views := count(author, pageID, "views", ""); views != 3 {
		t.Fatalf("three reads: views %v", views)
	}
	if viewers := count(author, pageID, "viewers", ""); viewers != 2 {
		t.Fatalf("two people: viewers %v", viewers)
	}
	// The id comes back as the number it is.
	body := object(send(v1, "/wiki/rest/api", author, "GET", "/analytics/content/"+pageID+"/views", nil, 200))
	if _, isNumber := body["id"].(float64); !isNumber {
		t.Fatalf("id should be a number: %v", body)
	}
	// Reading the analytics is not reading the content, so it adds no view.
	if views := count(author, pageID, "views", ""); views != 3 {
		t.Fatalf("reading analytics counted as a view: %v", views)
	}
	// A listing shows content without opening it, so it adds no view either.
	callV2(reader, "GET", "/pages?space-id="+spaceID, nil, 200)
	if views := count(author, pageID, "views", ""); views != 3 {
		t.Fatalf("a listing counted as a view: %v", views)
	}

	// fromDate counts from that moment: everything so far is before tomorrow
	// and after a year ago.
	if views := count(author, pageID, "views", "fromDate=2999-01-01"); views != 0 {
		t.Fatalf("views from the future: %v", views)
	}
	if viewers := count(author, pageID, "viewers", "fromDate=2000-01-01T00:00:00Z"); viewers != 2 {
		t.Fatalf("viewers since 2000: %v", viewers)
	}
	send(v1, "/wiki/rest/api", author, "GET", "/analytics/content/"+pageID+"/views?fromDate=yesterday", nil, 400)
	send(v1, "/wiki/rest/api", author, "GET", "/analytics/content/"+pageID+"/views?unknown=1", nil, 400)

	// A blog post is content too, and its views are its own.
	post := object(callV2(author, "POST", "/blogposts", map[string]any{"spaceId": spaceID, "title": "Announcement",
		"status": "current", "body": models.WikiBody{Representation: "storage", Value: "<p>news</p>"}}, 200))
	postID, _ := post["id"].(string)
	callV2(reader, "GET", "/blogposts/"+postID, nil, 200)
	if postID != pageID {
		if views := count(author, postID, "views", ""); views != 1 {
			t.Fatalf("blog post views: %v", views)
		}
	}
	if views := count(author, pageID, "views", ""); views != 3 {
		t.Fatalf("a blog post view was counted on the page: %v", views)
	}

	// Content that does not exist, or is not an id at all, is a 404.
	send(v1, "/wiki/rest/api", author, "GET", "/analytics/content/999999999/views", nil, 404)
	send(v1, "/wiki/rest/api", author, "GET", "/analytics/content/not-a-number/viewers", nil, 404)

	// Analytics never reveal content a reader may not open. Once the page is
	// restricted to its author, the reader's analytics read is a 404 exactly as
	// the page itself is, and the reader's failed read is not a view.
	send(v1, "/wiki/rest/api", author, "PUT", "/content/"+pageID+"/restriction",
		[]map[string]any{{"operation": "read", "restrictions": map[string]any{"user": []map[string]string{{"accountId": author}}}}}, 200)
	callV2(reader, "GET", "/pages/"+pageID, nil, 404)
	send(v1, "/wiki/rest/api", reader, "GET", "/analytics/content/"+pageID+"/views", nil, 404)
	if views := count(author, pageID, "views", ""); views != 3 {
		t.Fatalf("a refused read was counted: %v", views)
	}
}
