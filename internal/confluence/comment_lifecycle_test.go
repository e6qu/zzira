package confluence

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/attachments"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

// TestCommentLifecycle pins comments written and read in every body form,
// their include flags and statuses, inline comments following their passage
// through page edits, comments on custom content, and stars as favourite
// relations.
func TestCommentLifecycle(t *testing.T) {
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
	customType := "ac:zzira:comment-widget"
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES ($1,$1,'Comments test')`, ws)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES ($1,$2,'test',$1)`, actor, actor+"@example.test")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES ($1,$2,'admin')`, ws, actor)
	exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES ($1,$1,$2)`, actor, store.HashToken(actor))
	exec(`INSERT INTO wiki_custom_content_types(type,body_representation,title) VALUES ($1,'storage','Comment widget') ON CONFLICT DO NOTHING`, customType)
	t.Cleanup(func() {
		for _, sql := range []string{
			`DELETE FROM wiki_relations WHERE workspace_id=$1`,
			`DELETE FROM wiki_footer_comments WHERE custom_content_id IN (SELECT c.id FROM wiki_content c JOIN wiki_spaces s ON s.id=c.space_id WHERE s.workspace_id=$1)`,
			`DELETE FROM wiki_content WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`,
			`DELETE FROM wiki_footer_comments WHERE page_id IN (SELECT p.id FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id WHERE s.workspace_id=$1)`,
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
		exec(`DELETE FROM wiki_custom_content_types WHERE type=$1 AND NOT EXISTS(SELECT 1 FROM wiki_content WHERE custom_type=$1)`, customType)
	})
	blobs, err := attachments.NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{Store: st, Commands: &commands.Service{Store: st, Blobs: blobs}, Blobs: blobs, WorkspaceSlug: ws, BaseURL: "https://zzira.test"}
	v1 := &V1Handler{Handler: h}
	send := func(handler http.Handler, prefix, method, path string, body any, want int) map[string]any {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(method, prefix+path, strings.NewReader(string(raw)))
		request.SetBasicAuth(actor+"@example.test", actor)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, response.Code, want, response.Body.String())
		}
		out := map[string]any{}
		if strings.HasPrefix(strings.TrimSpace(response.Body.String()), "{") {
			if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
				t.Fatalf("decode %s: %v", path, err)
			}
		}
		return out
	}
	v2 := func(method, path string, body any, want int) map[string]any {
		t.Helper()
		return send(h, "/wiki/api/v2", method, path, body, want)
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
	space := idOf(v2("POST", "/spaces", map[string]any{"key": "CMT", "name": "Comments"}, 201))
	page := idOf(v2("POST", "/pages", map[string]any{"spaceId": space, "title": "Release notes", "status": "current", "body": storage("<p>Alpha beta gamma beta</p>")}, 200))

	// Comments are written in any body form and read in any format.
	comment := idOf(v2("POST", "/footer-comments", map[string]any{"pageId": page, "body": map[string]any{"wiki": map[string]any{"representation": "wiki", "value": "*Looks good*"}}}, 201))
	if got := dig(v2("GET", "/footer-comments/"+comment+"?body-format=storage", nil, 200), "body", "storage", "value"); got != "<p><strong>Looks good</strong></p>" {
		t.Fatalf("comment from wiki markup: %v", got)
	}
	if got, _ := dig(v2("GET", "/footer-comments/"+comment+"?body-format=atlas_doc_format", nil, 200), "body", "atlas_doc_format", "value").(string); !strings.Contains(got, "Looks good") {
		t.Fatalf("comment in the document format: %q", got)
	}
	v2("GET", "/footer-comments/"+comment+"?body-format=view", nil, 200)
	v2("GET", "/pages/"+page+"/footer-comments?body-format=view", nil, 400)
	if listed, _ := v2("GET", "/pages/"+page+"/footer-comments?body-format=atlas_doc_format", nil, 200)["results"].([]any); len(listed) != 1 || dig(listed[0], "body", "atlas_doc_format") == nil {
		t.Fatalf("comments in the document format: %v", listed)
	}
	included := v2("GET", "/footer-comments/"+comment+"?include-properties=true&include-operations=true&include-likes=true&include-versions=true&include-version=false", nil, 200)
	for _, key := range []string{"properties", "operations", "likes", "versions"} {
		if included[key] == nil {
			t.Fatalf("footer comment read is missing %s: %v", key, included)
		}
	}
	if included["version"] != nil {
		t.Fatalf("the version was not left out: %v", included)
	}
	if listed, _ := v2("GET", "/pages/"+page+"/footer-comments?status=trashed", nil, 200)["results"].([]any); len(listed) != 0 {
		t.Fatalf("trashed comments: %v", listed)
	}
	if listed, _ := v2("GET", "/pages/"+page+"/footer-comments?status=current", nil, 200)["results"].([]any); len(listed) != 1 {
		t.Fatalf("current comments: %v", listed)
	}
	v2("GET", "/pages/"+page+"/footer-comments?status=bogus", nil, 400)

	// An inline comment follows its passage through edits.
	inline := idOf(v2("POST", "/inline-comments", map[string]any{"pageId": page, "body": storage("<p>Check this</p>"),
		"inlineCommentProperties": map[string]any{"textSelection": "beta", "textSelectionMatchCount": 2, "textSelectionMatchIndex": 1}}, 201))
	anchor := func() (string, int, int) {
		t.Helper()
		var status string
		var count, index int
		if err := st.Pool.QueryRow(ctx, `SELECT resolution_status,inline_match_count,inline_match_index FROM wiki_footer_comments WHERE id::text=$1`, inline).Scan(&status, &count, &index); err != nil {
			t.Fatal(err)
		}
		return status, count, index
	}
	edit := func(version int, body string) {
		t.Helper()
		v2("PUT", "/pages/"+page, map[string]any{"id": page, "status": "current", "title": "Release notes", "body": storage(body), "version": map[string]any{"number": version}}, 200)
	}
	edit(2, "<p>Alpha gamma beta</p>")
	if status, count, index := anchor(); status != "open" || count != 1 || index != 0 {
		t.Fatalf("after one occurrence was removed: %s %d %d", status, count, index)
	}
	edit(3, "<p>Alpha gamma</p>")
	if status, _, _ := anchor(); status != "dangling" {
		t.Fatalf("after the passage was removed: %s", status)
	}
	if listed, _ := v2("GET", "/pages/"+page+"/inline-comments?resolution-status=dangling", nil, 200)["results"].([]any); len(listed) != 1 || dig(listed[0], "id") != inline {
		t.Fatalf("dangling inline comments: %v", listed)
	}
	edit(4, "<p>Alpha beta</p>")
	if status, count, _ := anchor(); status != "open" || count != 1 {
		t.Fatalf("after the passage came back: %s %d", status, count)
	}

	// Custom content takes footer comments.
	widget, err := st.CreateWikiCustomContent(ctx, ws, actor, models.WikiContent{CustomType: customType, SpaceID: space, Title: "Widget", Body: "<p>Widget</p>"})
	if err != nil {
		t.Fatal(err)
	}
	onWidget := v2("POST", "/footer-comments", map[string]any{"customContentId": widget.ID, "body": storage("<p>On the widget</p>")}, 201)
	if onWidget["customContentId"] != widget.ID {
		t.Fatalf("custom content comment: %v", onWidget)
	}
	reply := v2("POST", "/footer-comments", map[string]any{"parentCommentId": idOf(onWidget), "body": storage("<p>Reply</p>")}, 201)
	if reply["customContentId"] != widget.ID || reply["parentCommentId"] != idOf(onWidget) {
		t.Fatalf("reply on custom content: %v", reply)
	}
	if listed, _ := v2("GET", "/custom-content/"+widget.ID+"/footer-comments?body-format=storage", nil, 200)["results"].([]any); len(listed) != 1 || dig(listed[0], "body", "storage", "value") != "<p>On the widget</p>" {
		t.Fatalf("custom content comments: %v", listed)
	}
	v2("GET", "/footer-comments/"+idOf(onWidget), nil, 200)
	v2("POST", "/footer-comments", map[string]any{"customContentId": widget.ID, "pageId": page, "body": storage("<p>Two targets</p>")}, 400)

	// A star is a favourite relation, which CQL's favourite field finds.
	if err := st.SetWikiPageFavourite(ctx, ws, actor, page, true); err != nil {
		t.Fatal(err)
	}
	send(v1, "/wiki/rest/api", "GET", "/relation/favourite/from/user/current/to/content/"+page, nil, 200)
	found := send(v1, "/wiki/rest/api", "GET", "/content/search?cql="+url.QueryEscape("favourite = currentUser()"), nil, 200)
	if results, _ := found["results"].([]any); len(results) != 1 || dig(results[0], "id") != page {
		t.Fatalf("favourites by CQL: %v", found)
	}
	if favourite, err := st.IsWikiPageFavourite(ctx, ws, actor, page); err != nil || !favourite {
		t.Fatalf("star read back: %v %v", favourite, err)
	}
	if err := st.SetWikiPageFavourite(ctx, ws, actor, page, false); err != nil {
		t.Fatal(err)
	}
	send(v1, "/wiki/rest/api", "GET", "/relation/favourite/from/user/current/to/content/"+page, nil, 404)
}
