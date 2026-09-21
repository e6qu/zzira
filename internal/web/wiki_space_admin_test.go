package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/authn"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
)

// wikiSpaceAdminFixture is a workspace with an administrator, a plain member
// and one space, which is what every space administration journey needs.
type wikiSpaceAdminFixture struct {
	store       *store.Store
	handler     *Handler
	workspaceID string
	adminID     string
	memberID    string
	spaceID     string
	spaceKey    string
}

func newWikiSpaceAdminFixture(t *testing.T) *wikiSpaceAdminFixture {
	t.Helper()
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
	workspaceID, adminID, memberID := store.NewID("ws"), store.NewID("usr"), store.NewID("usr")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Space admin test')`, workspaceID)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Space owner')`, adminID, adminID+"@example.invalid")
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Space member')`, memberID, memberID+"@example.invalid")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'admin')`, workspaceID, adminID)
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'member')`, workspaceID, memberID)
	t.Cleanup(func() {
		exec(`DELETE FROM sessions WHERE user_id IN ($1,$2)`, adminID, memberID)
		exec(`DELETE FROM wiki_space_role_assignments WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`, workspaceID)
		exec(`DELETE FROM wiki_space_permission_grants WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`, workspaceID)
		exec(`DELETE FROM wiki_space_roles WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM wiki_pages WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`, workspaceID)
		exec(`DELETE FROM wiki_spaces WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		exec(`DELETE FROM users WHERE id IN ($1,$2)`, adminID, memberID)
	})
	key := "SA" + strings.ToUpper(workspaceID[len(workspaceID)-5:])
	space, err := st.CreateWikiSpace(ctx, workspaceID, adminID, key, "Space before", "Description before", false)
	if err != nil {
		t.Fatal(err)
	}
	return &wikiSpaceAdminFixture{
		store:       st,
		handler:     &Handler{Store: st, Commands: &commands.Service{Store: st}, WorkspaceSlug: workspaceID},
		workspaceID: workspaceID,
		adminID:     adminID,
		memberID:    memberID,
		spaceID:     space.ID,
		spaceKey:    space.Key,
	}
}

// post signs the user in and calls one of the space administration handlers.
func (f *wikiSpaceAdminFixture) post(t *testing.T, handler func(http.ResponseWriter, *http.Request), userID, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	token, _, err := authn.LoginOIDC(context.Background(), f.store, userID, "id-token", "https://issuer.example.invalid", userID+"-subject", "")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(&http.Cookie{Name: "zzira_session", Value: token})
	request.SetPathValue("space", f.spaceID)
	response := httptest.NewRecorder()
	handler(response, request)
	return response
}

// A space administrator renames a space and rewrites its description from the
// browser; Confluence's space details form is not API-only.
func TestSpaceDetailsAreEditedInTheBrowser(t *testing.T) {
	f := newWikiSpaceAdminFixture(t)
	ctx := context.Background()
	form := url.Values{"name": {"Space after"}, "description": {"Description after"}}
	if response := f.post(t, f.handler.WikiSpaceDetails, f.adminID, "/wiki/spaces/"+f.spaceID+"/details", form); response.Code != http.StatusSeeOther {
		t.Fatalf("rename by the space administrator = %d, want 303: %s", response.Code, response.Body.String())
	}
	space, err := f.store.WikiSpace(ctx, f.workspaceID, f.adminID, f.spaceID)
	if err != nil {
		t.Fatal(err)
	}
	if space.Name != "Space after" || space.Description != "Description after" {
		t.Fatalf("space details = %q / %q, want the edited pair", space.Name, space.Description)
	}

	denied := f.post(t, f.handler.WikiSpaceDetails, f.memberID, "/wiki/spaces/"+f.spaceID+"/details",
		url.Values{"name": {"Member rename"}, "description": {"Member description"}})
	if denied.Code != http.StatusForbidden {
		t.Fatalf("rename by a plain member = %d, want 403", denied.Code)
	}
	space, err = f.store.WikiSpace(ctx, f.workspaceID, f.adminID, f.spaceID)
	if err != nil {
		t.Fatal(err)
	}
	if space.Name != "Space after" {
		t.Fatalf("a denied rename still changed the space to %q", space.Name)
	}
}

// An empty name is refused, and the space keeps the name it had: the store's
// rule reaches the browser rather than being lost in an empty form field.
func TestSpaceDetailsRefuseAnEmptyName(t *testing.T) {
	f := newWikiSpaceAdminFixture(t)
	response := f.post(t, f.handler.WikiSpaceDetails, f.adminID, "/wiki/spaces/"+f.spaceID+"/details",
		url.Values{"name": {"  "}, "description": {"Description after"}})
	if response.Code != http.StatusBadRequest {
		t.Fatalf("empty rename = %d, want 400: %s", response.Code, response.Body.String())
	}
	space, err := f.store.WikiSpace(context.Background(), f.workspaceID, f.adminID, f.spaceID)
	if err != nil {
		t.Fatal(err)
	}
	if space.Name != "Space before" {
		t.Fatalf("space name = %q, want it unchanged", space.Name)
	}
}

// Custom space roles are created, edited and deleted from the browser.
func TestCustomSpaceRolesAreEditedAndDeletedInTheBrowser(t *testing.T) {
	f := newWikiSpaceAdminFixture(t)
	ctx := context.Background()
	path := "/wiki/spaces/" + f.spaceID + "/roles"
	create := url.Values{"action": {"create"}, "name": {"Reviewers"}, "description": {"Read and comment"},
		"permissions": {"read/space", "read/page"}}
	if response := f.post(t, f.handler.WikiSpaceRoleSettings, f.adminID, path, create); response.Code != http.StatusSeeOther {
		t.Fatalf("create = %d, want 303: %s", response.Code, response.Body.String())
	}
	roleID := ""
	roles, err := f.store.WikiSpaceRoles(ctx, f.workspaceID, f.adminID)
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range roles {
		if role.Name == "Reviewers" {
			roleID = role.ID
		}
	}
	if roleID == "" {
		t.Fatal("the created role is not in the catalogue")
	}

	update := url.Values{"action": {"update"}, "roleId": {roleID}, "name": {"Reviewers and editors"},
		"description": {"Read, comment and edit"}, "permissions": {"read/space", "read/page", "update/page"}}
	if response := f.post(t, f.handler.WikiSpaceRoleSettings, f.adminID, path, update); response.Code != http.StatusSeeOther {
		t.Fatalf("update = %d, want 303: %s", response.Code, response.Body.String())
	}
	roles, err = f.store.WikiSpaceRoles(ctx, f.workspaceID, f.adminID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, role := range roles {
		if role.ID != roleID {
			continue
		}
		found = true
		if role.Name != "Reviewers and editors" || len(role.SpacePermissions) != 3 {
			t.Fatalf("edited role = %q with %v", role.Name, role.SpacePermissions)
		}
	}
	if !found {
		t.Fatal("the edited role disappeared")
	}

	// A system role is Confluence's own and cannot be edited or deleted.
	system := url.Values{"action": {"delete"}, "roleId": {"system-admin"}}
	if response := f.post(t, f.handler.WikiSpaceRoleSettings, f.adminID, path, system); response.Code != http.StatusBadRequest {
		t.Fatalf("deleting a system role = %d, want 400", response.Code)
	}

	remove := url.Values{"action": {"delete"}, "roleId": {roleID}}
	if response := f.post(t, f.handler.WikiSpaceRoleSettings, f.adminID, path, remove); response.Code != http.StatusSeeOther {
		t.Fatalf("delete = %d, want 303: %s", response.Code, response.Body.String())
	}
	roles, err = f.store.WikiSpaceRoles(ctx, f.workspaceID, f.adminID)
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range roles {
		if role.ID == roleID {
			t.Fatal("the deleted role is still in the catalogue")
		}
	}
}

// Direct grants -- one subject, one permission -- are added and removed from
// the browser, beside the roles they complement.
func TestDirectSpaceGrantsAreManagedInTheBrowser(t *testing.T) {
	f := newWikiSpaceAdminFixture(t)
	ctx := context.Background()
	path := "/wiki/spaces/" + f.spaceID + "/permissions"
	add := url.Values{"action": {"add"}, "subjectType": {"user"}, "subjectId": {f.memberID}, "permission": {"read/space"}}
	if response := f.post(t, f.handler.WikiSpacePermissionGrants, f.adminID, path, add); response.Code != http.StatusSeeOther {
		t.Fatalf("add = %d, want 303: %s", response.Code, response.Body.String())
	}
	// The space had no roles and no grants, so it was open to every member.
	// The first grant ends that, and the administrator writing it keeps the
	// space: theirs is recorded beside the one they asked for.
	grants, err := f.store.WikiSpacePermissionGrants(ctx, f.workspaceID, f.adminID, f.spaceKey)
	if err != nil {
		t.Fatal(err)
	}
	held := map[string]string{}
	for _, grant := range grants {
		held[grant.SubjectID] = grant.Permission
	}
	if len(grants) != 2 || held[f.memberID] != "read/space" || held[f.adminID] != "administer/space" {
		t.Fatalf("grants after the first add = %+v", grants)
	}
	// The space page still opens for them, which is the point of that second
	// grant: the visibility gate now names who may read this space.
	if _, err = f.store.WikiSpace(ctx, f.workspaceID, f.adminID, f.spaceID); err != nil {
		t.Fatalf("the granting administrator lost the space page: %v", err)
	}

	denied := f.post(t, f.handler.WikiSpacePermissionGrants, f.memberID, path,
		url.Values{"action": {"add"}, "subjectType": {"user"}, "subjectId": {f.memberID}, "permission": {"administer/space"}})
	if denied.Code != http.StatusForbidden {
		t.Fatalf("a member granting themselves administration = %d, want 403", denied.Code)
	}

	memberGrant := ""
	for _, grant := range grants {
		if grant.SubjectID == f.memberID {
			memberGrant = grant.ID
		}
	}
	remove := url.Values{"action": {"remove"}, "grantId": {memberGrant}}
	if response := f.post(t, f.handler.WikiSpacePermissionGrants, f.adminID, path, remove); response.Code != http.StatusSeeOther {
		t.Fatalf("remove = %d, want 303: %s", response.Code, response.Body.String())
	}
	grants, err = f.store.WikiSpacePermissionGrants(ctx, f.workspaceID, f.adminID, f.spaceKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(grants) != 1 || grants[0].SubjectID != f.adminID {
		t.Fatalf("grants after remove = %+v", grants)
	}
}
