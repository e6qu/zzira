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

// TestSpaceLifecycle pins creating, updating and deleting a space, its
// settings and its theme, the private and personal kinds, and the data policy
// read.
func TestSpaceLifecycle(t *testing.T) {
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
	exec(`INSERT INTO workspaces(id,slug,name) VALUES ($1,$1,'Space lifecycle test')`, ws)
	for _, value := range []struct{ id, role string }{{admin, "admin"}, {member, "member"}} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES ($1,$2,'test','Space user')`, value.id, value.id+"@example.test")
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES ($1,$2,$3)`, ws, value.id, value.role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES ($1,$1,$2)`, value.id, store.HashToken(value.id))
	}
	t.Cleanup(func() {
		for _, sql := range []string{
			`DELETE FROM api_tasks WHERE workspace_id=$1`,
			`DELETE FROM wiki_space_permission_grants WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`,
			`DELETE FROM wiki_page_versions WHERE page_id IN (SELECT p.id FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id WHERE s.workspace_id=$1)`,
			`UPDATE wiki_spaces SET homepage_id=NULL WHERE workspace_id=$1`,
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

	created := object(callV1(admin, "POST", "/space", map[string]any{
		"key": "LIF", "name": "Lifecycle",
		"description": map[string]any{"plain": map[string]any{"value": "A space", "representation": "plain"}}}, 200))
	if created["key"] != "LIF" || created["type"] != "global" || created["status"] != "current" {
		t.Fatalf("created space: %v", created)
	}
	if icon, _ := created["icon"].(map[string]any); icon["isDefault"] != true {
		t.Fatalf("the older surface reports an icon: %v", created)
	}
	callV1(admin, "POST", "/space", map[string]any{"key": "LIF", "name": "Again"}, 400)
	callV1(admin, "POST", "/space", map[string]any{"key": "has space", "name": "Bad"}, 400)
	callV1(admin, "POST", "/space", map[string]any{"key": "NONAME"}, 400)
	callV1(member, "POST", "/space", map[string]any{"key": "MEMBER", "name": "Member space"}, 403)

	// A private space is the ordinary create with permissions set to the
	// creator alone, so it is a global space that only they can reach.
	private := object(callV1(admin, "POST", "/space/_private", map[string]any{"key": "PRIV", "name": "Private"}, 200))
	if private["type"] != "global" {
		t.Fatalf("a private space is a global space with narrow permissions: %v", private)
	}
	callV2(member, "GET", "/spaces/"+private["id"].(string), nil, 404)
	callV2(admin, "GET", "/spaces/"+private["id"].(string), nil, 200)

	// A personal space belongs to one person and is keyed by their account.
	personal := object(callV1(admin, "POST", "/space", map[string]any{
		"key": store.PersonalSpaceKey(admin), "name": "Admin's space"}, 200))
	if personal["type"] != "personal" || personal["key"] != "~"+admin {
		t.Fatalf("personal space: %v", personal)
	}

	// The update changes what Confluence allows it to.
	renamed := object(callV1(admin, "PUT", "/space/LIF", map[string]any{
		"name": "Lifecycle renamed", "description": map[string]any{"plain": map[string]any{"value": "Updated"}}}, 200))
	if renamed["name"] != "Lifecycle renamed" {
		t.Fatalf("renamed space: %v", renamed)
	}
	page := object(callV2(admin, "POST", "/pages", map[string]any{"spaceId": created["id"], "title": "Home",
		"status": "current", "body": models.WikiBody{Representation: "storage", Value: "<p>home</p>"}}, 200))
	homed := object(callV1(admin, "PUT", "/space/LIF", map[string]any{
		"homepage": map[string]any{"id": page["id"]}}, 200))
	if homed["homepageId"] != page["id"] {
		t.Fatalf("homepage: %v", homed)
	}
	// A homepage has to be a page in this space.
	callV1(admin, "PUT", "/space/LIF", map[string]any{"homepage": map[string]any{"id": "9999999"}}, 400)
	archived := object(callV1(admin, "PUT", "/space/LIF", map[string]any{"status": "archived"}, 200))
	if archived["status"] != "archived" {
		t.Fatalf("archived: %v", archived)
	}
	callV1(admin, "PUT", "/space/LIF", map[string]any{"status": "sideways"}, 400)
	callV1(admin, "PUT", "/space/LIF", map[string]any{"type": "sideways"}, 400)
	callV1(admin, "PUT", "/space/NOPE", map[string]any{"name": "Ghost"}, 404)
	callV1(member, "PUT", "/space/LIF", map[string]any{"name": "Member rename"}, 403)

	// Settings: reading needs only the permission to view the space.
	settings := object(callV1(member, "GET", "/space/LIF/settings", nil, 200))
	if settings["contentMode"] != "standard" || settings["routeOverrideEnabled"] != false || settings["spaceKey"] != "LIF" {
		t.Fatalf("settings: %v", settings)
	}
	updated := object(callV1(admin, "PUT", "/space/LIF/settings", map[string]any{
		"contentMode": "compact", "routeOverrideEnabled": true}, 200))
	if updated["contentMode"] != "compact" || updated["routeOverrideEnabled"] != true {
		t.Fatalf("updated settings: %v", updated)
	}
	callV1(admin, "PUT", "/space/LIF/settings", map[string]any{"contentMode": "weird"}, 400)
	callV1(member, "PUT", "/space/LIF/settings", map[string]any{"contentMode": "compact"}, 403)

	// A space with no theme inherits the site's look and feel, which is
	// reported as no theme rather than as a default one.
	callV1(admin, "GET", "/space/LIF/theme", nil, 404)
	callV1(admin, "DELETE", "/space/LIF/theme", nil, 404)
	themed := object(callV1(admin, "PUT", "/space/LIF/theme", map[string]any{
		"themeKey": "com.atlassian.confluence.plugins.confluence-documentation-theme:documentation"}, 200))
	if themed["name"] != "Documentation theme" {
		t.Fatalf("theme: %v", themed)
	}
	if read := object(callV1(member, "GET", "/space/LIF/theme", nil, 200)); read["themeKey"] != themed["themeKey"] {
		t.Fatalf("theme read: %v", read)
	}
	callV1(admin, "PUT", "/space/LIF/theme", map[string]any{"themeKey": "nope"}, 400)
	callV1(member, "PUT", "/space/LIF/theme", map[string]any{
		"themeKey": "com.atlassian.confluence.plugins.confluence-dark-theme:dark"}, 403)
	callV1(admin, "DELETE", "/space/LIF/theme", nil, 204)
	callV1(admin, "GET", "/space/LIF/theme", nil, 404)

	// The role mode is read from what the site holds. The private space wrote
	// direct grants and nothing has been transitioned, so it has not started.
	if mode := object(callV2(admin, "GET", "/space-role-mode", nil, 200)); mode["mode"] != "PRE_ROLES" {
		t.Fatalf("role mode: %v", mode)
	}

	policies := object(callV2(admin, "GET", "/data-policies/spaces", nil, 200))
	results, _ := policies["results"].([]any)
	if len(results) == 0 {
		t.Fatalf("data policies: %v", policies)
	}
	first, _ := results[0].(map[string]any)
	status, _ := first["status"].(map[string]any)
	policy, _ := status["dataPolicy"].(map[string]any)
	if policy["anyContentBlocked"] != false {
		t.Fatalf("data policy: %v", first)
	}

	// Deleting answers with the task that reports it, and that task is one the
	// long task read will show.
	callV1(member, "DELETE", "/space/LIF", nil, 403)
	accepted := object(callV1(admin, "DELETE", "/space/LIF", nil, 202))
	taskID, _ := accepted["id"].(string)
	links, _ := accepted["links"].(map[string]any)
	if taskID == "" || links["status"] == "" {
		t.Fatalf("delete task: %v", accepted)
	}
	runner := &store.APITaskRunner{Store: st, BulkIssueExecutor: h.Commands}
	if err := runner.DrainOnce(ctx, ws); err != nil {
		t.Fatal(err)
	}
	reported := object(callV1(admin, "GET", "/longtask/"+taskID, nil, 200))
	if reported["successful"] != true || reported["finished"] != true {
		t.Fatalf("the delete task is not reported as finished: %v", reported)
	}
	callV1(admin, "PUT", "/space/LIF", map[string]any{"name": "Gone"}, 404)
	callV1(admin, "DELETE", "/space/NOPE", nil, 404)
}
