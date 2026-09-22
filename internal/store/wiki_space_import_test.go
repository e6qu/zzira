package store

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/models"
)

// A space export carries a manifest, and importing one makes the space again:
// the same pages, in the same tree, with the same words.
func TestASpaceExportCanBeReadBackIn(t *testing.T) {
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
	workspaceID, _, err := st.DefaultWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	actorID, err := st.FirstAdminID(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	key := "EXP" + strings.ToUpper(NewID("s")[len(NewID("s"))-5:])
	space, err := st.CreateWikiSpaceFull(ctx, workspaceID, actorID, CreateWikiSpaceInput{
		Key: key, Name: "Export source " + key, Description: "What travels",
	})
	if err != nil {
		t.Fatalf("create the space: %v", err)
	}
	t.Cleanup(func() { _, _ = st.Pool.Exec(ctx, `DELETE FROM wiki_spaces WHERE id=$1::bigint`, space.ID) })
	parent, err := st.SaveWikiPage(ctx, workspaceID, actorID, models.WikiPage{
		SpaceID: space.ID, Title: "Runbooks", Status: "current",
		Body: models.WikiBody{Representation: "storage", Value: "<p>How we keep it up.</p>"},
	})
	if err != nil {
		t.Fatalf("write the parent page: %v", err)
	}
	if _, err := st.SaveWikiPage(ctx, workspaceID, actorID, models.WikiPage{
		SpaceID: space.ID, Title: "Restarting the queue", Status: "current", ParentID: parent.ID,
		Body: models.WikiBody{Representation: "storage", Value: "<p>Drain, restart, watch.</p>"},
	}); err != nil {
		t.Fatalf("write the child page: %v", err)
	}
	if _, err := st.SaveWikiBlogPost(ctx, workspaceID, actorID, models.WikiBlogPost{
		SpaceID: space.ID, Title: "What we learned", Status: "current",
		Body: models.WikiBody{Representation: "storage", Value: "<p>Write it down.</p>"},
	}); err != nil {
		t.Fatalf("write the blog post: %v", err)
	}

	pages, err := st.WikiPages(ctx, workspaceID, actorID, space.ID, "current", "")
	if err != nil {
		t.Fatal(err)
	}
	posts, err := st.WikiBlogPosts(ctx, workspaceID, actorID, space.ID, "current", "", "created-date")
	if err != nil {
		t.Fatal(err)
	}
	archive, err := buildWikiSpaceExport(space, pages, posts, nil)
	if err != nil {
		t.Fatalf("build the export: %v", err)
	}

	// Something that is not an export says so rather than making a space.
	if _, err := st.ImportWikiSpace(ctx, workspaceID, actorID, "NOTAZIP", "Not a zip", []byte("hello")); err == nil ||
		!strings.Contains(err.Error(), "not a space export") {
		t.Fatalf("importing a file that is not an export = %v", err)
	}

	copyKey := "CPY" + strings.ToUpper(NewID("s")[len(NewID("s"))-5:])
	result, err := st.ImportWikiSpace(ctx, workspaceID, actorID, copyKey, "Imported "+copyKey, archive)
	if err != nil {
		t.Fatalf("import the export: %v", err)
	}
	t.Cleanup(func() { _, _ = st.Pool.Exec(ctx, `DELETE FROM wiki_spaces WHERE id=$1::bigint`, result.Space.ID) })
	if result.Pages != 2 || result.BlogPosts != 1 {
		t.Fatalf("the import made %d pages and %d blog posts", result.Pages, result.BlogPosts)
	}
	imported, err := st.WikiPages(ctx, workspaceID, actorID, result.Space.ID, "current", "")
	if err != nil {
		t.Fatal(err)
	}
	byTitle := map[string]*models.WikiPage{}
	for _, page := range imported {
		byTitle[page.Title] = page
	}
	if byTitle["Runbooks"] == nil || byTitle["Restarting the queue"] == nil {
		t.Fatalf("the imported space holds %d pages: %+v", len(imported), byTitle)
	}
	if byTitle["Restarting the queue"].ParentID != byTitle["Runbooks"].ID {
		t.Fatal("the page tree did not come back: the child is not under its parent")
	}
	if !strings.Contains(byTitle["Restarting the queue"].Body.Value, "Drain, restart, watch") {
		t.Fatalf("the page came back as %q", byTitle["Restarting the queue"].Body.Value)
	}
}
