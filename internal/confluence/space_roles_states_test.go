package confluence

import (
	"bytes"
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

// TestSpaceRolesAndContentStateSettings pins role permission dependencies,
// principal filtering across spaces, moving anonymous and guest assignments
// when a role changes, role changes and deletions answered as long tasks, a
// space's content state settings and their enforcement, and content in a state
// with expansions.
func TestSpaceRolesAndContentStateSettings(t *testing.T) {
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
	ws, admin, member, guest := store.NewID("ws"), store.NewID("usr"), store.NewID("usr"), store.NewID("usr")
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES ($1,$1,'Roles test')`, ws)
	for _, user := range []string{admin, member, guest} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES ($1,$2,'test',$1)`, user, user+"@example.test")
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES ($1,$1,$2)`, user, store.HashToken(user))
	}
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES ($1,$2,'admin'),($1,$3,'member'),($1,$4,'member')`, ws, admin, member, guest)
	exec(`INSERT INTO directory_users(directory_id,user_id) SELECT d.id,u FROM directories d JOIN sites si ON si.organization_id=d.organization_id AND si.workspace_id=$1, unnest($2::text[]) u ON CONFLICT DO NOTHING`, ws, []string{admin, member, guest})
	t.Cleanup(func() {
		for _, sql := range []string{
			`DELETE FROM api_tasks WHERE workspace_id=$1`,
			`DELETE FROM wiki_page_versions WHERE page_id IN (SELECT p.id FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id WHERE s.workspace_id=$1)`,
			`DELETE FROM wiki_pages WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`,
			`DELETE FROM wiki_spaces WHERE workspace_id=$1`,
			`DELETE FROM wiki_space_roles WHERE workspace_id=$1`,
			`DELETE FROM actions WHERE workspace_id=$1`,
			`DELETE FROM memberships WHERE workspace_id=$1`,
			`DELETE FROM workspaces WHERE id=$1`,
		} {
			exec(sql, ws)
		}
		for _, user := range []string{admin, member, guest} {
			exec(`DELETE FROM role_bindings WHERE principal_id=$1`, user)
			exec(`DELETE FROM api_tokens WHERE user_id=$1`, user)
			exec(`DELETE FROM users WHERE id=$1`, user)
		}
	})
	blobs, err := attachments.NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{Store: st, Commands: &commands.Service{Store: st, Blobs: blobs}, Blobs: blobs, WorkspaceSlug: ws, BaseURL: "https://zzira.test"}
	v1 := &V1Handler{Handler: h}
	call := func(user, method, path string, body any, want int) map[string]any {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(method, path, bytes.NewReader(raw))
		request.SetBasicAuth(user+"@example.test", user)
		request.Header.Set("X-Atlassian-Token", "no-check")
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		if strings.HasPrefix(path, "/wiki/api/v2") {
			h.ServeHTTP(response, request)
		} else {
			v1.ServeHTTP(response, request)
		}
		if response.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, response.Code, want, response.Body.String())
		}
		out := map[string]any{}
		_ = json.Unmarshal(response.Body.Bytes(), &out)
		return out
	}
	roleIDs := func(body map[string]any) map[string]bool {
		ids := map[string]bool{}
		for _, item := range body["results"].([]any) {
			ids[item.(map[string]any)["id"].(string)] = true
		}
		return ids
	}
	space := call(admin, "POST", "/wiki/api/v2/spaces", map[string]any{"key": "ROLES", "name": "Roles"}, 201)["id"].(string)
	other := call(admin, "POST", "/wiki/api/v2/spaces", map[string]any{"key": "OTHER", "name": "Other"}, 201)["id"].(string)

	// A role's permissions must include what they depend on.
	call(admin, "POST", "/wiki/api/v2/space-roles", map[string]any{"name": "Editors", "description": "Edit", "spacePermissions": []string{"read/space", "update/page"}}, 400)
	role := call(admin, "POST", "/wiki/api/v2/space-roles", map[string]any{"name": "Editors", "description": "Edit", "spacePermissions": []string{"read/space", "read/page", "update/page"}}, 201)["id"].(string)

	// A principal's roles are found across every space.
	exec(`INSERT INTO role_bindings(scope_type,scope_id,role_key,principal_type,principal_id,source) SELECT 'product',p.id::text,'atlassian/guest','user',$2,'manual' FROM products p JOIN sites si ON si.id=p.site_id WHERE si.workspace_id=$1 AND p.product_key='confluence'`, ws, guest)
	call(admin, "POST", "/wiki/api/v2/spaces/"+space+"/role-assignments", []map[string]any{
		{"roleId": "system-admin", "principal": map[string]string{"principalType": "USER", "principalId": admin}},
		{"roleId": role, "principal": map[string]string{"principalType": "ACCESS_CLASS", "principalId": "anonymous-users"}},
		{"roleId": role, "principal": map[string]string{"principalType": "USER", "principalId": guest}},
		{"roleId": role, "principal": map[string]string{"principalType": "USER", "principalId": member}},
	}, 204)
	call(admin, "POST", "/wiki/api/v2/spaces/"+other+"/role-assignments", []map[string]any{
		{"roleId": "system-admin", "principal": map[string]string{"principalType": "USER", "principalId": admin}},
		{"roleId": "system-viewer", "principal": map[string]string{"principalType": "USER", "principalId": member}},
	}, 204)
	if roles := roleIDs(call(admin, "GET", "/wiki/api/v2/space-roles?principal-type=USER&principal-id="+member, nil, 200)); len(roles) != 2 || !roles[role] || !roles["system-viewer"] {
		t.Fatalf("member's roles across spaces: %v", roles)
	}

	// Changing a role can move anonymous access and guests to another role, as
	// a long task.
	updated := call(admin, "PUT", "/wiki/api/v2/space-roles/"+role, map[string]any{"name": "Editors", "description": "Edit pages", "spacePermissions": []string{"read/space", "read/page", "update/page"},
		"anonymousReassignmentRoleId": "system-viewer", "guestReassignmentRoleId": "system-viewer"}, 202)
	if updated["taskId"] == nil || updated["description"] != "Edit pages" {
		t.Fatalf("updated role: %v", updated)
	}
	assignments := mustJSON(t, call(admin, "GET", "/wiki/api/v2/spaces/"+space+"/role-assignments", nil, 200))
	for _, want := range []string{`{"principal":{"principalId":"anonymous-users","principalType":"ACCESS_CLASS"},"roleId":"system-viewer"}`, `{"principal":{"principalId":"` + guest + `","principalType":"USER"},"roleId":"system-viewer"}`, `{"principal":{"principalId":"` + member + `","principalType":"USER"},"roleId":"` + role + `"}`} {
		if !strings.Contains(assignments, want) {
			t.Fatalf("assignments lack %s: %s", want, assignments)
		}
	}
	task := call(admin, "GET", "/wiki/rest/api/longtask/"+updated["taskId"].(string), nil, 200)
	if !strings.Contains(mustJSON(t, task), `"finished":true`) && !strings.Contains(mustJSON(t, task), `"successful":true`) {
		t.Fatalf("role update task: %v", task)
	}
	call(admin, "PUT", "/wiki/api/v2/space-roles/"+role, map[string]any{"name": "Editors", "description": "Edit", "spacePermissions": []string{"read/space"}, "anonymousReassignmentRoleId": role}, 400)
	if deleted := call(admin, "DELETE", "/wiki/api/v2/space-roles/"+role, nil, 202); deleted["taskId"] == nil {
		t.Fatalf("deleted role: %v", deleted)
	}
	call(admin, "GET", "/wiki/api/v2/space-roles/"+role, nil, 404)

	// A space's administrators choose which content states its pages carry.
	page := call(admin, "POST", "/wiki/api/v2/pages", map[string]any{"spaceId": space, "title": "Plan", "status": "current", "body": map[string]any{"representation": "storage", "value": "<p>Plan</p>"}}, 200)["id"].(string)
	settings := call(admin, "GET", "/wiki/rest/api/space/ROLES/state/settings", nil, 200)
	if settings["contentStatesAllowed"] != true || len(settings["spaceContentStates"].([]any)) == 0 {
		t.Fatalf("default settings: %v", settings)
	}
	call(member, "GET", "/wiki/rest/api/space/OTHER/state/settings", nil, 403)
	if err := st.SetWikiContentStateSettings(ctx, ws, admin, "ROLES", store.WikiContentStateSettings{ContentStatesAllowed: true, SpaceContentStatesAllowed: true}); err != nil {
		t.Fatal(err)
	}
	call(admin, "PUT", "/wiki/rest/api/content/"+page+"/state?status=current", map[string]any{"name": "Waiting", "color": "#FF8B00"}, 400)
	call(admin, "PUT", "/wiki/rest/api/content/"+page+"/state?status=current", map[string]any{"id": 2}, 200)
	withVersion := call(admin, "GET", "/wiki/rest/api/space/ROLES/state/content?state-id=2&expand=version", nil, 200)
	if results := withVersion["results"].([]any); len(results) != 1 || results[0].(map[string]any)["version"] == nil {
		t.Fatalf("content in a state: %v", withVersion)
	}
	if err := st.SetWikiContentStateSettings(ctx, ws, admin, "ROLES", store.WikiContentStateSettings{}); err != nil {
		t.Fatal(err)
	}
	if off := call(admin, "GET", "/wiki/rest/api/space/ROLES/state/settings", nil, 200); off["contentStatesAllowed"] != false || len(off["spaceContentStates"].([]any)) != 0 {
		t.Fatalf("settings with states off: %v", off)
	}
	call(admin, "PUT", "/wiki/rest/api/content/"+page+"/state?status=current", map[string]any{"id": 3}, 400)

	// Transition combinations page with a limit and cursor.
	call(admin, "GET", "/wiki/api/v2/space-permissions/transition/combinations?limit=0", nil, 400)
	call(admin, "GET", "/wiki/api/v2/space-permissions/transition/combinations?cursor=%21", nil, 400)
	call(admin, "GET", "/wiki/api/v2/space-permissions/transition/combinations?limit=1", nil, 200)
}
