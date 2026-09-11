package confluence

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/attachments"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

// TestCustomContent pins Confluence's custom content surface: content an app
// defines, living in a space and optionally under a page, a blog post or other
// custom content, with its own versions, properties and labels.
func TestCustomContent(t *testing.T) {
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
	ws, actor, outsider := store.NewID("ws"), store.NewID("usr"), store.NewID("usr")
	customType := "com.zzira:probe-" + strings.ToLower(store.NewID("t"))
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES ($1,$1,'Custom content test')`, ws)
	for _, id := range []string{actor, outsider} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES ($1,$2,'test','Custom user')`, id, id+"@example.test")
		if id == actor {
			exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES ($1,$2,'admin')`, ws, id)
		}
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES ($1,$1,$2)`, id, store.HashToken(id))
	}
	// A custom content type is registered by the app that defines it.
	exec(`INSERT INTO wiki_custom_content_types(type,body_representation,title) VALUES($1,'storage','Probe widget')`, customType)
	t.Cleanup(func() {
		for _, sql := range []string{
			`DELETE FROM wiki_content_labels WHERE content_id IN (SELECT c.id FROM wiki_content c JOIN wiki_spaces s ON s.id=c.space_id WHERE s.workspace_id=$1)`,
			`DELETE FROM wiki_content_property_versions WHERE property_id IN (SELECT cp.id FROM wiki_content_properties cp JOIN wiki_content c ON c.id=cp.content_id JOIN wiki_spaces s ON s.id=c.space_id WHERE s.workspace_id=$1)`,
			`DELETE FROM wiki_content_properties WHERE content_id IN (SELECT c.id FROM wiki_content c JOIN wiki_spaces s ON s.id=c.space_id WHERE s.workspace_id=$1)`,
			`DELETE FROM wiki_content_versions WHERE content_id IN (SELECT c.id FROM wiki_content c JOIN wiki_spaces s ON s.id=c.space_id WHERE s.workspace_id=$1)`,
			`DELETE FROM wiki_content WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`,
			`DELETE FROM wiki_blog_post_versions WHERE blog_post_id IN (SELECT b.id FROM wiki_blog_posts b JOIN wiki_spaces s ON s.id=b.space_id WHERE s.workspace_id=$1)`,
			`DELETE FROM wiki_blog_posts WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`,
			`DELETE FROM wiki_page_versions WHERE page_id IN (SELECT p.id FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id WHERE s.workspace_id=$1)`,
			`DELETE FROM wiki_pages WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`,
			`DELETE FROM wiki_spaces WHERE workspace_id=$1`,
			`DELETE FROM wiki_labels WHERE workspace_id=$1`,
			`DELETE FROM actions WHERE workspace_id=$1`,
			`DELETE FROM memberships WHERE workspace_id=$1`,
			`DELETE FROM workspaces WHERE id=$1`,
		} {
			exec(sql, ws)
		}
		exec(`DELETE FROM wiki_custom_content_types WHERE type=$1`, customType)
		for _, id := range []string{actor, outsider} {
			exec(`DELETE FROM api_tokens WHERE user_id=$1`, id)
			exec(`DELETE FROM users WHERE id=$1`, id)
		}
	})
	blobs, err := attachments.NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{Store: st, Commands: &commands.Service{Store: st, Blobs: blobs}, Blobs: blobs, WorkspaceSlug: ws, BaseURL: "https://zzira.test"}
	call := func(user, method, path string, body any, want int) *httptest.ResponseRecorder {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(method, "/wiki/api/v2"+path, strings.NewReader(string(raw)))
		if user != "" {
			request.SetBasicAuth(user+"@example.test", user)
		}
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, response.Code, want, response.Body.String())
		}
		return response
	}
	decode := func(response *httptest.ResponseRecorder) map[string]any {
		t.Helper()
		var out map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v %s", err, response.Body.String())
		}
		return out
	}
	titles := func(response *httptest.ResponseRecorder) []string {
		t.Helper()
		body := decode(response)
		results, _ := body["results"].([]any)
		out := make([]string, 0, len(results))
		for _, raw := range results {
			entry, _ := raw.(map[string]any)
			out = append(out, entry["title"].(string))
		}
		return out
	}
	space := decode(call(actor, "POST", "/spaces", map[string]any{"key": "CCT", "name": "Custom content"}, 201))
	spaceID, _ := space["id"].(string)
	page := decode(call(actor, "POST", "/pages", map[string]any{"spaceId": spaceID, "title": "Host page",
		"status": "current", "body": models.WikiBody{Representation: "storage", Value: "<p>host</p>"}}, 200))
	pageID, _ := page["id"].(string)
	blog := decode(call(actor, "POST", "/blogposts", map[string]any{"spaceId": spaceID, "title": "Host post",
		"status": "current", "body": models.WikiBody{Representation: "storage", Value: "<p>post</p>"}}, 200))
	blogID, _ := blog["id"].(string)

	// Custom content can live directly in a space.
	created := decode(call(actor, "POST", "/custom-content", map[string]any{
		"type": customType, "spaceId": spaceID, "title": "Widget one",
		"body": map[string]any{"storage": map[string]any{"value": "<p>one</p>", "representation": "storage"}},
	}, 200))
	id, _ := created["id"].(string)
	if id == "" || created["type"] != customType || created["spaceId"] != spaceID {
		t.Fatalf("created custom content: %v", created)
	}
	// An unregistered type is not content anyone can create.
	call(actor, "POST", "/custom-content", map[string]any{"type": "com.zzira:nope", "spaceId": spaceID, "title": "x"}, 404)
	// Naming two parents is a contradiction rather than a precedence question.
	call(actor, "POST", "/custom-content", map[string]any{
		"type": customType, "pageId": pageID, "blogPostId": blogID, "title": "x"}, 400)
	call(actor, "POST", "/custom-content", map[string]any{"type": customType, "title": "x"}, 400)
	// Two body representations at once is the same kind of contradiction.
	call(actor, "POST", "/custom-content", map[string]any{
		"type": customType, "spaceId": spaceID, "title": "x",
		"body": map[string]any{
			"storage": map[string]any{"value": "<p>a</p>", "representation": "storage"},
			"raw":     map[string]any{"value": "b", "representation": "raw"},
		}}, 400)

	read := decode(call(actor, "GET", "/custom-content/"+id+"?body-format=storage", nil, 200))
	body, _ := read["body"].(map[string]any)
	storage, _ := body["storage"].(map[string]any)
	if storage["value"] != "<p>one</p>" || storage["representation"] != "storage" {
		t.Fatalf("custom content body: %v", read)
	}
	call(actor, "GET", "/custom-content/"+id+"?body-format=atlas_doc_format", nil, 400)
	// Someone outside the workspace does not get as far as the content.
	call(outsider, "GET", "/custom-content/"+id, nil, 403)

	// The update states the version it replaces, and a stale one is refused.
	updated := decode(call(actor, "PUT", "/custom-content/"+id, map[string]any{
		"type": customType, "title": "Widget two", "version": map[string]any{"number": 2, "message": "second"},
		"body": map[string]any{"storage": map[string]any{"value": "<p>two</p>", "representation": "storage"}},
	}, 200))
	if updated["title"] != "Widget two" {
		t.Fatalf("updated custom content: %v", updated)
	}
	stale := call(actor, "PUT", "/custom-content/"+id, map[string]any{
		"title": "Widget three", "version": map[string]any{"number": 2}}, 409)
	// The message must not tell a caller a page changed when none did.
	if strings.Contains(stale.Body.String(), "page changed") {
		t.Fatalf("conflict message names the wrong thing: %s", stale.Body.String())
	}
	call(actor, "PUT", "/custom-content/"+id, map[string]any{"title": "x"}, 400)
	// The type is what the app defined; a write cannot change it.
	call(actor, "PUT", "/custom-content/"+id, map[string]any{
		"type": "com.zzira:something-else", "title": "x", "version": map[string]any{"number": 3}}, 400)

	// Both versions are kept, and an earlier one reads back with its body.
	versions := decode(call(actor, "GET", "/custom-content/"+id+"/versions", nil, 200))
	versionResults, _ := versions["results"].([]any)
	if len(versionResults) != 2 {
		t.Fatalf("custom content versions: %v", versions)
	}
	first := decode(call(actor, "GET", "/custom-content/"+id+"/versions/1?body-format=storage", nil, 200))
	firstBody, _ := first["body"].(map[string]any)
	firstStorage, _ := firstBody["storage"].(map[string]any)
	if first["number"].(float64) != 1 || firstStorage["value"] != "<p>one</p>" {
		t.Fatalf("earlier version: %v", first)
	}
	call(actor, "GET", "/custom-content/"+id+"/versions/9", nil, 404)
	call(actor, "GET", "/custom-content/"+id+"/versions/zero", nil, 400)

	// The three parents, and the reads that report each one's custom content.
	underPage := decode(call(actor, "POST", "/custom-content", map[string]any{
		"type": customType, "pageId": pageID, "title": "Page widget"}, 200))
	if underPage["pageId"] != pageID {
		t.Fatalf("page-parented custom content: %v", underPage)
	}
	underBlog := decode(call(actor, "POST", "/custom-content", map[string]any{
		"type": customType, "blogPostId": blogID, "title": "Blog widget"}, 200))
	if underBlog["blogPostId"] != blogID {
		t.Fatalf("blog-parented custom content: %v", underBlog)
	}
	child := decode(call(actor, "POST", "/custom-content", map[string]any{
		"type": customType, "customContentId": id, "title": "Child widget"}, 200))
	childID, _ := child["id"].(string)
	if child["customContentId"] != id {
		t.Fatalf("custom-parented custom content: %v", child)
	}
	if got := titles(call(actor, "GET", "/pages/"+pageID+"/custom-content?type="+customType, nil, 200)); len(got) != 1 || got[0] != "Page widget" {
		t.Fatalf("page custom content: %v", got)
	}
	if got := titles(call(actor, "GET", "/blogposts/"+blogID+"/custom-content?type="+customType, nil, 200)); len(got) != 1 || got[0] != "Blog widget" {
		t.Fatalf("blog post custom content: %v", got)
	}
	if got := titles(call(actor, "GET", "/custom-content/"+id+"/children", nil, 200)); len(got) != 1 || got[0] != "Child widget" {
		t.Fatalf("custom content children: %v", got)
	}

	// The space and global listings, and the type the global one requires.
	if got := titles(call(actor, "GET", "/spaces/"+spaceID+"/custom-content?type="+customType, nil, 200)); len(got) != 4 {
		t.Fatalf("space custom content: %v", got)
	}
	if got := titles(call(actor, "GET", "/custom-content?type="+customType+"&sort=title", nil, 200)); len(got) != 4 || got[0] != "Blog widget" {
		t.Fatalf("global custom content: %v", got)
	}
	call(actor, "GET", "/custom-content", nil, 400)
	call(actor, "GET", "/custom-content?type=com.zzira:nope", nil, 404)
	call(actor, "GET", "/custom-content?type="+customType+"&sort=sideways", nil, 400)
	call(outsider, "GET", "/custom-content?type="+customType, nil, 403)

	// Properties, labels and operations hang off it like any other content.
	property := decode(call(actor, "POST", "/custom-content/"+id+"/properties",
		map[string]any{"key": "widget-state", "value": map[string]any{"open": true}}, 200))
	propertyID, _ := property["id"].(string)
	if propertyID == "" {
		t.Fatalf("created property: %v", property)
	}
	call(actor, "PUT", "/custom-content/"+id+"/properties/"+propertyID,
		map[string]any{"key": "widget-state", "value": map[string]any{"open": false}, "version": map[string]any{"number": 2}}, 200)
	if stored := decode(call(actor, "GET", "/custom-content/"+id+"/properties/"+propertyID, nil, 200)); stored["key"] != "widget-state" {
		t.Fatalf("property read: %v", stored)
	}
	withProperties := decode(call(actor, "GET", "/custom-content/"+id+"?include-properties=true&include-labels=true&include-operations=true", nil, 200))
	for _, key := range []string{"properties", "labels", "operations"} {
		if _, ok := withProperties[key]; !ok {
			t.Fatalf("%s missing from the expanded read: %v", key, withProperties)
		}
	}
	call(actor, "DELETE", "/custom-content/"+id+"/properties/"+propertyID, nil, 204)
	call(actor, "GET", "/custom-content/"+id+"/labels", nil, 200)
	call(actor, "GET", "/custom-content/"+id+"/operations", nil, 200)
	call(actor, "GET", "/custom-content/"+id+"/attachments", nil, 200)
	call(actor, "GET", "/custom-content/"+id+"/footer-comments", nil, 200)
	call(actor, "GET", "/custom-content/9999999/labels", nil, 404)

	// Deleting the parent is refused while a child is still there.
	call(actor, "DELETE", "/custom-content/"+id, nil, 400)
	call(actor, "DELETE", "/custom-content/"+childID, nil, 204)
	call(actor, "DELETE", "/custom-content/"+id, nil, 204)
	call(actor, "GET", "/custom-content/"+id, nil, 404)
	if got := titles(call(actor, "GET", "/custom-content?type="+customType, nil, 200)); len(got) != 2 {
		t.Fatalf("after deletion: %v", got)
	}
}
