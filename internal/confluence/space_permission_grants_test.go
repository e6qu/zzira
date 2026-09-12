package confluence

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/attachments"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

// TestSpacePermissionGrants pins Confluence's space permission surface: the
// catalogue of what may be granted, the role mode, granting and removing a
// permission, and checking whether a subject may act on a piece of content.
func TestSpacePermissionGrants(t *testing.T) {
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
	ws, admin, member, outsider := store.NewID("ws"), store.NewID("usr"), store.NewID("usr"), store.NewID("usr")
	organizationID, directoryID, groupID := "", "", ""
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES ($1,$1,'Space permission test')`, ws)
	for _, value := range []struct{ id, role string }{{admin, "admin"}, {member, "member"}, {outsider, "member"}} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES ($1,$2,'test','Permission user')`, value.id, value.id+"@example.test")
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES ($1,$2,$3)`, ws, value.id, value.role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES ($1,$1,$2)`, value.id, store.HashToken(value.id))
	}
	if err := st.Pool.QueryRow(ctx, `INSERT INTO organizations(name) VALUES($1) RETURNING id::text`, "Perm org "+ws).Scan(&organizationID); err != nil {
		t.Fatal(err)
	}
	if err := st.Pool.QueryRow(ctx, `INSERT INTO directories(organization_id,name) VALUES($1::uuid,$2) RETURNING id::text`,
		organizationID, "Perm directory "+ws).Scan(&directoryID); err != nil {
		t.Fatal(err)
	}
	if err := st.Pool.QueryRow(ctx, `INSERT INTO groups(directory_id,name) VALUES($1::uuid,$2) RETURNING id::text`,
		directoryID, "perm-readers-"+strings.ToLower(store.NewID("g"))).Scan(&groupID); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO group_members(group_id,user_id) VALUES($1::uuid,$2)`, groupID, outsider)
	t.Cleanup(func() {
		for _, sql := range []string{
			`DELETE FROM wiki_space_permission_grants WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`,
			`DELETE FROM wiki_page_versions WHERE page_id IN (SELECT p.id FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id WHERE s.workspace_id=$1)`,
			`DELETE FROM wiki_pages WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`,
			`DELETE FROM wiki_spaces WHERE workspace_id=$1`,
			`DELETE FROM memberships WHERE workspace_id=$1`,
			`DELETE FROM workspaces WHERE id=$1`,
		} {
			exec(sql, ws)
		}
		exec(`DELETE FROM group_members WHERE group_id=$1::uuid`, groupID)
		exec(`DELETE FROM groups WHERE id=$1::uuid`, groupID)
		exec(`DELETE FROM directories WHERE id=$1::uuid`, directoryID)
		exec(`DELETE FROM organizations WHERE id=$1::uuid`, organizationID)
		for _, id := range []string{admin, member, outsider} {
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
	checks := func(user, contentID, operation, subjectType, subjectID string) bool {
		t.Helper()
		body := object(callV1(user, "POST", "/content/"+contentID+"/permission/check",
			map[string]any{"subject": map[string]any{"type": subjectType, "identifier": subjectID}, "operation": operation}, 200))
		allowed, _ := body["hasPermission"].(bool)
		return allowed
	}
	space := object(callV2(admin, "POST", "/spaces", map[string]any{"key": "SPT", "name": "Permissions"}, 201))
	spaceID, _ := space["id"].(string)
	page := object(callV2(admin, "POST", "/pages", map[string]any{"spaceId": spaceID, "title": "Guarded runbook",
		"status": "current", "body": models.WikiBody{Representation: "storage", Value: "<p>steps</p>"}}, 200))
	pageID, _ := page["id"].(string)

	// The catalogue reports what may be granted, and each entry that is not
	// the view permission says it needs it.
	catalogue := object(callV2(member, "GET", "/space-permissions", nil, 200))
	results, _ := catalogue["results"].([]any)
	if len(results) == 0 {
		t.Fatalf("space permission catalogue: %v", catalogue)
	}
	ids := map[string]map[string]any{}
	for _, raw := range results {
		entry, _ := raw.(map[string]any)
		ids[entry["id"].(string)] = entry
	}
	for _, required := range []string{"read/space", "create/page", "administer/space"} {
		if _, ok := ids[required]; !ok {
			t.Fatalf("%s missing from the catalogue: %v", required, catalogue)
		}
	}
	if needs, _ := ids["create/page"]["requiredPermissionIds"].([]any); len(needs) != 1 || needs[0] != "read/space" {
		t.Fatalf("create/page should require the view permission: %v", ids["create/page"])
	}
	if needs, _ := ids["read/space"]["requiredPermissionIds"].([]any); len(needs) != 0 {
		t.Fatalf("the view permission should require nothing: %v", ids["read/space"])
	}
	if mode := object(callV2(member, "GET", "/space-role-mode", nil, 200)); mode["mode"] != "ROLES" {
		t.Fatalf("space role mode: %v", mode)
	}

	// A space that says nothing about who may do what is open to its readers.
	if !checks(admin, pageID, "read", "user", member) {
		t.Fatal("a space with no grants and no roles should be open")
	}

	// Granting Confluence's view permission is what lets someone read the
	// content, even though this product names each read separately.
	granted := object(callV1(admin, "POST", "/space/SPT/permission", map[string]any{
		"subject": map[string]any{"type": "user", "identifier": member}, "operation": map[string]any{"key": "read", "target": "space"}}, 200))
	grantID := ""
	switch typed := granted["id"].(type) {
	case string:
		grantID = typed
	case float64:
		grantID = strconv.FormatInt(int64(typed), 10)
	}
	if grantID == "" {
		t.Fatalf("granted permission: %v", granted)
	}
	if !checks(admin, pageID, "read", "user", member) {
		t.Fatal("the view permission should let the subject read the content")
	}
	// The space is no longer open, so someone with no grant is refused.
	if checks(admin, pageID, "read", "user", admin) {
		t.Fatal("a space with grants should not be open to someone without one")
	}
	// View does not imply changing anything.
	if checks(admin, pageID, "update", "user", member) {
		t.Fatal("the view permission should not allow an update")
	}
	callV1(admin, "POST", "/space/SPT/permission", map[string]any{
		"subject": map[string]any{"type": "user", "identifier": member}, "operation": map[string]any{"key": "update", "target": "page"}}, 200)
	if !checks(admin, pageID, "update", "user", member) {
		t.Fatal("granting update should allow it")
	}

	// A group grant reaches its people.
	callV1(admin, "POST", "/space/SPT/permission", map[string]any{
		"subject": map[string]any{"type": "group", "identifier": groupID}, "operation": map[string]any{"key": "read", "target": "space"}}, 200)
	if !checks(admin, pageID, "read", "user", outsider) {
		t.Fatal("a group grant should reach its members")
	}
	if !checks(admin, pageID, "read", "group", groupID) {
		t.Fatal("asking about the group itself should say yes")
	}

	// Every refusal.
	callV1(admin, "POST", "/space/SPT/permission", map[string]any{
		"subject": map[string]any{"type": "user", "identifier": member}, "operation": map[string]any{"key": "fly", "target": "space"}}, 400)
	callV1(admin, "POST", "/space/SPT/permission", map[string]any{
		"subject": map[string]any{"type": "robot", "identifier": "x"}, "operation": map[string]any{"key": "read", "target": "space"}}, 400)
	callV1(admin, "POST", "/space/SPT/permission", map[string]any{
		"subject": map[string]any{"type": "user", "identifier": "usr_nobody"}, "operation": map[string]any{"key": "read", "target": "space"}}, 404)
	// Granting is space administration.
	callV1(member, "POST", "/space/SPT/permission", map[string]any{
		"subject": map[string]any{"type": "user", "identifier": member}, "operation": map[string]any{"key": "read", "target": "space"}}, 403)
	callV1(admin, "POST", "/content/"+pageID+"/permission/check", map[string]any{
		"subject": map[string]any{"type": "user", "identifier": member}}, 400)
	callV1(admin, "POST", "/content/9999999/permission/check", map[string]any{
		"subject": map[string]any{"type": "user", "identifier": member}, "operation": "read"}, 404)

	// An app grants several custom content operations at once, and the ones it
	// said it did not want are not granted.
	callV1(admin, "POST", "/space/SPT/permission/custom-content", map[string]any{
		"subject": map[string]any{"type": "user", "identifier": member},
		"operations": []any{
			map[string]any{"key": "create", "target": "custom", "access": true},
			map[string]any{"key": "delete", "target": "custom", "access": false},
		}}, 204)
	var customGrants int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM wiki_space_permission_grants g
		JOIN wiki_spaces s ON s.id=g.space_id
		WHERE s.workspace_id=$1 AND g.permission LIKE '%/custom'`, ws).Scan(&customGrants); err != nil {
		t.Fatal(err)
	}
	if customGrants != 1 {
		t.Fatalf("an operation marked access:false was granted anyway: %d", customGrants)
	}

	// Removing the view permission takes the subject's other permissions with
	// it, because a permission that cannot be reached is not a permission.
	callV1(member, "DELETE", "/space/SPT/permission/"+grantID, nil, 403)
	callV1(admin, "DELETE", "/space/SPT/permission/"+grantID, nil, 204)
	var remaining int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM wiki_space_permission_grants g
		JOIN wiki_spaces s ON s.id=g.space_id
		WHERE s.workspace_id=$1 AND g.subject_type='user' AND g.subject_id=$2`, ws, member).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("removing the view permission left %d others behind", remaining)
	}
	if checks(admin, pageID, "read", "user", member) {
		t.Fatal("the subject should no longer be able to read the content")
	}
	callV1(admin, "DELETE", "/space/SPT/permission/"+grantID, nil, 404)
}
