package confluence

import (
	"context"
	"encoding/json"
	"errors"
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

// TestPageLifecycle pins what happens to a page between being current and
// being gone: archived pages stay listed by default and read back on request,
// archiving a parent lifts its children or takes them along, restoring brings
// them back where they were, and a page moved to another space carries its
// subtree, its folders and its replicas' view of it along.
func TestPageLifecycle(t *testing.T) {
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
	exec(`INSERT INTO workspaces(id,slug,name) VALUES ($1,$1,'Page lifecycle test')`, ws)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES ($1,$2,'test','Lifecycle user')`, actor, actor+"@example.test")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES ($1,$2,'admin')`, ws, actor)
	exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES ($1,$1,$2)`, actor, store.HashToken(actor))
	t.Cleanup(func() {
		for _, sql := range []string{
			`DELETE FROM api_tasks WHERE workspace_id=$1`,
			`DELETE FROM wiki_content_versions WHERE content_id IN (SELECT c.id FROM wiki_content c JOIN wiki_spaces s ON s.id=c.space_id WHERE s.workspace_id=$1)`,
			`DELETE FROM wiki_content WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`,
			`UPDATE wiki_spaces SET homepage_id=NULL WHERE workspace_id=$1`,
			`DELETE FROM wiki_page_versions WHERE page_id IN (SELECT p.id FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id WHERE s.workspace_id=$1)`,
			`DELETE FROM wiki_pages WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`,
			`DELETE FROM wiki_spaces WHERE workspace_id=$1`,
			`DELETE FROM actions WHERE workspace_id=$1`,
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
	service := &commands.Service{Store: st, Blobs: blobs}
	h := &Handler{Store: st, Commands: service, Blobs: blobs, WorkspaceSlug: ws, BaseURL: "https://zzira.test"}
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
		if response.Body.Len() > 0 {
			if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
				t.Fatalf("decode %s: %v", path, err)
			}
		}
		return out
	}
	callV1 := func(method, path string, body any, want int) map[string]any {
		t.Helper()
		return send(v1, "/wiki/rest/api", method, path, body, want)
	}
	callV2 := func(method, path string, body any, want int) map[string]any {
		t.Helper()
		return send(h, "/wiki/api/v2", method, path, body, want)
	}
	listed := func(path string) map[string]string {
		t.Helper()
		out := map[string]string{}
		results, _ := callV2("GET", path, nil, 200)["results"].([]any)
		for _, raw := range results {
			entry := raw.(map[string]any)
			out[entry["title"].(string)] = entry["status"].(string)
		}
		return out
	}
	spaceA, _ := callV2("POST", "/spaces", map[string]any{"key": "LCA", "name": "Lifecycle A"}, 201)["id"].(string)
	spaceB, _ := callV2("POST", "/spaces", map[string]any{"key": "LCB", "name": "Lifecycle B"}, 201)["id"].(string)
	newPage := func(spaceID, title, parentID string) string {
		t.Helper()
		payload := map[string]any{"spaceId": spaceID, "title": title, "status": "current",
			"body": models.WikiBody{Representation: "storage", Value: "<p>" + title + "</p>"}}
		if parentID != "" {
			payload["parentId"] = parentID
		}
		id, _ := callV2("POST", "/pages", payload, 200)["id"].(string)
		if id == "" {
			t.Fatalf("page %q was not created", title)
		}
		return id
	}
	page := func(id string, query string) map[string]any {
		t.Helper()
		return callV2("GET", "/pages/"+id+query, nil, 200)
	}
	home := newPage(spaceA, "Home", "")
	exec(`UPDATE wiki_spaces SET homepage_id=$2::bigint WHERE id::text=$1`, spaceA, home)
	parent := newPage(spaceA, "Parent", home)
	child := newPage(spaceA, "Child", parent)
	grandchild := newPage(spaceA, "Grandchild", child)
	sibling := newPage(spaceA, "Sibling", home)

	// Archived pages are listed with current ones unless a status is asked for.
	if _, err := service.ArchiveWikiPages(ctx, ws, actor, []string{sibling}, false); err != nil {
		t.Fatal(err)
	}
	if got := listed("/spaces/" + spaceA + "/pages"); got["Sibling"] != "archived" || got["Parent"] != "current" {
		t.Fatalf("default space listing: %v", got)
	}
	if got := listed("/pages?space-id=" + spaceA); got["Sibling"] != "archived" {
		t.Fatalf("default page listing: %v", got)
	}
	if got := listed("/spaces/" + spaceA + "/pages?status=current"); got["Sibling"] != "" {
		t.Fatalf("current listing kept the archived page: %v", got)
	}
	if got := listed("/spaces/" + spaceA + "/pages?status=archived"); len(got) != 1 || got["Sibling"] != "archived" {
		t.Fatalf("archived listing: %v", got)
	}
	if got := listed("/spaces/" + spaceA + "/pages?status=deleted"); len(got) != 0 {
		t.Fatalf("deleted listing: %v", got)
	}
	callV2("GET", "/spaces/"+spaceA+"/pages?status=historical", nil, 400)
	callV2("GET", "/pages/"+sibling, nil, 404)
	if got := page(sibling, "?status=archived"); got["status"] != "archived" {
		t.Fatalf("archived page read: %v", got)
	}
	callV2("GET", "/pages/"+sibling+"?status=sideways", nil, 400)
	if got := callV2("GET", "/pages/"+sibling+"/attachments?status=archived,trashed", nil, 200); got["results"] == nil {
		t.Fatalf("attachment statuses: %v", got)
	}
	callV2("GET", "/pages/"+sibling+"/attachments?status=draft", nil, 400)
	// An archived page is read-only until it is restored.
	archivedPage, err := st.WikiPage(ctx, ws, actor, sibling)
	if err != nil {
		t.Fatal(err)
	}
	archivedPage.Version.Number++
	archivedPage.Body.Value = "<p>Edited while archived</p>"
	if _, err := service.SaveWikiPage(ctx, ws, actor, *archivedPage); !errors.Is(err, store.ErrWikiValidation) {
		t.Fatalf("editing an archived page: %v", err)
	}
	// Restoring is refused while the space shows another current page with
	// the same title, and succeeds once it does not.
	replacement := newPage(spaceA, "Sibling", home)
	if _, err := service.RestoreWikiPage(ctx, ws, actor, sibling, false); !errors.Is(err, store.ErrWikiMoveValidation) {
		t.Fatalf("restoring over a current title: %v", err)
	}
	callV2("DELETE", "/pages/"+replacement, nil, 204)
	if restored, err := service.RestoreWikiPage(ctx, ws, actor, sibling, false); err != nil || restored != 1 {
		t.Fatalf("restore: %d %v", restored, err)
	}
	if got := page(sibling, ""); got["status"] != "current" || got["parentId"] != home {
		t.Fatalf("restored page: %v", got)
	}

	// Archiving a parent on its own lifts its children a level.
	if _, err := service.ArchiveWikiPages(ctx, ws, actor, []string{parent}, false); err != nil {
		t.Fatal(err)
	}
	if got := page(child, ""); got["parentId"] != home {
		t.Fatalf("the child did not move up a level: %v", got)
	}
	if _, err := service.RestoreWikiPage(ctx, ws, actor, parent, false); err != nil {
		t.Fatal(err)
	}
	// Archiving with the children takes the whole subtree, and restoring with
	// them brings it back where it was.
	if archived, err := service.ArchiveWikiPages(ctx, ws, actor, []string{child}, true); err != nil || archived != 2 {
		t.Fatalf("archive with children: %d %v", archived, err)
	}
	if got := page(grandchild, "?status=archived"); got["status"] != "archived" {
		t.Fatalf("grandchild was not archived: %v", got)
	}
	if _, err := service.ArchiveWikiPages(ctx, ws, actor, []string{child}, false); err == nil {
		t.Fatal("archiving an archived page succeeded")
	}
	if restored, err := service.RestoreWikiPage(ctx, ws, actor, child, true); err != nil || restored != 2 {
		t.Fatalf("restore with children: %d %v", restored, err)
	}
	if got := page(child, ""); got["parentId"] != home {
		t.Fatalf("child after restore: %v", got)
	}
	if got := page(grandchild, ""); got["parentId"] != child || got["status"] != "current" {
		t.Fatalf("grandchild after restore: %v", got)
	}
	// A restored page whose parent is no longer current lands at the top of
	// the space.
	orphanParent := newPage(spaceA, "Orphan parent", home)
	orphan := newPage(spaceA, "Orphan", orphanParent)
	if _, err := service.ArchiveWikiPages(ctx, ws, actor, []string{orphan, orphanParent}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RestoreWikiPage(ctx, ws, actor, orphan, false); err != nil {
		t.Fatal(err)
	}
	if got := page(orphan, ""); got["parentId"] != nil {
		t.Fatalf("a page restored under an archived parent: %v", got)
	}

	// An earlier version is history.
	current, err := st.WikiPage(ctx, ws, actor, child)
	if err != nil {
		t.Fatal(err)
	}
	current.Version.Number++
	current.Body.Value = "<p>Child, revised</p>"
	if _, err := service.SaveWikiPage(ctx, ws, actor, *current); err != nil {
		t.Fatal(err)
	}
	if got := page(child, "?version=1&status=historical"); got["status"] != "historical" {
		t.Fatalf("historical version: %v", got)
	}
	callV2("GET", "/pages/"+child+"?version=1&status=current", nil, 404)

	// Moving to another space takes the subtree and its folders along.
	folder, _ := callV2("POST", "/folders", map[string]any{"spaceId": spaceA, "title": "Child files", "parentId": child}, 200)["id"].(string)
	landing := newPage(spaceB, "Landing", "")
	callV1("PUT", "/content/"+child+"/move/append/"+landing, nil, 200)
	for _, id := range []string{child, grandchild} {
		if got := page(id, ""); got["spaceId"] != spaceB {
			t.Fatalf("page %s stayed behind: %v", id, got)
		}
	}
	if got := page(child, ""); got["parentId"] != landing {
		t.Fatalf("moved page parent: %v", got)
	}
	if got := callV2("GET", "/folders/"+folder, nil, 200); got["spaceId"] != spaceB {
		t.Fatalf("folder stayed behind: %v", got)
	}
	if got := listed("/spaces/" + spaceA + "/pages"); got["Child"] != "" || got["Grandchild"] != "" {
		t.Fatalf("the source space still lists the moved pages: %v", got)
	}
	// Replicas receive the moved pages with the space they are now in.
	var payload []byte
	if err := st.Pool.QueryRow(ctx, `SELECT payload FROM actions WHERE workspace_id=$1 AND entity_type='wiki_page' AND entity_id=$2 ORDER BY seq DESC LIMIT 1`, ws, grandchild).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	var action map[string]any
	if err := json.Unmarshal(payload, &action); err != nil {
		t.Fatal(err)
	}
	if snapshot, _ := action["wiki_page"].(map[string]any); action["wikiSpaceId"] != spaceB || snapshot["spaceId"] != spaceB || snapshot["published"] != true {
		t.Fatalf("grandchild move action: %s", payload)
	}
	// A title the destination already shows, and a space homepage, stay put.
	newPage(spaceB, "Sibling", "")
	callV1("PUT", "/content/"+sibling+"/move/append/"+landing, nil, 400)
	callV1("PUT", "/content/"+home+"/move/append/"+landing, nil, 400)
	// Moving back within the new space still orders siblings.
	second := newPage(spaceB, "Second", landing)
	callV1("PUT", "/content/"+second+"/move/before/"+child, nil, 200)
	results, _ := callV2("GET", "/pages/"+landing+"/direct-children", nil, 200)["results"].([]any)
	if len(results) != 2 || results[0].(map[string]any)["title"] != "Second" {
		t.Fatalf("children after reordering in the new space: %v", results)
	}
}
