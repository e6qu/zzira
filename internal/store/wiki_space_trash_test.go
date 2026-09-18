package store

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/e6qu/zzira/internal/models"
)

// A Confluence space that is deleted in the browser goes to the trash: its
// administrator may send it there, it leaves the directory and search with its
// content kept, and only a site administrator can bring it back or remove it
// for good.
func TestWikiSpaceTrashRestoreAndPurge(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	st, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if err := Migrate(ctx, st.Pool); err != nil {
		t.Fatal(err)
	}
	ws, siteAdmin, spaceAdmin, reader := NewID("ws"), NewID("usr"), NewID("usr"), NewID("usr")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Space trash test')`, ws)
	for _, person := range []struct{ id, role string }{{siteAdmin, "admin"}, {spaceAdmin, "member"}, {reader, "member"}} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Trash user')`, person.id, person.id+"@example.invalid")
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,$3)`, ws, person.id, person.role)
	}
	t.Cleanup(func() {
		for _, query := range []string{
			`DELETE FROM api_tasks WHERE workspace_id=$1`,
			`DELETE FROM actions WHERE workspace_id=$1`,
			`DELETE FROM wiki_space_role_assignments WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`,
			`DELETE FROM wiki_page_versions WHERE page_id IN (SELECT p.id FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id WHERE s.workspace_id=$1)`,
			`UPDATE wiki_spaces SET homepage_id=NULL WHERE workspace_id=$1`,
			`DELETE FROM wiki_pages WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`,
			`DELETE FROM wiki_spaces WHERE workspace_id=$1`,
			`DELETE FROM memberships WHERE workspace_id=$1`,
			`DELETE FROM workspaces WHERE id=$1`,
		} {
			exec(query, ws)
		}
		exec(`DELETE FROM users WHERE id IN ($1,$2,$3)`, siteAdmin, spaceAdmin, reader)
	})

	space, err := st.CreateWikiSpaceFull(ctx, ws, siteAdmin, CreateWikiSpaceInput{Key: "TRASH", Name: "Trash space"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetWikiSpaceRoleAssignments(ctx, ws, siteAdmin, space.ID, []models.WikiSpaceRoleAssignment{
		{RoleID: "system-admin", PrincipalType: "USER", PrincipalID: spaceAdmin},
		{RoleID: "system-member", PrincipalType: "USER", PrincipalID: reader},
	}); err != nil {
		t.Fatal(err)
	}
	page, err := st.SaveWikiPage(ctx, ws, spaceAdmin, models.WikiPage{
		SpaceID: space.ID, Title: "Retention schedule", Status: "current",
		Body: models.WikiBody{Value: "<p>Keep the retention schedule.</p>", Representation: "storage"},
	})
	if err != nil {
		t.Fatal(err)
	}

	findable := func(actor string) bool {
		t.Helper()
		results, _, searchErr := st.SearchWiki(ctx, ws, actor, WikiSearchRequest{
			CQL: `text ~ "retention"`, EntityTypes: WikiSearchContentTypes, IncludeArchivedSpaces: true, Limit: 25,
		})
		if searchErr != nil {
			t.Fatal(searchErr)
		}
		for _, result := range results {
			if result.ID == page.ID {
				return true
			}
		}
		return false
	}
	if !findable(reader) {
		t.Fatal("the page is not findable before the space is trashed")
	}

	// Sending a space to the trash is the space administrator's decision.
	if _, err := st.TrashWikiSpace(ctx, ws, reader, space.Key); !errors.Is(err, ErrProjectPermission) {
		t.Fatalf("a reader trashing the space = %v, want a permission error", err)
	}
	trashed, err := st.TrashWikiSpace(ctx, ws, spaceAdmin, space.Key)
	if err != nil {
		t.Fatal(err)
	}
	if trashed.Status != "trashed" {
		t.Fatalf("status after trashing = %q, want trashed", trashed.Status)
	}
	if _, err := st.TrashWikiSpace(ctx, ws, spaceAdmin, space.Key); !errors.Is(err, ErrWikiValidation) {
		t.Fatalf("trashing twice = %v, want a validation error", err)
	}

	// The space leaves the directory and search, and its content is kept.
	current, err := st.WikiSpacesWithStatus(ctx, ws, reader, "current")
	if err != nil {
		t.Fatal(err)
	}
	for _, listed := range current {
		if listed.ID == space.ID {
			t.Fatal("a trashed space is still listed among the current spaces")
		}
	}
	if findable(reader) {
		t.Fatal("a page in a trashed space is still findable")
	}
	kept, err := st.WikiPages(ctx, ws, spaceAdmin, space.ID, "current", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 1 || kept[0].ID != page.ID {
		t.Fatalf("pages kept in the trashed space = %+v", kept)
	}

	// The trash itself is a site administrator's list.
	if _, err := st.WikiSpacesWithStatus(ctx, ws, spaceAdmin, "trashed"); !errors.Is(err, ErrProjectPermission) {
		t.Fatalf("a space administrator reading the trash = %v, want a permission error", err)
	}
	inTrash, err := st.WikiSpacesWithStatus(ctx, ws, siteAdmin, "trashed")
	if err != nil {
		t.Fatal(err)
	}
	if len(inTrash) != 1 || inTrash[0].ID != space.ID {
		t.Fatalf("the trash holds %+v, want the one space", inTrash)
	}

	// Only a site administrator restores a space or removes it for good.
	if _, err := st.RestoreWikiSpace(ctx, ws, spaceAdmin, space.Key); !errors.Is(err, ErrProjectPermission) {
		t.Fatalf("a space administrator restoring = %v, want a permission error", err)
	}
	if _, err := st.PurgeWikiSpace(ctx, ws, spaceAdmin, space.Key); !errors.Is(err, ErrProjectPermission) {
		t.Fatalf("a space administrator purging = %v, want a permission error", err)
	}
	restored, err := st.RestoreWikiSpace(ctx, ws, siteAdmin, space.Key)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Status != "current" {
		t.Fatalf("status after restoring = %q, want current", restored.Status)
	}
	if !findable(reader) {
		t.Fatal("a restored space's page is not findable again")
	}
	if _, err := st.PurgeWikiSpace(ctx, ws, siteAdmin, space.Key); !errors.Is(err, ErrWikiValidation) {
		t.Fatalf("purging a space that is not in the trash = %v, want a validation error", err)
	}

	// Permanently deleting a trashed space takes it and its content away.
	if _, err := st.TrashWikiSpace(ctx, ws, siteAdmin, space.Key); err != nil {
		t.Fatal(err)
	}
	if _, err := st.PurgeWikiSpace(ctx, ws, siteAdmin, space.Key); err != nil {
		t.Fatal(err)
	}
	if err := (&APITaskRunner{Store: st}).DrainOnce(ctx, ws); err != nil {
		t.Fatal(err)
	}
	if _, err := st.WikiSpaceByKeyForAdmin(ctx, ws, siteAdmin, space.Key); err == nil {
		t.Fatal("the purged space is still there")
	}
}
