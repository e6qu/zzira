package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/authn"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

// The browser carries Confluence's whole space lifecycle: a space
// administrator archives a space or deletes it to the trash, the directory
// keeps current, archived and trashed spaces in lists of their own, and only a
// site administrator restores a trashed space or deletes it permanently.
func TestWikiSpaceTrashJourney(t *testing.T) {
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
	ws, siteAdmin, spaceAdmin := store.NewID("ws"), store.NewID("usr"), store.NewID("usr")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Space trash journey')`, ws)
	for _, person := range []struct{ id, role string }{{siteAdmin, "admin"}, {spaceAdmin, "member"}} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Journey user')`, person.id, person.id+"@example.invalid")
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,$3)`, ws, person.id, person.role)
	}
	t.Cleanup(func() {
		exec(`DELETE FROM sessions WHERE user_id IN ($1,$2)`, siteAdmin, spaceAdmin)
		for _, query := range []string{
			`DELETE FROM api_tasks WHERE workspace_id=$1`,
			`DELETE FROM actions WHERE workspace_id=$1`,
			`DELETE FROM wiki_space_role_assignments WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`,
			`UPDATE wiki_spaces SET homepage_id=NULL WHERE workspace_id=$1`,
			`DELETE FROM wiki_spaces WHERE workspace_id=$1`,
			`DELETE FROM memberships WHERE workspace_id=$1`,
			`DELETE FROM workspaces WHERE id=$1`,
		} {
			exec(query, ws)
		}
		exec(`DELETE FROM users WHERE id IN ($1,$2)`, siteAdmin, spaceAdmin)
	})

	space, err := st.CreateWikiSpaceFull(ctx, ws, siteAdmin, store.CreateWikiSpaceInput{Key: "JOURNEY", Name: "Journey space"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetWikiSpaceRoleAssignments(ctx, ws, siteAdmin, space.ID, []models.WikiSpaceRoleAssignment{
		{RoleID: "system-admin", PrincipalType: "USER", PrincipalID: spaceAdmin},
	}); err != nil {
		t.Fatal(err)
	}

	h := &Handler{Store: st, Commands: &commands.Service{Store: st}, WorkspaceSlug: ws}
	tokens := map[string]string{}
	for _, person := range []string{siteAdmin, spaceAdmin} {
		token, loginErr := authn.LoginOIDC(ctx, st, person, "id-token", "https://issuer.example.invalid", person+"-subject", "")
		if loginErr != nil {
			t.Fatal(loginErr)
		}
		tokens[person] = token
	}
	call := func(handler http.HandlerFunc, method, target, actor string, body string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, target, strings.NewReader(body))
		if body != "" {
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		request.SetPathValue("space", space.ID)
		request.AddCookie(&http.Cookie{Name: "zzira_session", Value: tokens[actor]})
		response := httptest.NewRecorder()
		handler(response, request)
		return response
	}

	// The space page offers the delete, and it says where the space goes.
	page := call(h.WikiSpacePage, http.MethodGet, "/wiki/spaces/"+space.ID, spaceAdmin, "")
	if page.Code != http.StatusOK {
		t.Fatalf("space page = %d: %s", page.Code, page.Body.String())
	}
	if !strings.Contains(page.Body.String(), `action="/wiki/spaces/`+space.ID+`/trash"`) ||
		!strings.Contains(page.Body.String(), "Delete space") {
		t.Fatal("the space page offers no delete")
	}

	// Deleting sends the space to the trash, and it leaves the directory.
	if code := call(h.WikiSpaceTrash, http.MethodPost, "/wiki/spaces/"+space.ID+"/trash", spaceAdmin, "").Code; code != http.StatusSeeOther {
		t.Fatalf("a space administrator deleting = %d, want 303", code)
	}
	directory := call(h.WikiHome, http.MethodGet, "/wiki", spaceAdmin, "")
	if directory.Code != http.StatusOK || strings.Contains(directory.Body.String(), "Journey space") {
		t.Fatalf("the directory still shows the trashed space: %d", directory.Code)
	}
	if code := call(h.WikiHome, http.MethodGet, "/wiki?status=trashed", spaceAdmin, "").Code; code != http.StatusForbidden {
		t.Fatalf("a space administrator reading the trash = %d, want 403", code)
	}
	trash := call(h.WikiHome, http.MethodGet, "/wiki?status=trashed", siteAdmin, "")
	if trash.Code != http.StatusOK || !strings.Contains(trash.Body.String(), "Journey space") ||
		!strings.Contains(trash.Body.String(), `action="/wiki/spaces/`+space.ID+`/purge"`) {
		t.Fatalf("the trash list = %d: %s", trash.Code, trash.Body.String())
	}

	// Archiving does not reach into the trash, and only a site administrator
	// takes a space back out of it — including this space, which no role
	// assignment lets them read.
	if code := call(h.WikiSpaceStatus, http.MethodPost, "/wiki/spaces/"+space.ID+"/status", spaceAdmin, "status=archived").Code; code != http.StatusBadRequest {
		t.Fatalf("archiving a trashed space = %d, want 400", code)
	}
	if code := call(h.WikiSpaceRestore, http.MethodPost, "/wiki/spaces/"+space.ID+"/restore", spaceAdmin, "").Code; code != http.StatusForbidden {
		t.Fatalf("a space administrator restoring = %d, want 403", code)
	}
	if code := call(h.WikiSpacePurge, http.MethodPost, "/wiki/spaces/"+space.ID+"/purge", spaceAdmin, "").Code; code != http.StatusForbidden {
		t.Fatalf("a space administrator purging = %d, want 403", code)
	}
	if code := call(h.WikiSpaceRestore, http.MethodPost, "/wiki/spaces/"+space.ID+"/restore", siteAdmin, "").Code; code != http.StatusSeeOther {
		t.Fatalf("a site administrator restoring = %d, want 303", code)
	}

	// Archiving keeps the space, out of the general list and in its own.
	if code := call(h.WikiSpaceStatus, http.MethodPost, "/wiki/spaces/"+space.ID+"/status", spaceAdmin, "status=archived").Code; code != http.StatusSeeOther {
		t.Fatalf("archiving a restored space = %d, want 303", code)
	}
	current := call(h.WikiHome, http.MethodGet, "/wiki", spaceAdmin, "")
	if current.Code != http.StatusOK || strings.Contains(current.Body.String(), "Journey space") {
		t.Fatalf("the general list still shows the archived space: %d", current.Code)
	}
	archived := call(h.WikiHome, http.MethodGet, "/wiki?status=archived", spaceAdmin, "")
	if archived.Code != http.StatusOK || !strings.Contains(archived.Body.String(), "Journey space") {
		t.Fatalf("the archived list = %d: %s", archived.Code, archived.Body.String())
	}

	// Deleting permanently is the site administrator's, and the task takes the
	// space away.
	if code := call(h.WikiSpaceTrash, http.MethodPost, "/wiki/spaces/"+space.ID+"/trash", spaceAdmin, "").Code; code != http.StatusSeeOther {
		t.Fatalf("deleting the archived space = %d, want 303", code)
	}
	if code := call(h.WikiSpacePurge, http.MethodPost, "/wiki/spaces/"+space.ID+"/purge", siteAdmin, "").Code; code != http.StatusSeeOther {
		t.Fatalf("a site administrator purging = %d, want 303", code)
	}
	if err := (&store.APITaskRunner{Store: st, BulkIssueExecutor: h.Commands}).DrainOnce(ctx, ws); err != nil {
		t.Fatal(err)
	}
	if _, err := st.WikiSpaceByKeyForAdmin(ctx, ws, siteAdmin, space.Key); err == nil {
		t.Fatal("the purged space is still there")
	}
}
