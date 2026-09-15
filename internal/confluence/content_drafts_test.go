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
	"github.com/e6qu/zzira/internal/store"
)

// TestContentDraftsAndDeletion pins Confluence's draft and deletion lifecycle
// for pages and blog posts, the blog post read that matches the page read, and
// v1 labels on content that is not a page.
func TestContentDraftsAndDeletion(t *testing.T) {
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
	ws, actor, other := store.NewID("ws"), store.NewID("usr"), store.NewID("usr")
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES ($1,$1,'Drafts test')`, ws)
	for _, user := range []struct{ id, role string }{{actor, "admin"}, {other, "member"}} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES ($1,$2,'test',$1)`, user.id, user.id+"@example.test")
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES ($1,$2,$3)`, ws, user.id, user.role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES ($1,$1,$2)`, user.id, store.HashToken(user.id))
	}
	t.Cleanup(func() {
		for _, sql := range []string{
			`DELETE FROM wiki_content_drafts WHERE workspace_id=$1`,
			`DELETE FROM wiki_relations WHERE workspace_id=$1`,
			`DELETE FROM wiki_blog_post_labels WHERE blog_post_id IN (SELECT b.id FROM wiki_blog_posts b JOIN wiki_spaces s ON s.id=b.space_id WHERE s.workspace_id=$1)`,
			`DELETE FROM wiki_blog_post_versions WHERE blog_post_id IN (SELECT b.id FROM wiki_blog_posts b JOIN wiki_spaces s ON s.id=b.space_id WHERE s.workspace_id=$1)`,
			`DELETE FROM wiki_blog_posts WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`,
			`DELETE FROM wiki_page_versions WHERE page_id IN (SELECT p.id FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id WHERE s.workspace_id=$1)`,
			`DELETE FROM wiki_pages WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`,
			`DELETE FROM wiki_spaces WHERE workspace_id=$1`,
			`DELETE FROM wiki_labels WHERE workspace_id=$1`,
			`DELETE FROM actions WHERE workspace_id=$1`,
			`DELETE FROM notifications WHERE workspace_id=$1`,
			`DELETE FROM memberships WHERE workspace_id=$1`,
			`DELETE FROM workspaces WHERE id=$1`,
		} {
			exec(sql, ws)
		}
		for _, user := range []string{actor, other} {
			exec(`DELETE FROM api_tokens WHERE user_id=$1`, user)
			exec(`DELETE FROM users WHERE id=$1`, user)
		}
	})
	blobs, err := attachments.NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{Store: st, Commands: &commands.Service{Store: st, Blobs: blobs}, Blobs: blobs, WorkspaceSlug: ws, BaseURL: "https://zzira.test"}
	v1 := &V1Handler{Handler: h}
	send := func(handler http.Handler, user, prefix, method, path string, body any, want int) map[string]any {
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
			t.Fatalf("%s %s as %s: got %d want %d: %s", method, path, user, response.Code, want, response.Body.String())
		}
		out := map[string]any{}
		if response.Body.Len() > 0 && strings.HasPrefix(strings.TrimSpace(response.Body.String()), "{") {
			if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
				t.Fatalf("decode %s: %v", path, err)
			}
		}
		return out
	}
	v2 := func(method, path string, body any, want int) map[string]any {
		t.Helper()
		return send(h, actor, "/wiki/api/v2", method, path, body, want)
	}
	asMember := func(method, path string, body any, want int) map[string]any {
		t.Helper()
		return send(h, other, "/wiki/api/v2", method, path, body, want)
	}
	callV1 := func(method, path string, body any, want int) map[string]any {
		t.Helper()
		return send(v1, actor, "/wiki/rest/api", method, path, body, want)
	}
	dig := func(value any, path ...string) any {
		for _, key := range path {
			object, _ := value.(map[string]any)
			value = object[key]
		}
		return value
	}
	idOf := func(body map[string]any) string {
		t.Helper()
		id, _ := body["id"].(string)
		if id == "" {
			t.Fatalf("no id in %v", body)
		}
		return id
	}
	storage := func(value string) map[string]any { return map[string]any{"representation": "storage", "value": value} }
	space := idOf(v2("POST", "/spaces", map[string]any{"key": "DRF", "name": "Drafts"}, 201))

	// A draft of a published page leaves the published page as readers see it.
	page := idOf(v2("POST", "/pages?root-level=true", map[string]any{"spaceId": space, "title": "Runbook", "status": "current", "body": storage("<p>Published</p>")}, 200))
	v2("PUT", "/pages/"+page, map[string]any{"id": page, "status": "draft", "title": "Runbook draft", "body": storage("<p>Draft</p>"), "version": map[string]any{"number": 2}}, 400)
	saved := v2("PUT", "/pages/"+page, map[string]any{"id": page, "status": "draft", "title": "Runbook draft", "body": storage("<p>Draft</p>"), "version": map[string]any{"number": 1}}, 200)
	if saved["status"] != "draft" || dig(saved, "body", "storage", "value") != "<p>Draft</p>" {
		t.Fatalf("saved draft: %v", saved)
	}
	if got := v2("GET", "/pages/"+page+"?body-format=storage", nil, 200); got["status"] != "current" || dig(got, "body", "storage", "value") != "<p>Published</p>" || got["title"] != "Runbook" {
		t.Fatalf("the published page changed: %v", got)
	}
	if got := v2("GET", "/pages/"+page+"?get-draft=true&body-format=storage", nil, 200); got["status"] != "draft" || got["title"] != "Runbook draft" || dig(got, "body", "storage", "value") != "<p>Draft</p>" {
		t.Fatalf("draft read: %v", got)
	}
	// Publishing replaces the draft.
	v2("PUT", "/pages/"+page, map[string]any{"id": page, "status": "current", "title": "Runbook", "body": storage("<p>Published again</p>"), "version": map[string]any{"number": 2}}, 200)
	v2("GET", "/pages/"+page+"?get-draft=true", nil, 404)
	// Discarding a draft removes it for good.
	v2("PUT", "/pages/"+page, map[string]any{"id": page, "status": "draft", "title": "Second draft", "body": storage("<p>Second draft</p>"), "version": map[string]any{"number": 1}}, 200)
	v2("DELETE", "/pages/"+page+"?draft=true", nil, 204)
	v2("GET", "/pages/"+page+"?get-draft=true", nil, 404)
	v2("DELETE", "/pages/"+page+"?draft=true", nil, 404)

	// A page that was never published is a draft, and discarding it removes it.
	idea := idOf(v2("POST", "/pages?root-level=true", map[string]any{"spaceId": space, "title": "Idea", "status": "draft", "body": storage("<p>Idea</p>")}, 200))
	v2("DELETE", "/pages/"+idea, nil, 400)
	v2("DELETE", "/pages/"+idea+"?draft=true", nil, 204)
	v2("GET", "/pages/"+idea+"?status=draft", nil, 404)

	// Trash, then purge: purged content is for space administrators to see
	// and restore.
	v2("DELETE", "/pages/"+page+"?purge=true", nil, 400)
	v2("DELETE", "/pages/"+page, nil, 204)
	v2("DELETE", "/pages/"+page, nil, 400)
	v2("DELETE", "/pages/"+page+"?purge=true&draft=true", nil, 400)
	asMember("DELETE", "/pages/"+page+"?purge=true", nil, 403)
	v2("DELETE", "/pages/"+page+"?purge=true", nil, 204)
	v2("GET", "/pages/"+page+"?status=trashed", nil, 404)
	deleted := v2("GET", "/pages/"+page+"?status=deleted&body-format=storage", nil, 200)
	if deleted["status"] != "deleted" || dig(deleted, "body", "storage", "value") != "<p>Published again</p>" {
		t.Fatalf("deleted page: %v", deleted)
	}
	asMember("GET", "/pages/"+page+"?status=deleted", nil, 404)
	listed := func(user func(string, string, any, int) map[string]any, path string) string {
		t.Helper()
		results, _ := user("GET", path, nil, 200)["results"].([]any)
		titles := []string{}
		for _, raw := range results {
			titles = append(titles, raw.(map[string]any)["title"].(string))
		}
		return strings.Join(titles, ",")
	}
	if got := listed(v2, "/pages?space-id="+space+"&status=deleted"); got != "Runbook" {
		t.Fatalf("deleted pages for an administrator: %q", got)
	}
	if got := listed(asMember, "/pages?space-id="+space+"&status=deleted"); got != "" {
		t.Fatalf("deleted pages for a member: %q", got)
	}
	version := int(dig(deleted, "version", "number").(float64))
	if restored := v2("PUT", "/pages/"+page, map[string]any{"id": page, "status": "current", "title": "Ignored", "body": storage("<p>Ignored</p>"), "version": map[string]any{"number": version + 1}}, 200); restored["status"] != "current" || restored["title"] != "Runbook" {
		t.Fatalf("restored page: %v", restored)
	}

	// Blog posts are written and read as pages are.
	post := idOf(v2("POST", "/blogposts", map[string]any{"spaceId": space, "title": "News", "status": "current", "body": map[string]any{"wiki": map[string]any{"representation": "wiki", "value": "h2. Shipped"}}}, 200))
	if got := v2("GET", "/blogposts/"+post+"?body-format=storage", nil, 200); dig(got, "body", "storage", "value") != "<h2>Shipped</h2>" {
		t.Fatalf("blog post from wiki markup: %v", got)
	}
	if got, _ := dig(v2("GET", "/blogposts/"+post+"?body-format=atlas_doc_format", nil, 200), "body", "atlas_doc_format", "value").(string); !strings.Contains(got, "Shipped") {
		t.Fatalf("blog post in the document format: %q", got)
	}
	v2("GET", "/blogposts/"+post+"?body-format=bogus", nil, 400)
	if err := st.SetWikiBlogPostFavourite(ctx, ws, actor, post, true); err != nil {
		t.Fatal(err)
	}
	included := v2("GET", "/blogposts/"+post+"?include-labels=true&include-properties=true&include-operations=true&include-likes=true&include-versions=true&include-collaborators=true&include-favorited-by-current-user-status=true&include-webresources=true&include-version=false", nil, 200)
	for _, key := range []string{"labels", "properties", "operations", "likes", "versions", "collaborators", "webresources"} {
		if included[key] == nil {
			t.Fatalf("blog post read is missing %s: %v", key, included)
		}
	}
	if included["isFavoritedByCurrentUser"] != true || included["version"] != nil {
		t.Fatalf("blog post star and version omission: %v", included)
	}
	if collaborators, _ := dig(included, "collaborators", "results").([]any); len(collaborators) != 1 || dig(collaborators[0], "accountId") != actor {
		t.Fatalf("blog post collaborators: %v", included["collaborators"])
	}
	v2("PUT", "/blogposts/"+post, map[string]any{"id": post, "status": "current", "title": "News", "body": storage("<p>Shipped, revised</p>"), "version": map[string]any{"number": 2}}, 200)
	if got := v2("GET", "/blogposts/"+post+"?version=1&status=historical&body-format=storage", nil, 200); got["status"] != "historical" || dig(got, "body", "storage", "value") != "<h2>Shipped</h2>" {
		t.Fatalf("historical blog post: %v", got)
	}
	v2("PUT", "/blogposts/"+post, map[string]any{"id": post, "status": "draft", "title": "News draft", "body": storage("<p>Not yet</p>"), "version": map[string]any{"number": 1}}, 200)
	if got := v2("GET", "/blogposts/"+post+"?get-draft=true", nil, 200); got["status"] != "draft" || got["title"] != "News draft" {
		t.Fatalf("blog post draft: %v", got)
	}
	v2("DELETE", "/blogposts/"+post+"?draft=true", nil, 204)
	v2("GET", "/blogposts/"+post+"?get-draft=true", nil, 404)
	v2("GET", "/blogposts?space-id="+space+"&status=draft", nil, 400)
	v2("DELETE", "/blogposts/"+post, nil, 204)
	if got := listed(v2, "/blogposts?space-id="+space+"&status=current,trashed"); got != "News" {
		t.Fatalf("blog posts across statuses: %q", got)
	}
	v2("DELETE", "/blogposts/"+post+"?purge=true", nil, 204)
	v2("GET", "/blogposts/"+post+"?status=deleted", nil, 200)
	asMember("GET", "/blogposts/"+post+"?status=deleted", nil, 404)
	restoredPost := v2("GET", "/blogposts/"+post+"?status=deleted", nil, 200)
	v2("PUT", "/blogposts/"+post, map[string]any{"id": post, "status": "current", "title": "News", "body": storage("<p>x</p>"), "version": map[string]any{"number": int(dig(restoredPost, "version", "number").(float64)) + 1}}, 200)

	// v1 labels follow the content's kind.
	callV1("POST", "/content/"+post+"/label", []map[string]string{{"prefix": "global", "name": "release-note"}}, 200)
	if labels, _ := v2("GET", "/blogposts/"+post+"/labels", nil, 200)["results"].([]any); len(labels) != 1 || dig(labels[0], "name") != "release-note" {
		t.Fatalf("blog post labels: %v", labels)
	}
	byLabel := callV1("GET", "/label?name=release-note&type=blogpost", nil, 200)
	if results, _ := dig(byLabel, "associatedContents", "results").([]any); len(results) != 1 || dig(results[0], "id") != post {
		t.Fatalf("blog posts by label: %v", byLabel)
	}
	if results, _ := dig(callV1("GET", "/label?name=release-note&type=page", nil, 200), "associatedContents", "results").([]any); len(results) != 0 {
		t.Fatalf("pages by a blog post label: %v", results)
	}
	callV1("GET", "/label?name=release-note&type=bogus", nil, 400)
	callV1("DELETE", "/content/"+post+"/label?name=release-note", nil, 204)
	if labels, _ := v2("GET", "/blogposts/"+post+"/labels", nil, 200)["results"].([]any); len(labels) != 0 {
		t.Fatalf("the label stayed on the blog post: %v", labels)
	}
}

// TestContentVersionBodies pins what a version list says about each version.
func TestContentVersionBodies(t *testing.T) {
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
	ws, actor := store.NewID("ws"), store.NewID("usr")
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES ($1,$1,'Versions test')`, ws)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES ($1,$2,'test',$1)`, actor, actor+"@example.test")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES ($1,$2,'admin')`, ws, actor)
	exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES ($1,$1,$2)`, actor, store.HashToken(actor))
	t.Cleanup(func() {
		for _, sql := range []string{
			`DELETE FROM wiki_blog_post_versions WHERE blog_post_id IN (SELECT b.id FROM wiki_blog_posts b JOIN wiki_spaces s ON s.id=b.space_id WHERE s.workspace_id=$1)`,
			`DELETE FROM wiki_blog_posts WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`,
			`DELETE FROM wiki_page_versions WHERE page_id IN (SELECT p.id FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id WHERE s.workspace_id=$1)`,
			`DELETE FROM wiki_pages WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`,
			`DELETE FROM wiki_spaces WHERE workspace_id=$1`,
			`DELETE FROM actions WHERE workspace_id=$1`,
			`DELETE FROM notifications WHERE workspace_id=$1`,
			`DELETE FROM memberships WHERE workspace_id=$1`,
			`DELETE FROM workspaces WHERE id=$1`,
		} {
			exec(sql, ws)
		}
		exec(`DELETE FROM api_tokens WHERE user_id=$1`, actor)
		exec(`DELETE FROM users WHERE id=$1`, actor)
	})
	blobs, err := attachments.NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{Store: st, Commands: &commands.Service{Store: st, Blobs: blobs}, Blobs: blobs, WorkspaceSlug: ws, BaseURL: "https://zzira.test"}
	call := func(method, path string, body any, want int) map[string]any {
		t.Helper()
		raw, _ := json.Marshal(body)
		request := httptest.NewRequest(method, "/wiki/api/v2"+path, strings.NewReader(string(raw)))
		request.SetBasicAuth(actor+"@example.test", actor)
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, response.Code, want, response.Body.String())
		}
		out := map[string]any{}
		_ = json.Unmarshal(response.Body.Bytes(), &out)
		return out
	}
	storage := func(value string) map[string]any { return map[string]any{"representation": "storage", "value": value} }
	space, _ := call("POST", "/spaces", map[string]any{"key": "VER", "name": "Versions"}, 201)["id"].(string)
	for _, kind := range []struct{ collection, field string }{{"pages", "page"}, {"blogposts", "blogpost"}} {
		id, _ := call("POST", "/"+kind.collection, map[string]any{"spaceId": space, "title": "First " + kind.field, "status": "current", "body": storage("<p>One</p>")}, 200)["id"].(string)
		call("PUT", "/"+kind.collection+"/"+id, map[string]any{"id": id, "status": "current", "title": "Second " + kind.field, "body": storage("<p>Two</p>"), "version": map[string]any{"number": 2}}, 200)
		results, _ := call("GET", "/"+kind.collection+"/"+id+"/versions?body-format=storage&sort=modified-date", nil, 200)["results"].([]any)
		if len(results) != 2 {
			t.Fatalf("%s versions: %v", kind.collection, results)
		}
		first := results[0].(map[string]any)[kind.field].(map[string]any)
		second := results[1].(map[string]any)[kind.field].(map[string]any)
		if first["title"] != "First "+kind.field || first["body"].(map[string]any)["storage"].(map[string]any)["value"] != "<p>One</p>" ||
			second["body"].(map[string]any)["storage"].(map[string]any)["value"] != "<p>Two</p>" || first["id"] != id {
			t.Fatalf("%s version bodies: %v", kind.collection, results)
		}
		plain, _ := call("GET", "/"+kind.collection+"/"+id+"/versions", nil, 200)["results"].([]any)
		if _, hasBody := plain[0].(map[string]any)[kind.field].(map[string]any)["body"]; hasBody {
			t.Fatalf("%s versions carried bodies nobody asked for: %v", kind.collection, plain)
		}
		call("GET", "/"+kind.collection+"/"+id+"/versions?body-format=view", nil, 400)
		call("GET", "/"+kind.collection+"/"+id+"/versions?sort=title", nil, 400)
		detail := call("GET", "/"+kind.collection+"/"+id+"/versions/1", nil, 200)
		if collaborators, _ := detail["collaborators"].([]any); len(collaborators) != 1 || collaborators[0] != actor {
			t.Fatalf("%s version collaborators: %v", kind.collection, detail)
		}
	}
}
