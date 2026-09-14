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

// TestWatchesOnEveryContentKind pins watching blog posts and other space
// content, space administrators managing watches for others in their space,
// watchers hearing about updates and comments without duplicates, and those
// notifications reaching people by email.
func TestWatchesOnEveryContentKind(t *testing.T) {
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
	ws, actor, spaceAdmin, watcher := store.NewID("ws"), store.NewID("usr"), store.NewID("usr"), store.NewID("usr")
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES ($1,$1,'Watches test')`, ws)
	for _, user := range []string{actor, spaceAdmin, watcher} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES ($1,$2,'test',$1)`, user, user+"@example.test")
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES ($1,$1,$2)`, user, store.HashToken(user))
	}
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES ($1,$2,'admin'),($1,$3,'member'),($1,$4,'member')`, ws, actor, spaceAdmin, watcher)
	t.Cleanup(func() {
		for _, sql := range []string{
			`DELETE FROM wiki_watches WHERE workspace_id=$1`,
			`DELETE FROM wiki_footer_comments WHERE blog_post_id IN (SELECT b.id FROM wiki_blog_posts b JOIN wiki_spaces s ON s.id=b.space_id WHERE s.workspace_id=$1)`,
			`DELETE FROM wiki_blog_post_labels WHERE blog_post_id IN (SELECT b.id FROM wiki_blog_posts b JOIN wiki_spaces s ON s.id=b.space_id WHERE s.workspace_id=$1)`,
			`DELETE FROM wiki_blog_post_versions WHERE blog_post_id IN (SELECT b.id FROM wiki_blog_posts b JOIN wiki_spaces s ON s.id=b.space_id WHERE s.workspace_id=$1)`,
			`DELETE FROM wiki_blog_posts WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`,
			`DELETE FROM wiki_content WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`,
			`DELETE FROM wiki_spaces WHERE workspace_id=$1`,
			`DELETE FROM wiki_labels WHERE workspace_id=$1`,
			`DELETE FROM actions WHERE workspace_id=$1`,
			`DELETE FROM notifications WHERE workspace_id=$1`,
			`DELETE FROM email_outbox WHERE workspace_id=$1`,
			`DELETE FROM memberships WHERE workspace_id=$1`,
			`DELETE FROM workspaces WHERE id=$1`,
		} {
			exec(sql, ws)
		}
		for _, user := range []string{actor, spaceAdmin, watcher} {
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
	send := func(handler http.Handler, user, path, method string, body any, want int) map[string]any {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(method, path, strings.NewReader(string(raw)))
		request.SetBasicAuth(user+"@example.test", user)
		request.Header.Set("X-Atlassian-Token", "no-check")
		request.Header.Set("Content-Type", "application/json")
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
	v2 := func(user, method, path string, body any, want int) map[string]any {
		t.Helper()
		return send(h, user, "/wiki/api/v2"+path, method, body, want)
	}
	legacy := func(user, method, path string, body any, want int) map[string]any {
		t.Helper()
		return send(v1, user, "/wiki/rest/api"+path, method, body, want)
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
	watched := func(user string) []string {
		t.Helper()
		notifications, err := st.NotificationsByUser(ctx, ws, user, 50)
		if err != nil {
			t.Fatal(err)
		}
		got := []string{}
		for _, n := range notifications {
			got = append(got, n.Kind+":"+n.EntityType+":"+n.Message)
		}
		return got
	}

	space := idOf(v2(actor, "POST", "/spaces", map[string]any{"key": "WAT", "name": "Watches"}, 201))
	v2(actor, "POST", "/spaces/"+space+"/role-assignments", []map[string]any{
		{"roleId": "system-admin", "principal": map[string]string{"principalType": "USER", "principalId": spaceAdmin}},
		{"roleId": "system-member", "principal": map[string]string{"principalType": "ACCESS_CLASS", "principalId": "authenticated-users"}},
	}, 204)
	post := idOf(v2(actor, "POST", "/blogposts", map[string]any{"spaceId": space, "title": "Weekly", "status": "current", "body": storage("<p>One</p>")}, 200))
	legacy(actor, "POST", "/content/"+post+"/label", []map[string]string{{"prefix": "global", "name": "weekly"}}, 200)

	// A space administrator manages watches for others on the space's
	// content, but label watches span spaces and stay with site
	// administrators; other people manage only their own.
	legacy(spaceAdmin, "POST", "/user/watch/content/"+post+"?accountId="+watcher, nil, 204)
	if status := legacy(spaceAdmin, "GET", "/user/watch/content/"+post+"?accountId="+watcher, nil, 200); status["watching"] != true {
		t.Fatalf("space administrator's watch for someone else: %v", status)
	}
	legacy(spaceAdmin, "GET", "/user/watch/space/WAT?accountId="+watcher, nil, 200)
	legacy(spaceAdmin, "POST", "/user/watch/label/weekly?accountId="+watcher, nil, 403)
	legacy(watcher, "POST", "/user/watch/content/"+post+"?accountId="+spaceAdmin, nil, 403)
	if watchers := legacy(actor, "GET", "/content/"+post+"/notification/child-created", nil, 200); !strings.Contains(mustJSON(t, watchers), `"type":"blogpost"`) || !strings.Contains(mustJSON(t, watchers), watcher) {
		t.Fatalf("blog post watchers: %v", watchers)
	}

	// Updates notify watchers once; minor edits notify nobody.
	v2(actor, "PUT", "/blogposts/"+post, map[string]any{"id": post, "spaceId": space, "title": "Weekly", "status": "current", "body": storage("<p>Two</p>"), "version": map[string]any{"number": 2}}, 200)
	if got := watched(watcher); len(got) != 1 || got[0] != `watched:wiki_blogpost:Updated blog post "Weekly".` {
		t.Fatalf("after an update: %v", got)
	}
	v2(actor, "PUT", "/blogposts/"+post, map[string]any{"id": post, "spaceId": space, "title": "Weekly", "status": "current", "body": storage("<p>Three</p>"), "version": map[string]any{"number": 3, "minorEdit": true}}, 200)
	if got := watched(watcher); len(got) != 1 {
		t.Fatalf("minor edit notified: %v", got)
	}

	// Comments notify watchers of what they are on, and someone a comment
	// mentions hears about it once.
	mention := `<p>Over to <ac:link><ri:user ri:account-id="` + watcher + `" /></ac:link></p>`
	comment := idOf(v2(actor, "POST", "/footer-comments", map[string]any{"blogPostId": post, "body": storage(mention)}, 201))
	if got := watched(watcher); len(got) != 2 || got[0] != `mentioned:wiki_blogpost:Mentioned you in "Weekly".` {
		t.Fatalf("mention and watch on one comment: %v", got)
	}
	v2(actor, "POST", "/footer-comments", map[string]any{"blogPostId": post, "body": storage("<p>Plain</p>")}, 201)
	if got := watched(watcher); len(got) != 3 || got[0] != `watched:wiki_blogpost:Commented on "Weekly".` {
		t.Fatalf("comment watch: %v", got)
	}

	// Other space content can be watched; comments are watched through what
	// they are on.
	board := idOf(v2(actor, "POST", "/whiteboards", map[string]any{"spaceId": space, "title": "Planning"}, 200))
	legacy(watcher, "POST", "/user/watch/content/"+board, nil, 204)
	if watchers := legacy(actor, "GET", "/content/"+board+"/notification/child-created", nil, 200); !strings.Contains(mustJSON(t, watchers), `"type":"whiteboard"`) {
		t.Fatalf("whiteboard watchers: %v", watchers)
	}
	legacy(watcher, "POST", "/user/watch/content/"+comment, nil, 400)

	// Each notification is emailed once to the person it is for.
	runner := &store.WikiNotificationEmailRunner{Store: st, BaseURL: "https://zzira.test", WorkspaceID: ws}
	if queued, err := runner.QueueEmails(ctx); err != nil || queued != 3 {
		t.Fatalf("queued %d emails: %v", queued, err)
	}
	if queued, err := runner.QueueEmails(ctx); err != nil || queued != 0 {
		t.Fatalf("queued again %d: %v", queued, err)
	}
	rows, err := st.Pool.Query(ctx, `SELECT subject,body FROM email_outbox WHERE workspace_id=$1 AND recipient=$2 ORDER BY id`, ws, watcher+"@example.test")
	if err != nil {
		t.Fatal(err)
	}
	var emails []string
	for rows.Next() {
		var subject, body string
		if err := rows.Scan(&subject, &body); err != nil {
			t.Fatal(err)
		}
		emails = append(emails, subject+"|"+body)
	}
	rows.Close()
	if len(emails) != 3 || !strings.HasPrefix(emails[0], actor+` updated blog post "Weekly"|`) || !strings.Contains(emails[1], actor+` mentioned you in "Weekly"`) || !strings.Contains(emails[2], "https://zzira.test/wiki/blogposts/"+post) {
		t.Fatalf("emails: %q", emails)
	}
}

// mustJSON encodes a value for substring checks, leaving markup unescaped so
// storage bodies read as written.
func mustJSON(t *testing.T, value any) string {
	t.Helper()
	var out strings.Builder
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(out.String())
}
