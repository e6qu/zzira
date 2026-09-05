package confluence

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

func TestWikiAPIPrivacyAndVersionedLifecycle(t *testing.T) {
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
	exec(`INSERT INTO workspaces(id,slug,name) VALUES ($1,$1,'Wiki test')`, ws)
	for _, id := range []string{actor, member, outsider} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES ($1,$2,'test','Wiki User')`, id, id+"@example.test")
		role := "member"
		if id == actor {
			role = "admin"
		}
		if id != outsider {
			exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES ($1,$2,$3)`, ws, id, role)
		}
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES ($1,$1,$2)`, id, store.HashToken(id))
	}
	t.Cleanup(func() {
		for _, sql := range []string{`DELETE FROM wiki_page_versions WHERE page_id IN (SELECT p.id FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id WHERE s.workspace_id=$1)`, `DELETE FROM wiki_pages WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`, `DELETE FROM wiki_spaces WHERE workspace_id=$1`, `DELETE FROM actions WHERE workspace_id=$1`, `DELETE FROM memberships WHERE workspace_id=$1`, `DELETE FROM workspaces WHERE id=$1`} {
			exec(sql, ws)
		}
		for _, id := range []string{actor, member, outsider} {
			exec(`DELETE FROM api_tokens WHERE user_id=$1`, id)
			exec(`DELETE FROM users WHERE id=$1`, id)
		}
	})
	h := &Handler{Store: st, Commands: &commands.Service{Store: st}, WorkspaceSlug: ws, BaseURL: "https://zzira.test"}
	call := func(user, method, path string, body any, want int) *httptest.ResponseRecorder {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(method, "/wiki/api/v2"+path, strings.NewReader(string(raw)))
		if user != "" {
			r.SetBasicAuth(user+"@example.test", user)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, w.Code, want, w.Body.String())
		}
		return w
	}
	space := func(key string, private bool) string {
		t.Helper()
		w := call(actor, "POST", "/spaces", map[string]any{"key": key, "name": key, "createPrivateSpace": private}, 201)
		var s struct{ ID string }
		if err := json.Unmarshal(w.Body.Bytes(), &s); err != nil {
			t.Fatal(err)
		}
		return s.ID
	}
	public, private := space("PUBLIC", false), space("PRIVATE", true)
	call("", "GET", "/spaces", nil, 401)
	call(outsider, "GET", "/spaces", nil, 403)
	call(member, "POST", "/spaces", map[string]string{"key": "DENIED", "name": "Denied"}, 403)
	hidden := call(member, "GET", "/spaces", nil, 200)
	if strings.Contains(hidden.Body.String(), "PRIVATE") {
		t.Fatal("private space leaked")
	}
	call(member, "GET", "/spaces/"+private, nil, 404)
	create := func(space, title, status string) models.WikiPage {
		t.Helper()
		w := call(actor, "POST", "/pages", map[string]any{"spaceId": space, "title": title, "status": status, "body": models.WikiBody{Representation: "storage", Value: "<p><strong>Release</strong> notes</p>"}}, 200)
		var page models.WikiPage
		// Wire body is a representation map, unlike the materialized model.
		var bean struct {
			ID, SpaceID, Title, Status string
			Version                    models.WikiVersion
			Body                       map[string]models.WikiBody
		}
		if err := json.Unmarshal(w.Body.Bytes(), &bean); err != nil {
			t.Fatal(err)
		}
		page = models.WikiPage{ID: bean.ID, SpaceID: bean.SpaceID, Title: bean.Title, Status: bean.Status, Version: bean.Version, Body: bean.Body["storage"]}
		return page
	}
	page := create(public, "Release guide", "current")
	draft := create(public, "Private draft", "draft")
	secret := create(private, "Secret guide", "current")
	call(member, "GET", "/pages/"+draft.ID+"?status=draft", nil, 404)
	call(member, "GET", "/pages/"+secret.ID, nil, 404)
	call(member, "POST", "/pages", map[string]any{"spaceId": private, "title": "Intrusion", "body": models.WikiBody{Representation: "storage", Value: "<p>x</p>"}}, 404)
	call(actor, "POST", "/pages", map[string]any{"spaceId": public, "title": "Unsafe", "body": models.WikiBody{Representation: "storage", Value: "<script>alert(1)</script>"}}, 400)
	call(actor, "GET", "/pages?body-format=atlas_doc_format", nil, 400)
	update := map[string]any{"id": page.ID, "spaceId": public, "title": "Release guide updated", "status": "current", "body": models.WikiBody{Representation: "storage", Value: "<h2>Ready</h2>"}, "version": map[string]any{"number": 2, "message": "Updated release instructions"}}
	call(member, "PUT", "/pages/"+page.ID, update, 200)
	call(actor, "PUT", "/pages/"+page.ID, update, 409)
	versions := call(actor, "GET", "/pages/"+page.ID+"/versions", nil, 200)
	if !strings.Contains(versions.Body.String(), "Updated release instructions") {
		t.Fatal(versions.Body.String())
	}
	update["parentId"] = page.ID
	update["version"] = map[string]int{"number": 3}
	call(actor, "PUT", "/pages/"+page.ID, update, 400)
	delete(update, "parentId")
	call(actor, "DELETE", "/pages/"+page.ID, nil, 204)
	call(actor, "GET", "/pages/"+page.ID, nil, 404)
	call(actor, "GET", "/pages/"+page.ID+"?status=trashed&body-format=storage", nil, 200)
	update["version"] = map[string]int{"number": 4}
	call(actor, "PUT", "/pages/"+page.ID, update, 200)
	got := call(actor, "GET", "/pages/"+page.ID+"?body-format=storage", nil, 200)
	if !strings.Contains(got.Body.String(), "<h2>Ready</h2>") && !strings.Contains(got.Body.String(), `\u003ch2\u003eReady`) {
		t.Fatal(got.Body.String())
	}
	create(public, "Another page", "current")
	paged := call(actor, "GET", "/pages?limit=1", nil, 200)
	if paged.Header().Get("Link") == "" {
		t.Fatal("missing cursor Link header")
	}
	call(actor, "GET", "/pages?cursor=broken", nil, 400)
	call(actor, "DELETE", "/pages/"+draft.ID, nil, 204)
	call(member, "GET", "/pages/"+draft.ID+"?status=trashed", nil, 404)
	trash := call(member, "GET", "/pages?status=trashed", nil, 200)
	if strings.Contains(trash.Body.String(), "Private draft") {
		t.Fatal("trashed draft leaked")
	}
	actions, err := st.ActionsSince(ctx, ws, member, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range actions {
		if strings.Contains(string(a.Payload), "Private draft") || strings.Contains(string(a.Payload), "Secret guide") || strings.Contains(string(a.Payload), "PRIVATE") {
			t.Fatalf("private wiki action leaked: %s", a.Payload)
		}
	}
}
