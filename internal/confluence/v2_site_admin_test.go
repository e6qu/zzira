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

// TestV2SiteAdministration pins the admin key, content id conversion and the
// email access checks.
func TestV2SiteAdministration(t *testing.T) {
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
	ws, admin, member := store.NewID("ws"), store.NewID("usr"), store.NewID("usr")
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES ($1,$1,'Site administration test')`, ws)
	for _, person := range []struct{ id, role string }{{admin, "admin"}, {member, "member"}} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES ($1,$2,'test','Site user')`, person.id, person.id+"@example.test")
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES ($1,$2,$3)`, ws, person.id, person.role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES ($1,$1,$2)`, person.id, store.HashToken(person.id))
	}
	invited := strings.ToLower(store.NewID("inv")) + "@example.test"
	t.Cleanup(func() {
		for _, sql := range []string{
			`DELETE FROM wiki_admin_keys WHERE workspace_id=$1`,
			`DELETE FROM wiki_footer_comment_versions WHERE comment_id IN (SELECT c.id FROM wiki_footer_comments c JOIN wiki_pages p ON p.id=c.page_id JOIN wiki_spaces s ON s.id=p.space_id WHERE s.workspace_id=$1)`,
			`DELETE FROM wiki_footer_comments WHERE page_id IN (SELECT p.id FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id WHERE s.workspace_id=$1)`,
			`DELETE FROM wiki_blog_post_versions WHERE blog_post_id IN (SELECT b.id FROM wiki_blog_posts b JOIN wiki_spaces s ON s.id=b.space_id WHERE s.workspace_id=$1)`,
			`DELETE FROM wiki_blog_posts WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`,
			`DELETE FROM wiki_page_restrictions WHERE page_id IN (SELECT p.id FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id WHERE s.workspace_id=$1)`,
			`DELETE FROM wiki_page_versions WHERE page_id IN (SELECT p.id FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id WHERE s.workspace_id=$1)`,
			`UPDATE wiki_spaces SET homepage_id=NULL WHERE workspace_id=$1`,
			`DELETE FROM wiki_pages WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`,
			`DELETE FROM wiki_spaces WHERE workspace_id=$1`,
			`DELETE FROM actions WHERE workspace_id=$1`,
			`DELETE FROM memberships WHERE workspace_id=$1`,
		} {
			exec(sql, ws)
		}
		exec(`DELETE FROM directory_users WHERE user_id IN (SELECT id FROM users WHERE email=$1)`, invited)
		exec(`DELETE FROM users WHERE email=$1`, invited)
		exec(`DELETE FROM workspaces WHERE id=$1`, ws)
		for _, id := range []string{admin, member} {
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
		var reader *strings.Reader
		if body == nil {
			reader = strings.NewReader("")
		} else {
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			reader = strings.NewReader(string(raw))
		}
		request := httptest.NewRequest(method, "/wiki/api/v2"+path, reader)
		request.SetBasicAuth(user+"@example.test", user)
		response := httptest.NewRecorder()
		http.Handler(h).ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, response.Code, want, response.Body.String())
		}
		return response
	}
	object := func(response *httptest.ResponseRecorder) map[string]any {
		t.Helper()
		var out map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v %s", err, response.Body.String())
		}
		return out
	}

	// ---- Admin key ----
	// A member who is not an administrator has no admin key to read, issue or
	// end, and Confluence answers that with a 404.
	call(member, "GET", "/admin-key", nil, 404)
	call(member, "POST", "/admin-key", map[string]any{}, 404)
	call(member, "DELETE", "/admin-key", nil, 404)
	// An administrator without a key has none to read.
	call(admin, "GET", "/admin-key", nil, 404)
	// An empty body issues the ten-minute default.
	issued := object(call(admin, "POST", "/admin-key", nil, 200))
	if issued["accountId"] != admin || issued["expirationTime"] == "" {
		t.Fatalf("issued key: %v", issued)
	}
	read := object(call(admin, "GET", "/admin-key", nil, 200))
	if read["expirationTime"] != issued["expirationTime"] {
		t.Fatalf("read key differs from the issued one: %v vs %v", read, issued)
	}
	// Issuing again replaces the key with a fresh expiry.
	longer := object(call(admin, "POST", "/admin-key", map[string]any{"durationInMinutes": 60}, 200))
	if longer["expirationTime"].(string) <= issued["expirationTime"].(string) {
		t.Fatalf("a longer key should expire later: %v vs %v", longer, issued)
	}
	// An hour is the most a key can last.
	call(admin, "POST", "/admin-key", map[string]any{"durationInMinutes": 61}, 400)
	call(admin, "POST", "/admin-key", map[string]any{"durationInMinutes": -1}, 400)
	call(admin, "DELETE", "/admin-key", nil, 204)
	call(admin, "GET", "/admin-key", nil, 404)
	// Ending a key that is not there leaves the administrator without one.
	call(admin, "DELETE", "/admin-key", nil, 204)

	// The key is what lets an administrator past a restriction.
	space := object(call(admin, "POST", "/spaces", map[string]any{"key": "SITEADM", "name": "Site administration"}, 201))
	page := object(call(member, "POST", "/pages", map[string]any{"spaceId": space["id"], "title": "Private notes",
		"status": "current", "body": models.WikiBody{Representation: "storage", Value: "<p>mine</p>"}}, 200))
	pageID := page["id"].(string)
	send := func(user, method, path string, body any, want int) {
		t.Helper()
		raw, _ := json.Marshal(body)
		request := httptest.NewRequest(method, "/wiki/rest/api"+path, strings.NewReader(string(raw)))
		request.SetBasicAuth(user+"@example.test", user)
		response := httptest.NewRecorder()
		(&V1Handler{Handler: h}).ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, response.Code, want, response.Body.String())
		}
	}
	send(member, "PUT", "/content/"+pageID+"/restriction",
		[]map[string]any{{"operation": "read", "restrictions": map[string]any{"user": []map[string]string{{"accountId": member}}}}}, 200)
	call(admin, "GET", "/pages/"+pageID, nil, 404)
	call(admin, "POST", "/admin-key", map[string]any{"durationInMinutes": 1}, 200)
	call(admin, "GET", "/pages/"+pageID, nil, 200)
	// An expired key opens nothing.
	exec(`UPDATE wiki_admin_keys SET expires_at=now() - interval '1 second' WHERE workspace_id=$1 AND user_id=$2`, ws, admin)
	call(admin, "GET", "/pages/"+pageID, nil, 404)
	call(admin, "GET", "/admin-key", nil, 404)

	// ---- Convert ids to types ----
	post := object(call(admin, "POST", "/blogposts", map[string]any{"spaceId": space["id"], "title": "News",
		"status": "current", "body": models.WikiBody{Representation: "storage", Value: "<p>news</p>"}}, 200))
	openPage := object(call(admin, "POST", "/pages", map[string]any{"spaceId": space["id"], "title": "Open page",
		"status": "current", "body": models.WikiBody{Representation: "storage", Value: "<p>production</p>"}}, 200))
	footer := object(call(admin, "POST", "/footer-comments", map[string]any{"pageId": openPage["id"],
		"body": map[string]any{"storage": models.WikiBody{Representation: "storage", Value: "<p>ok</p>"}}}, 201))
	inline := object(call(admin, "POST", "/inline-comments", map[string]any{"pageId": openPage["id"],
		"body":                    map[string]any{"storage": models.WikiBody{Representation: "storage", Value: "<p>why?</p>"}},
		"inlineCommentProperties": map[string]any{"textSelection": "production", "textSelectionMatchCount": 1, "textSelectionMatchIndex": 0}}, 201))
	folder := object(call(admin, "POST", "/folders", map[string]any{"spaceId": space["id"], "title": "Drawer"}, 200))

	// Ids may be strings or numbers; a duplicate is answered once.
	var numericPost json.Number = json.Number(post["id"].(string))
	// The administrator's key has expired, so the member's restricted page is
	// content the administrator may not view.
	converted := object(call(admin, "POST", "/content/convert-ids-to-types", map[string]any{"contentIds": []any{
		openPage["id"], numericPost, post["id"], footer["id"], inline["id"], folder["id"], pageID, "999999999999", "not-an-id",
	}}, 200))
	results := converted["results"].(map[string]any)
	expect := map[string]any{
		openPage["id"].(string): "page",
		post["id"].(string):     "blogpost",
		footer["id"].(string):   "footer-comment",
		inline["id"].(string):   "inline-comment",
		folder["id"].(string):   "folder",
		// The restricted page is the member's alone: null, not "page".
		pageID:         nil,
		"999999999999": nil,
		"not-an-id":    nil,
	}
	for id, want := range expect {
		got, present := results[id]
		if !present || got != want {
			t.Fatalf("convert %s: got %v (present %v) want %v; all %v", id, got, present, want, results)
		}
	}
	if len(results) != len(expect) {
		t.Fatalf("duplicates should collapse to one key: %v", results)
	}
	// Its own author, who may view it, is told what it is.
	own := object(call(member, "POST", "/content/convert-ids-to-types", map[string]any{"contentIds": []any{pageID}}, 200))
	if own["results"].(map[string]any)[pageID] != "page" {
		t.Fatalf("the author's own restricted page: %v", own)
	}
	call(member, "POST", "/content/convert-ids-to-types", map[string]any{}, 400)
	tooMany := make([]any, 101)
	for i := range tooMany {
		tooMany[i] = i + 1
	}
	call(member, "POST", "/content/convert-ids-to-types", map[string]any{"contentIds": tooMany}, 400)
	call(member, "POST", "/content/convert-ids-to-types", map[string]any{"contentIds": []any{true}}, 400)

	// ---- Access by email ----
	access := object(call(member, "POST", "/user/access/check-access-by-email", map[string]any{
		"emails": []string{member + "@example.test", invited, "not an email", invited},
	}, 200))
	without := access["emailsWithoutAccess"].([]any)
	invalid := access["invalidEmails"].([]any)
	if len(without) != 1 || without[0] != invited {
		t.Fatalf("emails without access: %v", access)
	}
	if len(invalid) != 1 || invalid[0] != "not an email" {
		t.Fatalf("invalid emails: %v", access)
	}
	call(member, "POST", "/user/access/check-access-by-email", map[string]any{"emails": []string{}}, 400)

	// Inviting gives the address access; invalid addresses and people who
	// already have access are left alone.
	call(member, "POST", "/user/access/invite-by-email", map[string]any{
		"emails": []string{invited, member + "@example.test", "not an email"},
	}, 200)
	after := object(call(member, "POST", "/user/access/check-access-by-email", map[string]any{"emails": []string{invited}}, 200))
	if got := after["emailsWithoutAccess"].([]any); len(got) != 0 {
		t.Fatalf("the invited address still has no access: %v", after)
	}
	// Inviting the same address again changes nothing and is not an error.
	call(member, "POST", "/user/access/invite-by-email", map[string]any{"emails": []string{invited}}, 200)
}
