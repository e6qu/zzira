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

// TestContentHistory pins a content item's history: restoring a version,
// deleting one, reading a macro as it was in a version, and the body
// conversions Confluence supports.
func TestContentHistory(t *testing.T) {
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
	exec(`INSERT INTO workspaces(id,slug,name) VALUES ($1,$1,'History test')`, ws)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES ($1,$2,'test','History user')`, actor, actor+"@example.test")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES ($1,$2,'admin')`, ws, actor)
	exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES ($1,$1,$2)`, actor, store.HashToken(actor))
	t.Cleanup(func() {
		for _, sql := range []string{
			`DELETE FROM wiki_body_conversions WHERE workspace_id=$1`,
			`DELETE FROM wiki_page_versions WHERE page_id IN (SELECT p.id FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id WHERE s.workspace_id=$1)`,
			`DELETE FROM wiki_pages WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`,
			`DELETE FROM wiki_spaces WHERE workspace_id=$1`,
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
	space := object(callV1("POST", "/space", map[string]any{"key": "HIS", "name": "History"}, 200))
	macro := `<p>Intro</p><ac:structured-macro ac:name="info" ac:macro-id="m-1">` +
		`<ac:parameter ac:name="title">Heads up</ac:parameter>` +
		`<ac:rich-text-body><p>Careful <strong>here</strong></p></ac:rich-text-body>` +
		`</ac:structured-macro>`
	page := object(callV2("POST", "/pages", map[string]any{"spaceId": space["id"], "title": "Runbook",
		"status": "current", "body": models.WikiBody{Representation: "storage", Value: macro}}, 200))
	pageID, _ := page["id"].(string)
	callV2("PUT", "/pages/"+pageID, map[string]any{"id": pageID, "status": "current", "title": "Runbook v2",
		"version": map[string]any{"number": 2},
		"body":    models.WikiBody{Representation: "storage", Value: "<p>Rewritten</p>"}}, 200)

	// A macro is read out of the version it was in, by the id the editor gave
	// it — the current version no longer has it.
	read := object(callV1("GET", "/content/"+pageID+"/history/1/macro/id/m-1", nil, 200))
	if read["name"] != "info" || read["body"] != "<p>Careful <strong>here</strong></p>" {
		t.Fatalf("macro: %v", read)
	}
	parameters, _ := read["parameters"].(map[string]any)
	if parameters["title"] != "Heads up" {
		t.Fatalf("macro parameters: %v", read)
	}
	callV1("GET", "/content/"+pageID+"/history/1/macro/id/nope", nil, 404)
	callV1("GET", "/content/"+pageID+"/history/2/macro/id/m-1", nil, 404)
	callV1("GET", "/content/"+pageID+"/history/zero/macro/id/m-1", nil, 400)

	// The macro converts into the formats Confluence lists.
	view := object(callV1("GET", "/content/"+pageID+"/history/1/macro/id/m-1/convert/view", nil, 200))
	if view["value"] != "<p>Careful <strong>here</strong></p>" || view["representation"] != "view" {
		t.Fatalf("macro to view: %v", view)
	}
	adf := object(callV1("GET", "/content/"+pageID+"/history/1/macro/id/m-1/convert/atlas_doc_format", nil, 200))
	if !strings.HasPrefix(adf["value"].(string), `{"type":"doc"`) {
		t.Fatalf("macro to document format: %v", adf)
	}
	callV1("GET", "/content/"+pageID+"/history/1/macro/id/m-1/convert/wiki", nil, 400)

	// The asynchronous convert answers an id, and that id is one the result
	// endpoint can read back — an id nothing can fetch would be useless.
	accepted := object(callV1("GET", "/content/"+pageID+"/history/1/macro/id/m-1/convert/async/view", nil, 200))
	asyncID, _ := accepted["asyncId"].(string)
	if asyncID == "" {
		t.Fatalf("async convert: %v", accepted)
	}
	result := object(callV1("GET", "/contentbody/convert/async/"+asyncID, nil, 200))
	if result["status"] != "COMPLETED" || result["value"] != "<p>Careful <strong>here</strong></p>" {
		t.Fatalf("async result: %v", result)
	}
	callV1("GET", "/contentbody/convert/async/conv_missing", nil, 404)

	// Restoring makes a historical version the latest by adding a version, so
	// the history keeps everything that happened.
	restored := object(callV1("POST", "/content/"+pageID+"/version", map[string]any{
		"operationKey": "restore",
		"params":       map[string]any{"versionNumber": 1, "message": "Back to the macro", "restoreTitle": true}}, 200))
	if restored["number"].(float64) != 3 {
		t.Fatalf("restore: %v", restored)
	}
	current := object(callV2("GET", "/pages/"+pageID+"?body-format=storage", nil, 200))
	if current["title"] != "Runbook" {
		t.Fatalf("restoreTitle should have brought the title back: %v", current)
	}
	body, _ := current["body"].(map[string]any)
	storage, _ := body["storage"].(map[string]any)
	if !strings.Contains(storage["value"].(string), "structured-macro") {
		t.Fatalf("the restored body should be the historical one: %v", current)
	}
	versions := object(callV2("GET", "/pages/"+pageID+"/versions", nil, 200))
	if results, _ := versions["results"].([]any); len(results) != 3 {
		t.Fatalf("restoring should add a version rather than remove one: %v", versions)
	}
	callV1("POST", "/content/"+pageID+"/version", map[string]any{"operationKey": "undo"}, 400)
	callV1("POST", "/content/"+pageID+"/version", map[string]any{
		"operationKey": "restore", "params": map[string]any{"versionNumber": 99}}, 400)

	// The current version cannot be deleted, because its changes have nowhere
	// to roll into.
	callV1("DELETE", "/content/"+pageID+"/version/3", nil, 400)
	callV1("DELETE", "/content/"+pageID+"/version/2", nil, 204)
	callV1("DELETE", "/content/"+pageID+"/version/2", nil, 404)
	after := object(callV2("GET", "/pages/"+pageID+"/versions", nil, 200))
	remaining, _ := after["results"].([]any)
	if len(remaining) != 2 {
		t.Fatalf("versions after the delete: %v", after)
	}
	// The content of the deleted version is not undone: the page still reads
	// as it did.
	if unchanged := object(callV2("GET", "/pages/"+pageID, nil, 200)); unchanged["title"] != "Runbook" {
		t.Fatalf("deleting a historical version changed the page: %v", unchanged)
	}

	// Body conversion on its own, and in bulk.
	converted := object(callV1("POST", "/contentbody/convert/async/atlas_doc_format", map[string]any{
		"value": "<p>Hello <strong>there</strong></p>", "representation": "storage"}, 200))
	convertedID, _ := converted["asyncId"].(string)
	single := object(callV1("GET", "/contentbody/convert/async/"+convertedID, nil, 200))
	if single["status"] != "COMPLETED" || !strings.Contains(single["value"].(string), `"text":"Hello "`) {
		t.Fatalf("body conversion: %v", single)
	}
	// A conversion Confluence does not support fails, and says so, rather than
	// answering a body in the wrong format.
	refused := object(callV1("POST", "/contentbody/convert/async/wiki", map[string]any{
		"value": "<p>x</p>", "representation": "storage"}, 200))
	refusedResult := object(callV1("GET", "/contentbody/convert/async/"+refused["asyncId"].(string), nil, 200))
	if refusedResult["status"] != "FAILED" || refusedResult["error"] == nil {
		t.Fatalf("an unsupported conversion should fail with a reason: %v", refusedResult)
	}

	bulk := callV1("POST", "/contentbody/convert/async/bulk/tasks", map[string]any{
		"conversionInputs": []any{
			map[string]any{"to": "view", "body": map[string]any{"value": "<p>a</p>", "representation": "storage"}},
			map[string]any{"to": "storage", "body": map[string]any{"value": "<p>b</p>", "representation": "editor"}},
		}}, 200)
	var ids []map[string]any
	if err := json.Unmarshal(bulk.Body.Bytes(), &ids); err != nil || len(ids) != 2 {
		t.Fatalf("bulk conversion: %v %s", err, bulk.Body.String())
	}
	callV1("POST", "/contentbody/convert/async/bulk/tasks", map[string]any{"conversionInputs": []any{}}, 400)
	bulkResults := callV1("GET", "/contentbody/convert/async/bulk/tasks?ids="+
		ids[0]["asyncId"].(string)+","+ids[1]["asyncId"].(string)+",conv_missing", nil, 200)
	var fetched []map[string]any
	if err := json.Unmarshal(bulkResults.Body.Bytes(), &fetched); err != nil || len(fetched) != 3 {
		t.Fatalf("bulk results: %v %s", err, bulkResults.Body.String())
	}
	// One id that cannot be read does not fail the others, because a bulk read
	// asks about several independent conversions.
	if fetched[0]["status"] != "COMPLETED" || fetched[2]["status"] != "FAILED" {
		t.Fatalf("bulk results: %v", fetched)
	}
	callV1("GET", "/contentbody/convert/async/bulk/tasks", nil, 400)
}
