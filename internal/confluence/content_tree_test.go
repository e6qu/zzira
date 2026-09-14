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

// TestContentTree pins Confluence's single content tree: pages beneath
// folders and databases, siblings of every kind in one stored order, the
// children, descendants and ancestors reads across kinds, and moving,
// archiving, restoring and renaming nodes that are not pages.
func TestContentTree(t *testing.T) {
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
	exec(`INSERT INTO workspaces(id,slug,name) VALUES ($1,$1,'Content tree test')`, ws)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES ($1,$2,'test','Tree user')`, actor, actor+"@example.test")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES ($1,$2,'admin')`, ws, actor)
	exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES ($1,$1,$2)`, actor, store.HashToken(actor))
	t.Cleanup(func() {
		for _, sql := range []string{
			`DELETE FROM api_tasks WHERE workspace_id=$1`,
			`UPDATE wiki_pages SET parent_content_id=NULL WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`,
			`DELETE FROM wiki_content WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`,
			`DELETE FROM wiki_page_versions WHERE page_id IN (SELECT p.id FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id WHERE s.workspace_id=$1)`,
			`UPDATE wiki_pages SET parent_id=NULL WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`,
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
	// entries reads a result list as "title:type" in the order it came back.
	entries := func(path string) []string {
		t.Helper()
		results, _ := callV2("GET", path, nil, 200)["results"].([]any)
		out := []string{}
		for _, raw := range results {
			entry := raw.(map[string]any)
			title, _ := entry["title"].(string)
			if title == "" {
				title, _ = entry["id"].(string)
			}
			out = append(out, title+":"+entry["type"].(string))
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
	spaceA := idOf(callV2("POST", "/spaces", map[string]any{"key": "CTA", "name": "Tree A"}, 201))
	spaceB := idOf(callV2("POST", "/spaces", map[string]any{"key": "CTB", "name": "Tree B"}, 201))
	newPage := func(spaceID, title, parentID string, want int) map[string]any {
		t.Helper()
		payload := map[string]any{"spaceId": spaceID, "title": title, "status": "current",
			"body": models.WikiBody{Representation: "storage", Value: "<p>" + title + "</p>"}}
		if parentID != "" {
			payload["parentId"] = parentID
		}
		return callV2("POST", "/pages", payload, want)
	}
	guide := idOf(newPage(spaceA, "Guide", "", 200))
	runbooks := idOf(callV2("POST", "/folders", map[string]any{"spaceId": spaceA, "title": "Runbooks", "parentId": guide}, 200))
	rollbackPage := newPage(spaceA, "Rollback", runbooks, 200)
	rollback := idOf(rollbackPage)
	if rollbackPage["parentId"] != runbooks || rollbackPage["parentType"] != "folder" {
		t.Fatalf("a page beneath a folder: %v", rollbackPage)
	}
	catalog := idOf(callV2("POST", "/databases", map[string]any{"spaceId": spaceA, "title": "Catalog", "parentId": runbooks}, 200))

	// Children of every kind come back together, in their shared order.
	if got := strings.Join(entries("/folders/"+runbooks+"/direct-children"), ","); got != "Rollback:page,Catalog:database" {
		t.Fatalf("folder children: %s", got)
	}
	if got := strings.Join(entries("/pages/"+guide+"/direct-children"), ","); got != "Runbooks:folder" {
		t.Fatalf("page children: %s", got)
	}
	if got := entries("/pages/" + guide + "/children"); len(got) != 0 {
		t.Fatalf("the child-page read listed content that is not a page: %v", got)
	}
	if got := strings.Join(entries("/pages/"+guide+"/descendants?depth=2"), ","); got != "Runbooks:folder,Rollback:page,Catalog:database" {
		t.Fatalf("page descendants: %s", got)
	}
	if got := strings.Join(entries("/pages/"+rollback+"/ancestors"), ","); got != guide+":page,"+runbooks+":folder" {
		t.Fatalf("ancestors of a page beneath a folder: %s", got)
	}
	// With a limit, the nearest ancestors come back, so a caller can continue
	// from the highest one received.
	if got := strings.Join(entries("/pages/"+rollback+"/ancestors?limit=1"), ","); got != runbooks+":folder" {
		t.Fatalf("limited ancestors: %s", got)
	}
	if got := strings.Join(entries("/databases/"+catalog+"/ancestors"), ","); got != guide+":page,"+runbooks+":folder" {
		t.Fatalf("database ancestors: %s", got)
	}
	v1Pages := callV1("GET", "/content/"+guide+"/descendant/page", nil, 200)
	if results, _ := v1Pages["results"].([]any); len(results) != 1 || results[0].(map[string]any)["title"] != "Rollback" {
		t.Fatalf("v1 page descendants beneath a folder: %v", v1Pages)
	}

	// A page moves among siblings of other kinds.
	callV1("PUT", "/content/"+rollback+"/move/after/"+catalog, nil, 200)
	if got := strings.Join(entries("/folders/"+runbooks+"/direct-children"), ","); got != "Catalog:database,Rollback:page" {
		t.Fatalf("after moving the page after the database: %s", got)
	}
	// A page cannot be filed beneath private content, and content that still
	// holds a page cannot be deleted.
	private := idOf(callV2("POST", "/databases?private=true", map[string]any{"spaceId": spaceA, "title": "Private catalog"}, 200))
	newPage(spaceA, "Hidden", private, 400)
	callV1("PUT", "/content/"+rollback+"/move/append/"+private, nil, 400)
	callV2("DELETE", "/folders/"+runbooks, nil, 400)

	// Moving the folder to the top level of another space takes what is
	// inside it along, and what was beneath a page is no longer.
	if _, err := service.MoveWikiTreeNodeToSpace(ctx, ws, actor, runbooks, spaceB); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/folders/" + runbooks, "/pages/" + rollback, "/databases/" + catalog} {
		if got := callV2("GET", path, nil, 200); got["spaceId"] != spaceB {
			t.Fatalf("%s stayed behind: %v", path, got)
		}
	}
	if got := callV2("GET", "/folders/"+runbooks, nil, 200); got["parentId"] != "" {
		t.Fatalf("the folder is not at the top level: %v", got)
	}
	var root *string
	if err := st.Pool.QueryRow(ctx, `SELECT root_page_id::text FROM wiki_content WHERE id::text=$1`, catalog).Scan(&root); err != nil || root != nil {
		t.Fatalf("the database still answers to a page it is no longer beneath: %v %v", root, err)
	}
	var payload []byte
	if err := st.Pool.QueryRow(ctx, `SELECT payload FROM actions WHERE workspace_id=$1 AND entity_type='wiki_content' AND entity_id=$2 ORDER BY seq DESC LIMIT 1`, ws, catalog).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	var action map[string]any
	if err := json.Unmarshal(payload, &action); err != nil {
		t.Fatal(err)
	}
	if moved, _ := action["wiki_content"].(map[string]any); action["wikiSpaceId"] != spaceB || moved["spaceId"] != spaceB {
		t.Fatalf("replicas were not told where the database went: %s", payload)
	}
	if got := entries("/pages/" + guide + "/direct-children"); len(got) != 0 {
		t.Fatalf("the page still lists the moved folder: %v", got)
	}

	// Archiving the folder on its own lifts what is inside it to its level;
	// an archived folder still reads back.
	if archived, err := service.ArchiveWikiTreeNode(ctx, ws, actor, runbooks, false); err != nil || archived != 1 {
		t.Fatalf("archive folder: %d %v", archived, err)
	}
	if got := callV2("GET", "/folders/"+runbooks, nil, 200); got["status"] != "archived" {
		t.Fatalf("archived folder: %v", got)
	}
	if got := callV2("GET", "/pages/"+rollback, nil, 200); got["parentId"] != nil {
		t.Fatalf("the page was not lifted out of the archived folder: %v", got)
	}
	if _, err := service.RestoreWikiTreeNode(ctx, ws, actor, runbooks, false); err != nil {
		t.Fatal(err)
	}
	// With its contents, the whole subtree is archived and restored together.
	if _, err := service.MoveWikiTreeNode(ctx, ws, actor, rollback, "append", runbooks); err != nil {
		t.Fatal(err)
	}
	if archived, err := service.ArchiveWikiTreeNode(ctx, ws, actor, runbooks, true); err != nil || archived != 2 {
		t.Fatalf("archive folder with its contents: %d %v", archived, err)
	}
	if got := callV2("GET", "/pages/"+rollback+"?status=archived", nil, 200); got["status"] != "archived" {
		t.Fatalf("the page inside was not archived: %v", got)
	}
	if restored, err := service.RestoreWikiTreeNode(ctx, ws, actor, runbooks, true); err != nil || restored != 2 {
		t.Fatalf("restore folder with its contents: %d %v", restored, err)
	}
	if got := strings.Join(entries("/folders/"+runbooks+"/direct-children"), ","); got != "Rollback:page" {
		t.Fatalf("folder children after restore: %s", got)
	}

	// Content other than pages is renamed as a new version.
	renamed, err := service.RenameWikiContent(ctx, ws, actor, runbooks, "Runbooks archive")
	if err != nil || renamed.Title != "Runbooks archive" || renamed.Version.Number != 2 {
		t.Fatalf("rename: %v %v", renamed, err)
	}
	if _, err := service.RenameWikiContent(ctx, ws, actor, rollback, "Renamed page"); !errors.Is(err, store.ErrWikiValidation) {
		t.Fatalf("renaming a page through the content rename: %v", err)
	}
	if _, err := service.RenameWikiContent(ctx, ws, actor, catalog, " "); !errors.Is(err, store.ErrWikiValidation) {
		t.Fatalf("an empty name: %v", err)
	}
}
