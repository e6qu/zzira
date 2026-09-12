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

// TestPageMovesAndCopies pins Confluence's page relocation surface: moving a
// page among its siblings or under a new parent, copying one page or a whole
// hierarchy, archiving pages, trashing a tree, and the long tasks that report
// the ones that run in the background.
func TestPageMovesAndCopies(t *testing.T) {
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
	exec(`INSERT INTO workspaces(id,slug,name) VALUES ($1,$1,'Page move test')`, ws)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES ($1,$2,'test','Move user')`, actor, actor+"@example.test")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES ($1,$2,'admin')`, ws, actor)
	exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES ($1,$1,$2)`, actor, store.HashToken(actor))
	t.Cleanup(func() {
		for _, sql := range []string{
			`DELETE FROM api_tasks WHERE workspace_id=$1`,
			`DELETE FROM wiki_page_properties WHERE page_id IN (SELECT p.id FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id WHERE s.workspace_id=$1)`,
			`DELETE FROM wiki_page_labels WHERE page_id IN (SELECT p.id FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id WHERE s.workspace_id=$1)`,
			`DELETE FROM wiki_page_versions WHERE page_id IN (SELECT p.id FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id WHERE s.workspace_id=$1)`,
			`DELETE FROM wiki_pages WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`,
			`DELETE FROM wiki_spaces WHERE workspace_id=$1`,
			`DELETE FROM wiki_labels WHERE workspace_id=$1`,
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
	h := &Handler{Store: st, Commands: &commands.Service{Store: st, Blobs: blobs}, Blobs: blobs, WorkspaceSlug: ws, BaseURL: "https://zzira.test"}
	v1 := &V1Handler{Handler: h}
	send := func(handler http.Handler, prefix, method, path string, body any, want int) *httptest.ResponseRecorder {
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
		return response
	}
	callV1 := func(method, path string, body any, want int) *httptest.ResponseRecorder {
		t.Helper()
		return send(v1, "/wiki/rest/api", method, path, body, want)
	}
	callV2 := func(method, path string, body any, want int) *httptest.ResponseRecorder {
		t.Helper()
		return send(h, "/wiki/api/v2", method, path, body, want)
	}
	object := func(response *httptest.ResponseRecorder) map[string]any {
		t.Helper()
		var out map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v %s", err, response.Body.String())
		}
		return out
	}
	titles := func(parentID string) []string {
		t.Helper()
		body := object(callV2("GET", "/pages/"+parentID+"/direct-children", nil, 200))
		results, _ := body["results"].([]any)
		out := make([]string, 0, len(results))
		for _, raw := range results {
			entry, _ := raw.(map[string]any)
			out = append(out, entry["title"].(string))
		}
		return out
	}
	runner := &store.APITaskRunner{Store: st, BulkIssueExecutor: h.Commands}
	drain := func(taskID string) map[string]any {
		t.Helper()
		if err := runner.DrainOnce(ctx, ws); err != nil {
			t.Fatalf("running task %s: %v", taskID, err)
		}
		return object(callV1("GET", "/longtask/"+taskID, nil, 200))
	}
	space := object(callV2("POST", "/spaces", map[string]any{"key": "MVT", "name": "Moves"}, 201))
	spaceID, _ := space["id"].(string)
	newPage := func(title, parentID string) string {
		t.Helper()
		payload := map[string]any{"spaceId": spaceID, "title": title, "status": "current",
			"body": models.WikiBody{Representation: "storage", Value: "<p>" + title + "</p>"}}
		if parentID != "" {
			payload["parentId"] = parentID
		}
		created := object(callV2("POST", "/pages", payload, 200))
		id, _ := created["id"].(string)
		if id == "" {
			t.Fatalf("created page: %v", created)
		}
		return id
	}
	root := newPage("Root", "")
	alpha, bravo, charlie := newPage("Alpha", root), newPage("Bravo", root), newPage("Charlie", root)
	alphaChild := newPage("Alpha child", alpha)

	// Moving among siblings changes the order a reader sees, which is the
	// whole point: a move that answered 200 and reordered nothing would be
	// indistinguishable from one that worked.
	if got := titles(root); strings.Join(got, ",") != "Alpha,Bravo,Charlie" {
		t.Fatalf("initial children: %v", got)
	}
	callV1("PUT", "/content/"+charlie+"/move/before/"+alpha, nil, 200)
	if got := titles(root); strings.Join(got, ",") != "Charlie,Alpha,Bravo" {
		t.Fatalf("after moving Charlie before Alpha: %v", got)
	}
	callV1("PUT", "/content/"+charlie+"/move/after/"+bravo, nil, 200)
	if got := titles(root); strings.Join(got, ",") != "Alpha,Bravo,Charlie" {
		t.Fatalf("after moving Charlie after Bravo: %v", got)
	}
	// `append` re-parents the page.
	callV1("PUT", "/content/"+bravo+"/move/append/"+alpha, nil, 200)
	if got := titles(root); strings.Join(got, ",") != "Alpha,Charlie" {
		t.Fatalf("after re-parenting Bravo: %v", got)
	}
	if got := titles(alpha); len(got) != 2 {
		t.Fatalf("Bravo did not land under Alpha: %v", got)
	}
	// `above` re-parents and puts the page first.
	callV1("PUT", "/content/"+charlie+"/move/above/"+alpha, nil, 200)
	if got := titles(alpha); got[0] != "Charlie" {
		t.Fatalf("above did not put the page first: %v", got)
	}

	// Every refusal.
	callV1("PUT", "/content/"+alpha+"/move/sideways/"+bravo, nil, 400)
	callV1("PUT", "/content/"+alpha+"/move/append/"+alpha, nil, 400)
	callV1("PUT", "/content/"+alpha+"/move/append/"+alphaChild, nil, 400)
	callV1("PUT", "/content/9999999/move/append/"+alpha, nil, 404)

	// Copying one page answers immediately with the copy.
	copied := object(callV1("POST", "/content/"+alpha+"/copy", map[string]any{
		"copyLabels": true, "destination": map[string]any{"type": "space_key", "value": "MVT"}}, 200))
	copyID, _ := copied["id"].(string)
	if copyID == "" || copyID == alpha {
		t.Fatalf("copy: %v", copied)
	}
	// A space shows one current page per title, so the copy is renamed rather
	// than failing on the collision.
	if copied["title"] == "Alpha" {
		t.Fatalf("the copy kept a title already in the space: %v", copied)
	}
	renamed := object(callV1("POST", "/content/"+alpha+"/copy", map[string]any{
		"pageTitle": "Alpha, reconsidered", "destination": map[string]any{"type": "space_key", "value": "MVT"}}, 200))
	if renamed["title"] != "Alpha, reconsidered" {
		t.Fatalf("named copy: %v", renamed)
	}
	callV1("POST", "/content/"+alpha+"/copy", map[string]any{
		"destination": map[string]any{"type": "space_key", "value": "NOPE"}}, 400)
	callV1("POST", "/content/"+alpha+"/copy", map[string]any{
		"destination": map[string]any{"type": "sideways", "value": "MVT"}}, 400)

	// Copying a hierarchy runs in the background and reports through a task.
	target := newPage("Landing zone", "")
	accepted := object(callV1("POST", "/content/"+alpha+"/pagehierarchy/copy", map[string]any{
		"copyDescendants": true, "destinationPageId": target,
		"titleOptions": map[string]any{"prefix": "Copy of "}}, 202))
	taskID, _ := accepted["id"].(string)
	if taskID == "" {
		t.Fatalf("hierarchy copy task: %v", accepted)
	}
	finished := drain(taskID)
	if finished["successful"] != true || finished["finished"] != true {
		t.Fatalf("hierarchy copy did not finish: %v", finished)
	}
	landed := titles(target)
	if len(landed) != 1 || landed[0] != "Copy of Alpha" {
		t.Fatalf("hierarchy copy did not land under the destination: %v", landed)
	}
	callV1("POST", "/content/"+alpha+"/pagehierarchy/copy", map[string]any{"copyDescendants": true}, 400)

	// A hierarchy cannot be copied into itself, and the task says so.
	intoItself := object(callV1("POST", "/content/"+alpha+"/pagehierarchy/copy", map[string]any{
		"destinationPageId": alphaChild}, 202))
	selfTask, _ := intoItself["id"].(string)
	failed := drain(selfTask)
	if failed["successful"] != false || failed["finished"] != true {
		t.Fatalf("copying a hierarchy into itself was not refused: %v", failed)
	}

	// Archiving takes pages out of the space's current content.
	archiveAccepted := object(callV1("POST", "/content/archive", map[string]any{
		"pages": []any{map[string]any{"id": copyID}}}, 202))
	archiveTask, _ := archiveAccepted["id"].(string)
	if archived := drain(archiveTask); archived["successful"] != true {
		t.Fatalf("archive did not finish: %v", archived)
	}
	callV2("GET", "/pages/"+copyID, nil, 404)
	callV1("POST", "/content/archive", map[string]any{"pages": []any{}}, 400)

	// Trashing a tree takes the descendants with it.
	trashAccepted := object(callV1("DELETE", "/content/"+alpha+"/pageTree", nil, 202))
	trashTask, _ := trashAccepted["id"].(string)
	if trashed := drain(trashTask); trashed["successful"] != true {
		t.Fatalf("page tree trash did not finish: %v", trashed)
	}
	callV2("GET", "/pages/"+alpha, nil, 404)
	callV2("GET", "/pages/"+alphaChild, nil, 404)
	// A page that is already trashed is not a current tree, which Confluence
	// reports as a refusal rather than as a missing page.
	callV1("DELETE", "/content/"+alpha+"/pageTree", nil, 400)
	callV1("DELETE", "/content/9999999/pageTree", nil, 404)

	// The long task list reports what ran.
	listed := object(callV1("GET", "/longtask", nil, 200))
	results, _ := listed["results"].([]any)
	if len(results) != 4 {
		t.Fatalf("long task list: %v", listed)
	}
	callV1("GET", "/longtask/task_missing", nil, 404)
}
