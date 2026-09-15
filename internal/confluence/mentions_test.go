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
	"github.com/e6qu/zzira/internal/store"
)

// TestMentionNotifications pins who hears about a mention: people newly
// mentioned in published pages, blog posts and comments they can see, and
// nobody else.
func TestMentionNotifications(t *testing.T) {
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
	ws, actor, member, outsider := store.NewID("ws"), store.NewID("usr"), store.NewID("usr"), store.NewID("usr")
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES ($1,$1,'Mentions test')`, ws)
	for _, user := range []string{actor, member, outsider} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES ($1,$2,'test',$1)`, user, user+"@example.test")
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES ($1,$1,$2)`, user, store.HashToken(user))
	}
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES ($1,$2,'admin'),($1,$3,'member')`, ws, actor, member)
	t.Cleanup(func() {
		for _, sql := range []string{
			`DELETE FROM wiki_footer_comments WHERE page_id IN (SELECT p.id FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id WHERE s.workspace_id=$1)`,
			`DELETE FROM wiki_page_restrictions WHERE page_id IN (SELECT p.id FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id WHERE s.workspace_id=$1)`,
			`DELETE FROM wiki_content_drafts WHERE workspace_id=$1`,
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
		for _, user := range []string{actor, member, outsider} {
			exec(`DELETE FROM api_tokens WHERE user_id=$1`, user)
			exec(`DELETE FROM users WHERE id=$1`, user)
		}
	})
	blobs, err := attachments.NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{Store: st, Commands: &commands.Service{Store: st, Blobs: blobs}, Blobs: blobs, WorkspaceSlug: ws, BaseURL: "https://zzira.test"}
	v2 := func(method, path string, body any, want int) map[string]any {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(method, "/wiki/api/v2"+path, strings.NewReader(string(raw)))
		request.SetBasicAuth(actor+"@example.test", actor)
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, response.Code, want, response.Body.String())
		}
		out := map[string]any{}
		if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		return out
	}
	idOf := func(body map[string]any) string {
		t.Helper()
		id, _ := body["id"].(string)
		if id == "" {
			t.Fatalf("no id in %v", body)
		}
		return id
	}
	mention := func(accounts ...string) map[string]any {
		value := "<p>Please review"
		for _, account := range accounts {
			value += ` <ac:link><ri:user ri:account-id="` + account + `" /></ac:link>`
		}
		return map[string]any{"representation": "storage", "value": value + "</p>"}
	}
	mentions := func(user string) []string {
		t.Helper()
		notifications, err := st.NotificationsByUser(ctx, ws, user, 50)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, n := range notifications {
			if n.Kind == "mentioned" {
				got = append(got, n.EntityType+":"+n.EntityID)
			}
		}
		return got
	}
	space := idOf(v2("POST", "/spaces", map[string]any{"key": "MEN", "name": "Mentions"}, 201))

	// A published page tells the people it newly mentions, never the writer
	// or someone outside the site.
	page := idOf(v2("POST", "/pages", map[string]any{"spaceId": space, "title": "Launch", "status": "current", "body": mention(member, actor, outsider)}, 200))
	if got := mentions(member); len(got) != 1 || got[0] != "wiki_page:"+page {
		t.Fatalf("member mentions after publishing: %v", got)
	}
	if got := mentions(actor); len(got) != 0 {
		t.Fatalf("self mention notified: %v", got)
	}
	if got := mentions(outsider); len(got) != 0 {
		t.Fatalf("outsider notified: %v", got)
	}
	// Keeping a mention in a later version is not a new mention.
	v2("PUT", "/pages/"+page, map[string]any{"id": page, "spaceId": space, "title": "Launch", "status": "current", "body": mention(member), "version": map[string]any{"number": 2}}, 200)
	if got := mentions(member); len(got) != 1 {
		t.Fatalf("unchanged mention notified again: %v", got)
	}

	// Drafts notify nobody until they are published.
	draft := idOf(v2("POST", "/pages", map[string]any{"spaceId": space, "title": "Plans", "status": "draft", "body": mention(member)}, 200))
	if got := mentions(member); len(got) != 1 {
		t.Fatalf("draft mention notified: %v", got)
	}
	v2("PUT", "/pages/"+draft, map[string]any{"id": draft, "spaceId": space, "title": "Plans", "status": "current", "body": mention(member), "version": map[string]any{"number": 2}}, 200)
	if got := mentions(member); len(got) != 2 || got[0] != "wiki_page:"+draft {
		t.Fatalf("publishing a draft did not notify: %v", got)
	}

	// Someone a page's read restriction excludes is not told about it.
	secret := idOf(v2("POST", "/pages", map[string]any{"spaceId": space, "title": "Secret", "status": "current", "body": mention()}, 200))
	exec(`INSERT INTO wiki_page_restrictions(page_id,operation,subject_type,subject_id,author_id) VALUES ($1::bigint,'read','user',$2,$2)`, secret, actor)
	v2("PUT", "/pages/"+secret, map[string]any{"id": secret, "spaceId": space, "title": "Secret", "status": "current", "body": mention(member), "version": map[string]any{"number": 2}}, 200)
	if got := mentions(member); len(got) != 2 {
		t.Fatalf("restricted page mention notified: %v", got)
	}

	// Blog posts and comments mention people too; a comment leads to what it
	// is on.
	post := idOf(v2("POST", "/blogposts", map[string]any{"spaceId": space, "title": "Weekly", "status": "current", "body": mention(member)}, 200))
	if got := mentions(member); len(got) != 3 || got[0] != "wiki_blogpost:"+post {
		t.Fatalf("blog post mention: %v", got)
	}
	comment := idOf(v2("POST", "/footer-comments", map[string]any{"pageId": page, "body": mention()}, 201))
	if got := mentions(member); len(got) != 3 {
		t.Fatalf("comment without mentions notified: %v", got)
	}
	v2("PUT", "/footer-comments/"+comment, map[string]any{"body": mention(member), "version": map[string]any{"number": 2}}, 200)
	if got := mentions(member); len(got) != 4 || got[0] != "wiki_page:"+page {
		t.Fatalf("comment mention: %v", got)
	}
}
