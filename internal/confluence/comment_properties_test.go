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

// TestCommentProperties pins content properties on comments: the same
// contract as page properties, on footer and inline comments alike.
func TestCommentProperties(t *testing.T) {
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
	ws, author, other := store.NewID("ws"), store.NewID("usr"), store.NewID("usr")
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES ($1,$1,'Comment property test')`, ws)
	// The other person is an ordinary member: they may read a comment but not
	// edit someone else's, which is what the property writes are gated on.
	for _, person := range []struct{ id, role string }{{author, "admin"}, {other, "member"}} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES ($1,$2,'test','Comment user')`, person.id, person.id+"@example.test")
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES ($1,$2,$3)`, ws, person.id, person.role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES ($1,$1,$2)`, person.id, store.HashToken(person.id))
	}
	t.Cleanup(func() {
		for _, sql := range []string{
			`DELETE FROM wiki_comment_properties WHERE comment_id IN (SELECT c.id FROM wiki_footer_comments c JOIN wiki_pages p ON p.id=c.page_id JOIN wiki_spaces s ON s.id=p.space_id WHERE s.workspace_id=$1)`,
			`DELETE FROM wiki_footer_comment_versions WHERE comment_id IN (SELECT c.id FROM wiki_footer_comments c JOIN wiki_pages p ON p.id=c.page_id JOIN wiki_spaces s ON s.id=p.space_id WHERE s.workspace_id=$1)`,
			`DELETE FROM wiki_footer_comments WHERE page_id IN (SELECT p.id FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id WHERE s.workspace_id=$1)`,
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
		for _, id := range []string{author, other} {
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
	keys := func(response *httptest.ResponseRecorder) []string {
		t.Helper()
		out := []string{}
		for _, raw := range object(response)["results"].([]any) {
			out = append(out, raw.(map[string]any)["key"].(string))
		}
		return out
	}

	space := object(call(author, "POST", "/spaces", map[string]any{"key": "CPROP", "name": "Comment properties"}, 201))
	page := object(call(author, "POST", "/pages", map[string]any{"spaceId": space["id"], "title": "Discussed page",
		"status": "current", "body": models.WikiBody{Representation: "storage", Value: "<p>production rollout</p>"}}, 200))
	footer := object(call(author, "POST", "/footer-comments", map[string]any{"pageId": page["id"],
		"body": map[string]any{"storage": models.WikiBody{Representation: "storage", Value: "<p>Looks good</p>"}}}, 201))
	inline := object(call(author, "POST", "/inline-comments", map[string]any{"pageId": page["id"],
		"body":                    map[string]any{"storage": models.WikiBody{Representation: "storage", Value: "<p>Why here?</p>"}},
		"inlineCommentProperties": map[string]any{"textSelection": "production", "textSelectionMatchCount": 1, "textSelectionMatchIndex": 0}}, 201))
	footerID, inlineID := footer["id"].(string), inline["id"].(string)

	// A comment starts with no properties.
	if got := keys(call(author, "GET", "/comments/"+footerID+"/properties", nil, 200)); len(got) != 0 {
		t.Fatalf("new comment properties: %v", got)
	}
	// Creating one returns it at version 1 with the value as given.
	created := object(call(author, "POST", "/comments/"+footerID+"/properties", map[string]any{"key": "review", "value": map[string]any{"state": "open"}}, 200))
	propertyID := created["id"].(string)
	if created["key"] != "review" || created["version"].(map[string]any)["number"] != float64(1) {
		t.Fatalf("created property: %v", created)
	}
	call(author, "POST", "/comments/"+footerID+"/properties", map[string]any{"key": "alpha", "value": true}, 200)
	// Both footer and inline comments carry properties, independently.
	call(author, "POST", "/comments/"+inlineID+"/properties", map[string]any{"key": "review", "value": "inline"}, 200)
	if got := keys(call(author, "GET", "/comments/"+footerID+"/properties", nil, 200)); len(got) != 2 || got[0] != "alpha" || got[1] != "review" {
		t.Fatalf("footer properties in key order: %v", got)
	}
	if got := keys(call(author, "GET", "/comments/"+footerID+"/properties?sort=-key", nil, 200)); got[0] != "review" {
		t.Fatalf("descending key order: %v", got)
	}
	if got := keys(call(author, "GET", "/comments/"+inlineID+"/properties", nil, 200)); len(got) != 1 {
		t.Fatalf("inline properties: %v", got)
	}
	if got := keys(call(author, "GET", "/comments/"+footerID+"/properties?key=review", nil, 200)); len(got) != 1 {
		t.Fatalf("filter by key: %v", got)
	}
	// A key names one property per comment.
	call(author, "POST", "/comments/"+footerID+"/properties", map[string]any{"key": "review", "value": 1}, 400)

	// Reading one returns what was stored.
	read := object(call(other, "GET", "/comments/"+footerID+"/properties/"+propertyID, nil, 200))
	if read["value"].(map[string]any)["state"] != "open" {
		t.Fatalf("read property: %v", read)
	}
	// An update needs the next version, guarding against a lost update.
	call(author, "PUT", "/comments/"+footerID+"/properties/"+propertyID, map[string]any{"key": "review", "value": map[string]any{"state": "closed"}, "version": map[string]any{"number": 3}}, 409)
	updated := object(call(author, "PUT", "/comments/"+footerID+"/properties/"+propertyID, map[string]any{"key": "review", "value": map[string]any{"state": "closed"}, "version": map[string]any{"number": 2, "message": "Resolved"}}, 200))
	if updated["version"].(map[string]any)["number"] != float64(2) || updated["value"].(map[string]any)["state"] != "closed" {
		t.Fatalf("updated property: %v", updated)
	}

	// Someone who may read the comment but not edit it may not change its
	// properties.
	call(other, "POST", "/comments/"+footerID+"/properties", map[string]any{"key": "sneaky", "value": 1}, 403)
	call(other, "DELETE", "/comments/"+footerID+"/properties/"+propertyID, nil, 403)

	// A property on another comment is not reachable through this one.
	call(author, "GET", "/comments/"+inlineID+"/properties/"+propertyID, nil, 404)
	// A comment that does not exist is a 404.
	call(author, "GET", "/comments/999999999999/properties", nil, 404)

	// Deleting removes it, and it is gone afterwards.
	call(author, "DELETE", "/comments/"+footerID+"/properties/"+propertyID, nil, 204)
	call(author, "GET", "/comments/"+footerID+"/properties/"+propertyID, nil, 404)
	if got := keys(call(author, "GET", "/comments/"+footerID+"/properties", nil, 200)); len(got) != 1 || got[0] != "alpha" {
		t.Fatalf("after delete: %v", got)
	}
}
