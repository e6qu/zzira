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

// TestSpaceCreationAccessAndFilters pins creating spaces with templates,
// aliases, role assignments and copied access; permissions read from what the
// space holds; and listing spaces by type, status and who starred them.
func TestSpaceCreationAccessAndFilters(t *testing.T) {
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
	exec(`INSERT INTO workspaces(id,slug,name) VALUES ($1,$1,'Spaces test')`, ws)
	for _, user := range []string{admin, member} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES ($1,$2,'test',$1)`, user, user+"@example.test")
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES ($1,$1,$2)`, user, store.HashToken(user))
	}
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES ($1,$2,'admin'),($1,$3,'member')`, ws, admin, member)
	t.Cleanup(func() {
		for _, sql := range []string{
			`DELETE FROM wiki_relations WHERE workspace_id=$1`,
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
		for _, user := range []string{admin, member} {
			exec(`DELETE FROM api_tokens WHERE user_id=$1`, user)
			exec(`DELETE FROM users WHERE id=$1`, user)
		}
	})
	blobs, err := attachments.NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{Store: st, Commands: &commands.Service{Store: st, Blobs: blobs}, Blobs: blobs, WorkspaceSlug: ws, BaseURL: "https://zzira.test"}
	v2 := func(user, method, path string, body any, want int) map[string]any {
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
		out := map[string]any{}
		if strings.HasPrefix(strings.TrimSpace(response.Body.String()), "{") {
			if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
				t.Fatalf("decode %s: %v", path, err)
			}
		}
		return out
	}
	keysOf := func(body map[string]any) []string {
		list, _ := body["results"].([]any)
		keys := []string{}
		for _, item := range list {
			keys = append(keys, item.(map[string]any)["key"].(string))
		}
		return keys
	}

	// A template decides the kind of space and writes its homepage.
	kb := v2(admin, "POST", "/spaces", map[string]any{"key": "KB", "name": "Support answers", "templateKey": "com.atlassian.confluence.plugins.confluence-knowledge-base:knowledge-base-space-blueprint"}, 201)
	if kb["type"] != "knowledge_base" || kb["homepageId"] == nil {
		t.Fatalf("space from a template: %v", kb)
	}
	if home := v2(admin, "GET", "/pages/"+kb["homepageId"].(string), nil, 200); home["title"] != "Knowledge base" || home["spaceId"] != kb["id"] {
		t.Fatalf("template homepage: %v", home)
	}
	v2(admin, "POST", "/spaces", map[string]any{"key": "NOPE", "name": "Nope", "templateKey": "no-such-template"}, 400)

	// A space named by an alias takes its key from it.
	if handbook := v2(admin, "POST", "/spaces", map[string]any{"alias": "handbook", "name": "Handbook"}, 201); handbook["key"] != "HANDBOOK" || handbook["currentActiveAlias"] != "handbook" {
		t.Fatalf("space from an alias: %v", handbook)
	}
	v2(admin, "POST", "/spaces", map[string]any{"name": "Nameless"}, 400)

	// Role assignments are the space's exact access, one of them administering it.
	v2(admin, "POST", "/spaces", map[string]any{"key": "NOADMIN", "name": "No admin", "roleAssignments": []map[string]any{
		{"roleId": "system-viewer", "principal": map[string]string{"principalType": "ACCESS_CLASS", "principalId": "authenticated-users"}},
	}}, 400)
	team := v2(admin, "POST", "/spaces", map[string]any{"key": "TEAM", "name": "Team", "roleAssignments": []map[string]any{
		{"roleId": "system-admin", "principal": map[string]string{"principalType": "USER", "principalId": member}},
		{"roleId": "system-viewer", "principal": map[string]string{"principalType": "ACCESS_CLASS", "principalId": "authenticated-users"}},
	}}, 201)
	teamID := team["id"].(string)
	withAccess := mustJSON(t, v2(member, "GET", "/spaces/"+teamID+"?include-permissions=true&include-role-assignments=true", nil, 200))
	for _, want := range []string{`"principal":{"id":"` + member + `","type":"user"}`, `"operation":{"key":"administer","targetType":"space"}`, `"principal":{"id":"authenticated-users","type":"role"}`, `"roleId":"system-viewer"`} {
		if !strings.Contains(withAccess, want) {
			t.Fatalf("space access lacks %s: %s", want, withAccess)
		}
	}
	v2(member, "POST", "/spaces/"+teamID+"/role-assignments", []map[string]any{
		{"roleId": "system-admin", "principal": map[string]string{"principalType": "USER", "principalId": member}},
	}, 204)

	// Assigning only yourself as administrator makes a private space.
	mine := v2(admin, "POST", "/spaces", map[string]any{"key": "MINE", "name": "Mine", "roleAssignments": []map[string]any{
		{"roleId": "system-admin", "principal": map[string]string{"principalType": "USER", "principalId": admin}},
	}}, 201)
	v2(member, "GET", "/spaces/"+mine["id"].(string), nil, 404)

	// Copying another space's access copies its roles.
	source := v2(admin, "POST", "/spaces", map[string]any{"key": "SOURCE", "name": "Source", "roleAssignments": []map[string]any{
		{"roleId": "system-admin", "principal": map[string]string{"principalType": "ACCESS_CLASS", "principalId": "all-product-admins"}},
		{"roleId": "system-member", "principal": map[string]string{"principalType": "USER", "principalId": member}},
	}}, 201)
	copied := v2(admin, "POST", "/spaces", map[string]any{"key": "COPY", "name": "Copy", "copySpaceAccessConfiguration": json.Number(source["id"].(string))}, 201)
	if assignments := mustJSON(t, v2(admin, "GET", "/spaces/"+copied["id"].(string)+"/role-assignments", nil, 200)); !strings.Contains(assignments, `"principalId":"`+member+`"`) || !strings.Contains(assignments, `"principalId":"all-product-admins"`) {
		t.Fatalf("copied access: %s", assignments)
	}
	v2(admin, "POST", "/spaces", map[string]any{"key": "BOTH", "name": "Both", "copySpaceAccessConfiguration": json.Number(source["id"].(string)), "createPrivateSpace": true}, 400)

	// Spaces list by type, status and who starred them.
	if keys := keysOf(v2(admin, "GET", "/spaces?type=knowledge_base", nil, 200)); len(keys) != 1 || keys[0] != "KB" {
		t.Fatalf("knowledge bases: %v", keys)
	}
	v2(admin, "GET", "/spaces?type=wiki", nil, 400)
	v2(admin, "GET", "/spaces?status=gone", nil, 400)
	archived := "archived"
	if _, err := st.UpdateWikiSpace(ctx, ws, admin, "HANDBOOK", store.UpdateWikiSpaceInput{Status: &archived}); err != nil {
		t.Fatal(err)
	}
	if keys := keysOf(v2(admin, "GET", "/spaces?status=archived", nil, 200)); len(keys) != 1 || keys[0] != "HANDBOOK" {
		t.Fatalf("archived spaces: %v", keys)
	}
	exec(`INSERT INTO wiki_relations(workspace_id,name,source_type,source_key,source_status,source_version,target_type,target_key,target_status,target_version,created_by)
		VALUES($1,'favourite','user',$2,'current',0,'space','KB','current',0,$2)`, ws, admin)
	if keys := keysOf(v2(admin, "GET", "/spaces?favorited-by="+admin, nil, 200)); len(keys) != 1 || keys[0] != "KB" {
		t.Fatalf("starred spaces: %v", keys)
	}
	if keys := keysOf(v2(admin, "GET", "/spaces?not-favorited-by="+admin, nil, 200)); strings.Contains(strings.Join(keys, ","), "KB") || len(keys) == 0 {
		t.Fatalf("unstarred spaces: %v", keys)
	}
}
