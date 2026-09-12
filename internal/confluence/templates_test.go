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

// TestContentTemplates pins Confluence's template surface: content templates
// written through the API, the templates blueprints provide, and publishing a
// draft made from a blueprint.
func TestContentTemplates(t *testing.T) {
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
	exec(`INSERT INTO workspaces(id,slug,name) VALUES ($1,$1,'Template test')`, ws)
	for _, value := range []struct{ id, role string }{{admin, "admin"}, {member, "member"}} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES ($1,$2,'test','Template user')`, value.id, value.id+"@example.test")
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES ($1,$2,$3)`, ws, value.id, value.role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES ($1,$1,$2)`, value.id, store.HashToken(value.id))
	}
	t.Cleanup(func() {
		for _, sql := range []string{
			`DELETE FROM wiki_content_templates WHERE workspace_id=$1`,
			`DELETE FROM wiki_page_versions WHERE page_id IN (SELECT p.id FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id WHERE s.workspace_id=$1)`,
			`DELETE FROM wiki_pages WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`,
			`DELETE FROM wiki_spaces WHERE workspace_id=$1`,
			`DELETE FROM memberships WHERE workspace_id=$1`,
			`DELETE FROM workspaces WHERE id=$1`,
		} {
			exec(sql, ws)
		}
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
	v1 := &V1Handler{Handler: h}
	send := func(handler http.Handler, prefix, user, method, path string, body any, want int) *httptest.ResponseRecorder {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(method, prefix+path, strings.NewReader(string(raw)))
		request.SetBasicAuth(user+"@example.test", user)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s as %s: got %d want %d: %s", method, path, user, response.Code, want, response.Body.String())
		}
		return response
	}
	callV1 := func(user, method, path string, body any, want int) *httptest.ResponseRecorder {
		t.Helper()
		return send(v1, "/wiki/rest/api", user, method, path, body, want)
	}
	callV2 := func(user, method, path string, body any, want int) *httptest.ResponseRecorder {
		t.Helper()
		return send(h, "/wiki/api/v2", user, method, path, body, want)
	}
	object := func(response *httptest.ResponseRecorder) map[string]any {
		t.Helper()
		var out map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v %s", err, response.Body.String())
		}
		return out
	}
	names := func(response *httptest.ResponseRecorder) []string {
		t.Helper()
		body := object(response)
		results, _ := body["results"].([]any)
		out := make([]string, 0, len(results))
		for _, raw := range results {
			entry, _ := raw.(map[string]any)
			out = append(out, entry["name"].(string))
		}
		return out
	}
	space := object(callV1(admin, "POST", "/space", map[string]any{"key": "TPL", "name": "Templates"}, 200))
	spaceID, _ := space["id"].(string)

	// The blueprints provide their templates whether or not anyone has written
	// one, and each names the blueprint it came from.
	blueprints := object(callV1(member, "GET", "/template/blueprint", nil, 200))
	blueprintResults, _ := blueprints["results"].([]any)
	if len(blueprintResults) == 0 {
		t.Fatalf("blueprint templates: %v", blueprints)
	}
	first, _ := blueprintResults[0].(map[string]any)
	original, _ := first["originalTemplate"].(map[string]any)
	if original["moduleKey"] == "" || original["pluginKey"] == "" {
		t.Fatalf("a blueprint template names the blueprint it came from: %v", first)
	}
	// A space inherits every global blueprint.
	if inSpace := names(callV1(member, "GET", "/template/blueprint?spaceKey=TPL", nil, 200)); len(inSpace) != len(blueprintResults) {
		t.Fatalf("a space should inherit the global blueprints: %v", inSpace)
	}
	callV1(member, "GET", "/template/blueprint?spaceKey=NOPE", nil, 404)

	// A content template is written through the API. The create answers with
	// the template it made, which is what a client needs to use it.
	created := object(callV1(admin, "POST", "/template", map[string]any{
		"name": "Runbook", "description": "Ops runbook", "templateType": "page",
		"body":   map[string]any{"storage": map[string]any{"value": "<h2>Steps</h2>", "representation": "storage"}},
		"labels": []any{map[string]any{"name": "ops"}}}, 200))
	templateID, _ := created["templateId"].(string)
	if templateID == "" || created["name"] != "Runbook" {
		t.Fatalf("created template: %v", created)
	}
	read := object(callV1(member, "GET", "/template/"+templateID, nil, 200))
	if read["name"] != "Runbook" || read["description"] != "Ops runbook" {
		t.Fatalf("template read: %v", read)
	}
	body, _ := read["body"].(map[string]any)
	storage, _ := body["storage"].(map[string]any)
	if storage["value"] != "<h2>Steps</h2>" {
		t.Fatalf("template body: %v", read)
	}
	labels, _ := read["labels"].([]any)
	if len(labels) != 1 || labels[0].(map[string]any)["name"] != "ops" {
		t.Fatalf("template labels: %v", read)
	}

	updated := object(callV1(admin, "PUT", "/template", map[string]any{
		"templateId": templateID, "name": "Runbook v2", "templateType": "page",
		"body": map[string]any{"storage": map[string]any{"value": "<h2>Rollback</h2>", "representation": "storage"}}}, 200))
	if updated["name"] != "Runbook v2" {
		t.Fatalf("updated template: %v", updated)
	}
	if after := object(callV1(member, "GET", "/template/"+templateID, nil, 200)); after["name"] != "Runbook v2" {
		t.Fatalf("the update did not stick: %v", after)
	}

	// Every refusal.
	callV1(admin, "POST", "/template", map[string]any{"name": "", "templateType": "page"}, 400)
	callV1(admin, "POST", "/template", map[string]any{"name": "Odd", "templateType": "sideways"}, 400)
	callV1(admin, "PUT", "/template", map[string]any{"name": "No id"}, 400)
	callV1(member, "POST", "/template", map[string]any{"name": "Member template", "templateType": "page"}, 403)
	// A blueprint owns its template, so the API will not write it.
	callV1(admin, "PUT", "/template", map[string]any{
		"templateId": "blueprint:decision", "name": "Mine", "templateType": "page"}, 400)
	callV1(admin, "DELETE", "/template/blueprint:decision", nil, 400)
	callV1(member, "GET", "/template/9999999", nil, 404)

	// A blueprint template is readable by its own id.
	blueprint := object(callV1(member, "GET", "/template/blueprint:decision", nil, 200))
	if blueprint["name"] != "Decision" {
		t.Fatalf("blueprint by id: %v", blueprint)
	}

	// A space's templates include the site's, because a space inherits them.
	spaceTemplate := object(callV1(admin, "POST", "/template", map[string]any{
		"name": "Space runbook", "templateType": "page", "space": map[string]any{"key": "TPL"},
		"body": map[string]any{"storage": map[string]any{"value": "<p>x</p>", "representation": "storage"}}}, 200))
	if spaceScope, _ := spaceTemplate["space"].(map[string]any); spaceScope["key"] != "TPL" {
		t.Fatalf("space template: %v", spaceTemplate)
	}
	siteOnly := names(callV1(member, "GET", "/template/page", nil, 200))
	if len(siteOnly) != 1 || siteOnly[0] != "Runbook v2" {
		t.Fatalf("the site listing should not carry a space's templates: %v", siteOnly)
	}
	withSpace := names(callV1(member, "GET", "/template/page?spaceKey=TPL", nil, 200))
	if len(withSpace) != 2 {
		t.Fatalf("a space listing carries the site's templates too: %v", withSpace)
	}

	callV1(member, "DELETE", "/template/"+templateID, nil, 403)
	callV1(admin, "DELETE", "/template/"+templateID, nil, 204)
	callV1(member, "GET", "/template/"+templateID, nil, 404)

	// Publishing a draft made from a blueprint turns it into a page.
	draft := object(callV2(admin, "POST", "/pages", map[string]any{"spaceId": spaceID, "title": "Draft notes",
		"status": "draft", "body": models.WikiBody{Representation: "storage", Value: "<p>notes</p>"}}, 200))
	draftID, _ := draft["id"].(string)
	published := object(callV1(admin, "PUT", "/content/blueprint/instance/"+draftID, map[string]any{
		"title": "Meeting notes", "version": map[string]any{"number": 2}, "space": map[string]any{"key": "TPL"}}, 200))
	if published["status"] != "current" || published["title"] != "Meeting notes" {
		t.Fatalf("published draft: %v", published)
	}
	if publishedSpace, _ := published["space"].(map[string]any); publishedSpace["key"] != "TPL" {
		t.Fatalf("published draft space: %v", published)
	}
	// A draft already published is no longer a draft to publish.
	callV1(admin, "PUT", "/content/blueprint/instance/"+draftID, map[string]any{"title": "Again"}, 404)

	// The legacy draft endpoint behaves the same way, which is what Confluence
	// says of it.
	legacy := object(callV2(admin, "POST", "/pages", map[string]any{"spaceId": spaceID, "title": "Legacy draft",
		"status": "draft", "body": models.WikiBody{Representation: "storage", Value: "<p>legacy</p>"}}, 200))
	legacyID, _ := legacy["id"].(string)
	legacyPublished := object(callV1(admin, "POST", "/content/blueprint/instance/"+legacyID, map[string]any{
		"title": "Legacy notes"}, 200))
	if legacyPublished["status"] != "current" || legacyPublished["title"] != "Legacy notes" {
		t.Fatalf("legacy publish: %v", legacyPublished)
	}
	// A stale version is refused, as it is everywhere else content is written.
	stale := object(callV2(admin, "POST", "/pages", map[string]any{"spaceId": spaceID, "title": "Stale draft",
		"status": "draft", "body": models.WikiBody{Representation: "storage", Value: "<p>stale</p>"}}, 200))
	callV1(admin, "PUT", "/content/blueprint/instance/"+stale["id"].(string), map[string]any{
		"title": "Stale", "version": map[string]any{"number": 9}}, 409)
}
