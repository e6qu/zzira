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

// TestLivePresence pins presence on pages and blog posts: who else has the
// content open and whether they are editing, presence expiring when it stops
// being reported, leaving, and the version and comment count an open page
// watches for changes.
func TestLivePresence(t *testing.T) {
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
	ws, admin, member, outsider := store.NewID("ws"), store.NewID("usr"), store.NewID("usr"), store.NewID("usr")
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES ($1,$1,'Presence test')`, ws)
	for _, user := range []string{admin, member, outsider} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES ($1,$2,'test',$3)`, user, user+"@example.test", "Person "+user)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES ($1,$1,$2)`, user, store.HashToken(user))
	}
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES ($1,$2,'admin'),($1,$3,'member')`, ws, admin, member)
	t.Cleanup(func() {
		for _, sql := range []string{
			`DELETE FROM wiki_presence WHERE workspace_id=$1`,
			`DELETE FROM wiki_footer_comments WHERE page_id IN (SELECT p.id FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id WHERE s.workspace_id=$1)`,
			`DELETE FROM wiki_blog_post_versions WHERE blog_post_id IN (SELECT b.id FROM wiki_blog_posts b JOIN wiki_spaces s ON s.id=b.space_id WHERE s.workspace_id=$1)`,
			`DELETE FROM wiki_blog_posts WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`,
			`DELETE FROM wiki_page_versions WHERE page_id IN (SELECT p.id FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id WHERE s.workspace_id=$1)`,
			`DELETE FROM wiki_pages WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`,
			`DELETE FROM wiki_spaces WHERE workspace_id=$1`,
			`DELETE FROM actions WHERE workspace_id=$1`,
			`DELETE FROM memberships WHERE workspace_id=$1`,
			`DELETE FROM workspaces WHERE id=$1`,
		} {
			exec(sql, ws)
		}
		for _, user := range []string{admin, member, outsider} {
			exec(`DELETE FROM api_tokens WHERE user_id=$1`, user)
			exec(`DELETE FROM users WHERE id=$1`, user)
		}
	})
	blobs, err := attachments.NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{Store: st, Commands: &commands.Service{Store: st, Blobs: blobs}, Blobs: blobs, WorkspaceSlug: ws, BaseURL: "https://zzira.test"}
	call := func(user, method, path string, body any, want int) map[string]any {
		t.Helper()
		raw, _ := json.Marshal(body)
		request := httptest.NewRequest(method, "/wiki/api/v2"+path, strings.NewReader(string(raw)))
		request.SetBasicAuth(user+"@example.test", user)
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, response.Code, want, response.Body.String())
		}
		out := map[string]any{}
		_ = json.Unmarshal(response.Body.Bytes(), &out)
		return out
	}
	storage := map[string]any{"representation": "storage", "value": "<p>Plan</p>"}
	space := call(admin, "POST", "/spaces", map[string]any{"key": "LIVE", "name": "Live"}, 201)["id"].(string)
	page := call(admin, "POST", "/pages", map[string]any{"spaceId": space, "title": "Plan", "status": "current", "body": storage}, 200)["id"].(string)
	post := call(admin, "POST", "/blogposts", map[string]any{"spaceId": space, "title": "Weekly", "status": "current", "body": storage}, 200)["id"].(string)

	// Someone editing is seen by someone reading, and not by themselves.
	editorState, err := st.WikiHeartbeat(ctx, ws, admin, "page", page, true)
	if err != nil || len(editorState.Present) != 0 || editorState.Version != 1 || editorState.CommentCount != 0 {
		t.Fatalf("editor's first report: %+v %v", editorState, err)
	}
	readerState, err := st.WikiHeartbeat(ctx, ws, member, "page", page, false)
	if err != nil || len(readerState.Present) != 1 || readerState.Present[0].AccountID != admin || !readerState.Present[0].Editing {
		t.Fatalf("reader sees the editor: %+v %v", readerState, err)
	}
	if again, _ := st.WikiHeartbeat(ctx, ws, admin, "page", page, true); len(again.Present) != 1 || again.Present[0].Editing {
		t.Fatalf("editor sees the reader: %+v", again)
	}

	// New versions and comments show in the state an open page watches.
	call(admin, "PUT", "/pages/"+page, map[string]any{"id": page, "spaceId": space, "title": "Plan", "status": "current", "body": storage, "version": map[string]any{"number": 2}}, 200)
	call(admin, "POST", "/footer-comments", map[string]any{"pageId": page, "body": storage}, 201)
	if changed, _ := st.WikiHeartbeat(ctx, ws, member, "page", page, false); changed.Version != 2 || changed.CommentCount != 1 || changed.LastCommentAt == "" {
		t.Fatalf("changes: %+v", changed)
	}

	// Presence that stops being reported expires, and leaving removes it.
	exec(`UPDATE wiki_presence SET seen_at=now()-interval '2 minutes' WHERE user_id=$1`, admin)
	if expired, _ := st.WikiHeartbeat(ctx, ws, member, "page", page, false); len(expired.Present) != 0 {
		t.Fatalf("stale presence: %+v", expired)
	}
	if _, err := st.WikiHeartbeat(ctx, ws, admin, "blogpost", post, false); err != nil {
		t.Fatal(err)
	}
	if onPost, _ := st.WikiHeartbeat(ctx, ws, member, "blogpost", post, true); len(onPost.Present) != 1 {
		t.Fatalf("blog post presence: %+v", onPost)
	}
	if err := st.LeaveWikiContent(ctx, ws, admin, "blogpost", post); err != nil {
		t.Fatal(err)
	}
	if left, _ := st.WikiHeartbeat(ctx, ws, member, "blogpost", post, true); len(left.Present) != 0 {
		t.Fatalf("after leaving: %+v", left)
	}

	// Presence is kept only for content the caller can see.
	if _, err := st.WikiHeartbeat(ctx, ws, outsider, "page", page, false); err == nil {
		t.Fatal("someone outside the site reported presence")
	}
}
