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

// TestGroupAccessTypesAndGuests pins groups listed by the access they give,
// guests found only when a user search asks for them, and the expansions of a
// user: the site permissions they hold and their personal space.
func TestGroupAccessTypesAndGuests(t *testing.T) {
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
	stamp := store.NewID("x")
	ws, admin, member, guest := store.NewID("ws"), store.NewID("usr"), store.NewID("usr"), store.NewID("usr")
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES ($1,$1,'Groups test')`, ws)
	for _, user := range []string{admin, member, guest} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES ($1,$2,'test',$3)`, user, user+"@example.test", "Person "+stamp+" "+user)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES ($1,$1,$2)`, user, store.HashToken(user))
	}
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES ($1,$2,'admin'),($1,$3,'member'),($1,$4,'member')`, ws, admin, member, guest)
	exec(`INSERT INTO directory_users(directory_id,user_id) SELECT d.id,u FROM directories d JOIN sites si ON si.organization_id=d.organization_id AND si.workspace_id=$1, unnest($2::text[]) u ON CONFLICT DO NOTHING`, ws, []string{admin, member, guest})
	t.Cleanup(func() {
		for _, sql := range []string{
			`DELETE FROM role_bindings WHERE principal_id IN (SELECT g.id::text FROM groups g JOIN directories d ON d.id=g.directory_id JOIN sites si ON si.organization_id=d.organization_id WHERE si.workspace_id=$1)`,
			`DELETE FROM group_members WHERE group_id IN (SELECT g.id FROM groups g JOIN directories d ON d.id=g.directory_id JOIN sites si ON si.organization_id=d.organization_id WHERE si.workspace_id=$1)`,
			`DELETE FROM groups WHERE directory_id IN (SELECT d.id FROM directories d JOIN sites si ON si.organization_id=d.organization_id WHERE si.workspace_id=$1)`,
			`DELETE FROM wiki_spaces WHERE workspace_id=$1`,
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
		request := httptest.NewRequest(method, path, strings.NewReader(string(raw)))
		request.SetBasicAuth(user+"@example.test", user)
		request.Header.Set("X-Atlassian-Token", "no-check")
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
	names := func(body map[string]any) []string {
		list, _ := body["results"].([]any)
		out := []string{}
		for _, item := range list {
			out = append(out, item.(map[string]any)["name"].(string))
		}
		return out
	}
	var siteID, organizationID, confluenceID string
	if err := st.Pool.QueryRow(ctx, `SELECT si.id::text,si.organization_id::text,p.id::text FROM sites si JOIN products p ON p.site_id=si.id AND p.product_key='confluence' WHERE si.workspace_id=$1`, ws).Scan(&siteID, &organizationID, &confluenceID); err != nil {
		t.Fatal(err)
	}
	group := func(name string) string {
		t.Helper()
		created, err := st.CreateWikiGroup(ctx, ws, admin, name)
		if err != nil {
			t.Fatal(err)
		}
		return created.ID
	}
	users, admins, siteAdmins, guests := group("confluence-users-"+stamp), group("confluence-admins-"+stamp), group("site-admins-"+stamp), group("guests-"+stamp)
	group("unassigned-" + stamp)
	bind := func(role, scopeType, scopeID, principalType, principalID string) {
		exec(`INSERT INTO role_bindings(scope_type,scope_id,role_key,principal_type,principal_id,source) VALUES($1,$2,$3,$4,$5,'manual')`, scopeType, scopeID, role, principalType, principalID)
	}
	bind("atlassian/product-user", "product", confluenceID, "group", users)
	bind("atlassian/product-admin", "product", confluenceID, "group", admins)
	bind("atlassian/site-admin", "site", siteID, "group", siteAdmins)
	bind("atlassian/guest", "product", confluenceID, "group", guests)
	if err := st.SetWikiGroupMembership(ctx, ws, admin, guests, guest, true); err != nil {
		t.Fatal(err)
	}
	if err := st.SetWikiGroupMembership(ctx, ws, admin, users, member, true); err != nil {
		t.Fatal(err)
	}

	// Groups are listed by the access they give.
	for accessType, want := range map[string]string{"user": "confluence-users-" + stamp, "admin": "confluence-admins-" + stamp, "site-admin": "site-admins-" + stamp} {
		if got := names(call(member, "GET", "/wiki/rest/api/group?accessType="+accessType, nil, 200)); len(got) != 1 || got[0] != want {
			t.Errorf("accessType=%s: %v", accessType, got)
		}
	}
	if all := names(call(member, "GET", "/wiki/rest/api/group", nil, 200)); len(all) != 5 {
		t.Errorf("all groups: %v", all)
	}
	call(member, "GET", "/wiki/rest/api/group?accessType=owner", nil, 400)

	// A user search finds licensed users unless it asks for guests.
	search := func(filter string) map[string]bool {
		t.Helper()
		path := `/wiki/rest/api/search/user?cql=user.fullname~"` + stamp + `"`
		if filter != "" {
			path += "&sitePermissionTypeFilter=" + filter
		}
		body := call(member, "GET", path, nil, 200)
		found := map[string]bool{}
		for _, item := range body["results"].([]any) {
			user := item.(map[string]any)["user"].(map[string]any)
			found[user["accountId"].(string)] = user["isExternalCollaborator"].(bool)
		}
		return found
	}
	if found := search(""); len(found) != 2 || found[guest] {
		t.Errorf("licensed users: %v", found)
	}
	if found := search("externalCollaborator"); len(found) != 1 || !found[guest] {
		t.Errorf("guests: %v", found)
	}
	if found := search("all"); len(found) != 3 {
		t.Errorf("everyone: %v", found)
	}
	call(member, "GET", `/wiki/rest/api/search/user?cql=user.fullname~"x"&sitePermissionTypeFilter=licensed`, nil, 400)
	if paged := call(member, "GET", `/wiki/rest/api/search/user?cql=user.fullname~"`+stamp+`"&sitePermissionTypeFilter=all&limit=1&start=1`, nil, 200); paged["size"] != float64(1) || paged["totalSize"] != float64(3) {
		t.Errorf("paged search: %v", paged)
	}

	// Group members expand to their permissions and personal space.
	if _, err := st.CreateWikiSpaceFull(ctx, ws, admin, store.CreateWikiSpaceInput{Name: "Member's space", Personal: true, OwnerID: member}); err != nil {
		t.Fatal(err)
	}
	expanded := call(admin, "GET", "/wiki/rest/api/group/"+users+"/membersByGroupId?expand=operations,personalSpace", nil, 200)
	people := expanded["results"].([]any)
	if len(people) != 1 {
		t.Fatalf("members: %v", expanded)
	}
	person := people[0].(map[string]any)
	if space, _ := person["personalSpace"].(map[string]any); space == nil || space["key"] != store.PersonalSpaceKey(member) {
		t.Errorf("personal space: %v", person)
	}
	if ops := mustJSON(t, person["operations"]); !strings.Contains(ops, `"operation":"use"`) || strings.Contains(ops, `"administer"`) {
		t.Errorf("member operations: %s", ops)
	}
	if plain := call(admin, "GET", "/wiki/rest/api/group/"+users+"/membersByGroupId", nil, 200)["results"].([]any)[0].(map[string]any); plain["_expandable"] == nil || plain["operations"] != nil {
		t.Errorf("unexpanded member: %v", plain)
	}
	call(admin, "GET", "/wiki/rest/api/group/"+users+"/membersByGroupId?expand=groups", nil, 400)

	// The v2 bulk read takes at most 250 ids.
	many := make([]string, 251)
	for i := range many {
		many[i] = member
	}
	call(member, "POST", "/wiki/api/v2/users-bulk", map[string]any{"accountIds": many}, 400)
}
