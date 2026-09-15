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

// TestOperationVocabulary pins the operations Confluence reports: what a space
// administrator may do with a page, blog post, whiteboard and space, and how
// much less a viewer may.
func TestOperationVocabulary(t *testing.T) {
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
	ws, admin, viewer := store.NewID("ws"), store.NewID("usr"), store.NewID("usr")
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES ($1,$1,'Operations test')`, ws)
	for _, user := range []string{admin, viewer} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES ($1,$2,'test',$1)`, user, user+"@example.test")
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES ($1,$1,$2)`, user, store.HashToken(user))
	}
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES ($1,$2,'admin'),($1,$3,'member')`, ws, admin, viewer)
	t.Cleanup(func() {
		for _, sql := range []string{
			`DELETE FROM wiki_blog_post_versions WHERE blog_post_id IN (SELECT b.id FROM wiki_blog_posts b JOIN wiki_spaces s ON s.id=b.space_id WHERE s.workspace_id=$1)`,
			`DELETE FROM wiki_blog_posts WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`,
			`DELETE FROM wiki_content WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`,
			`DELETE FROM wiki_page_versions WHERE page_id IN (SELECT p.id FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id WHERE s.workspace_id=$1)`,
			`DELETE FROM wiki_pages WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`,
			`DELETE FROM wiki_spaces WHERE workspace_id=$1`,
			`DELETE FROM actions WHERE workspace_id=$1`,
			`DELETE FROM memberships WHERE workspace_id=$1`,
			`DELETE FROM workspaces WHERE id=$1`,
		} {
			exec(sql, ws)
		}
		for _, user := range []string{admin, viewer} {
			exec(`DELETE FROM api_tokens WHERE user_id=$1`, user)
			exec(`DELETE FROM users WHERE id=$1`, user)
		}
	})
	blobs, err := attachments.NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{Store: st, Commands: &commands.Service{Store: st, Blobs: blobs}, Blobs: blobs, WorkspaceSlug: ws, BaseURL: "https://zzira.test"}
	call := func(user, method, path string, body any, want int) []byte {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(method, "/wiki/api/v2"+path, strings.NewReader(string(raw)))
		request.SetBasicAuth(user+"@example.test", user)
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, response.Code, want, response.Body.String())
		}
		return response.Body.Bytes()
	}
	idOf := func(body []byte) string {
		t.Helper()
		var bean struct{ ID string }
		if err := json.Unmarshal(body, &bean); err != nil || bean.ID == "" {
			t.Fatalf("no id in %s", body)
		}
		return bean.ID
	}
	operations := func(user, path string) map[string]bool {
		t.Helper()
		var response struct {
			Operations []struct{ Operation, TargetType string }
		}
		if err := json.Unmarshal(call(user, "GET", path, nil, 200), &response); err != nil {
			t.Fatal(err)
		}
		set := map[string]bool{}
		for _, op := range response.Operations {
			set[op.Operation+":"+op.TargetType] = true
		}
		return set
	}
	expect := func(label string, got map[string]bool, present, absent []string) {
		t.Helper()
		for _, op := range present {
			if !got[op] {
				t.Errorf("%s lacks %s: %v", label, op, got)
			}
		}
		for _, op := range absent {
			if got[op] {
				t.Errorf("%s has %s: %v", label, op, got)
			}
		}
	}
	storage := map[string]any{"representation": "storage", "value": "<p>Plan</p>"}
	space := idOf(call(admin, "POST", "/spaces", map[string]any{"key": "OPS", "name": "Operations", "roleAssignments": []map[string]any{
		{"roleId": "system-admin", "principal": map[string]string{"principalType": "USER", "principalId": admin}},
		{"roleId": "system-viewer", "principal": map[string]string{"principalType": "USER", "principalId": viewer}},
	}}, 201))
	page := idOf(call(admin, "POST", "/pages", map[string]any{"spaceId": space, "title": "Plan", "status": "current", "body": storage}, 200))
	post := idOf(call(admin, "POST", "/blogposts", map[string]any{"spaceId": space, "title": "News", "status": "current", "body": storage}, 200))
	board := idOf(call(admin, "POST", "/whiteboards", map[string]any{"spaceId": space, "title": "Board"}, 200))

	expect("administrator page", operations(admin, "/pages/"+page+"/operations"),
		[]string{"read:page", "export:page", "update:page", "archive:page", "delete:page", "copy:page", "move:page", "restrict_content:page", "purge:page", "purge_version:page", "create:comment", "create:attachment"}, nil)
	expect("viewer page", operations(viewer, "/pages/"+page+"/operations"),
		[]string{"read:page", "export:page"}, []string{"update:page", "delete:page", "copy:page", "move:page", "purge:page", "create:attachment", "restrict_content:page"})
	expect("administrator blog post", operations(admin, "/blogposts/"+post+"/operations"),
		[]string{"read:blogpost", "update:blogpost", "delete:blogpost", "copy:blogpost", "purge:blogpost", "create:comment"}, nil)
	expect("administrator whiteboard", operations(admin, "/whiteboards/"+board+"/operations"),
		[]string{"read:whiteboard", "export:whiteboard", "update:whiteboard", "copy:whiteboard", "move:whiteboard", "purge:whiteboard"}, nil)
	expect("viewer whiteboard", operations(viewer, "/whiteboards/"+board+"/operations"),
		[]string{"read:whiteboard"}, []string{"update:whiteboard", "copy:whiteboard", "purge:whiteboard"})
	expect("administrator space", operations(admin, "/spaces/"+space+"/operations"),
		[]string{"read:space", "export:space", "create:page", "create:blogpost", "create:whiteboard", "administer:space", "archive:space", "restrict_content:space"}, nil)
	expect("viewer space", operations(viewer, "/spaces/"+space+"/operations"),
		[]string{"read:space"}, []string{"create:page", "administer:space", "update:space"})
	if included := string(call(admin, "GET", "/pages/"+page+"?include-operations=true", nil, 200)); !strings.Contains(included, `"operation":"restrict_content"`) {
		t.Fatalf("included operations: %s", included)
	}
}
