package store

import (
	"context"
	"io"
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
	// The parent is edited, so it has a past to carry, and closed to one
	// group, so it has a restriction to carry.
	parent, err = st.SaveWikiPage(ctx, workspaceID, actorID, models.WikiPage{
		ID: parent.ID, SpaceID: space.ID, Title: "Runbooks", Status: "current",
		Body:    models.WikiBody{Representation: "storage", Value: "<p>How we keep it up, and who to call.</p>"},
		Version: models.WikiVersion{Number: parent.Version.Number + 1, Message: "Say who to call"},
	})
	if err != nil {
		t.Fatalf("edit the parent page: %v", err)
	}
	groupName := "Runbook readers " + key
	group, err := st.CreateWikiGroup(ctx, workspaceID, actorID, groupName)
	if err != nil {
		t.Fatalf("create the group: %v", err)
	}
	t.Cleanup(func() { _, _ = st.Pool.Exec(ctx, `DELETE FROM groups WHERE id::text=$1`, group.ID) })
	if _, err := st.SetWikiPageRestrictions(ctx, workspaceID, actorID, parent.ID, "add", []models.WikiPageRestriction{{
		Operation: "update", Groups: []models.WikiRestrictionSubject{{Type: "group", ID: group.ID, Name: group.Name}},
	}}); err != nil {
		t.Fatalf("restrict the page: %v", err)
	}
	// What the parent carries beside its body: a label and a comment.
	if _, err := st.AddWikiPageLabels(ctx, workspaceID, actorID, parent.ID, []models.WikiLabel{{Name: "runbook", Prefix: "global"}}); err != nil {
		t.Fatalf("label the page: %v", err)
	}
	if _, err := st.CreateWikiFooterComment(ctx, workspaceID, actorID, models.WikiFooterComment{
		PageID: parent.ID, Body: models.WikiBody{Representation: "storage", Value: "<p>Say who is on call.</p>"},
	}); err != nil {
		t.Fatalf("comment on the page: %v", err)
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
	// The export carries what the pages hold beside their bodies, and one
	// file, so the import has all three to bring back.
	extras, err := st.wikiSpaceExportExtras(ctx, workspaceID, actorID, pages, posts)
	if err != nil {
		t.Fatal(err)
	}
	carried := []wikiExportAttachment{{
		ID: "9001", PageID: pages[0].ID, Filename: "runbook.txt", MediaType: "text/plain",
		Content: []byte("drain, restart, watch"), Included: true,
	}}
	archive, err := buildWikiSpaceExport(space, pages, posts, carried, extras)
	if err != nil {
		t.Fatalf("build the export: %v", err)
	}

	// Something that is not an export says so rather than making a space.
	if _, err := st.ImportWikiSpace(ctx, workspaceID, actorID, "NOTAZIP", "Not a zip", []byte("hello"), nil); err == nil ||
		!strings.Contains(err.Error(), "not a space export") {
		t.Fatalf("importing a file that is not an export = %v", err)
	}

	copyKey := "CPY" + strings.ToUpper(NewID("s")[len(NewID("s"))-5:])
	blobs := memoryBlobs{}
	result, err := st.ImportWikiSpace(ctx, workspaceID, actorID, copyKey, "Imported "+copyKey, archive, blobs)
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
	// What the pages carried came with them.
	if result.Labels != 1 || result.Comments != 1 {
		t.Fatalf("the import brought %d labels and %d comments", result.Labels, result.Comments)
	}
	labels, err := st.WikiPageLabels(ctx, workspaceID, actorID, byTitle["Runbooks"].ID)
	if err != nil || len(labels) != 1 || labels[0].Name != "runbook" {
		t.Fatalf("the imported page is labelled %+v, %v", labels, err)
	}
	comments, err := st.WikiFooterComments(ctx, workspaceID, actorID, byTitle["Runbooks"].ID)
	if err != nil || len(comments) != 1 || !strings.Contains(comments[0].Body.Value, "Say who is on call") {
		t.Fatalf("the imported page carries %d comments, %v", len(comments), err)
	}
	if result.Attachments != 1 || len(blobs) != 1 {
		t.Fatalf("the import brought %d attachments and wrote %d files", result.Attachments, len(blobs))
	}
	files, err := st.WikiAttachments(ctx, workspaceID, actorID, byTitle["Runbooks"].ID, "", "", "current")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Filename != "runbook.txt" {
		t.Fatalf("the imported page holds %+v", files)
	}

	// The page's past came with it: what it said before, and who wrote that.
	if result.Versions == 0 {
		t.Fatal("the import brought no history")
	}
	versions, err := st.WikiVersions(ctx, workspaceID, actorID, byTitle["Runbooks"].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) < 2 {
		t.Fatalf("the imported page has %d versions", len(versions))
	}
	if !strings.Contains(versions[0].Message, "Imported") {
		t.Fatalf("the first version says %q", versions[0].Message)
	}
	bodies, err := st.WikiPageVersionBodies(ctx, workspaceID, actorID, byTitle["Runbooks"].ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(bodies[1].Body, "How we keep it up.") {
		t.Fatalf("the first version says %q", bodies[1].Body)
	}
	if !strings.Contains(byTitle["Runbooks"].Body.Value, "who to call") {
		t.Fatalf("the page ends at %q", byTitle["Runbooks"].Body.Value)
	}

	// So did who may edit it.
	if result.Restrictions != 1 || len(result.Unmatched) != 0 {
		t.Fatalf("the import brought %d restrictions and could not match %v", result.Restrictions, result.Unmatched)
	}
	restrictions, err := st.WikiPageRestrictions(ctx, workspaceID, actorID, byTitle["Runbooks"].ID)
	if err != nil {
		t.Fatal(err)
	}
	restricted := false
	for _, restriction := range restrictions {
		for _, carried := range restriction.Groups {
			restricted = restricted || (restriction.Operation == "update" && carried.Name == groupName)
		}
	}
	if !restricted {
		t.Fatalf("the imported page is restricted to %+v", restrictions)
	}
}

// memoryBlobs keeps the files an import writes, so a test can count them.
type memoryBlobs map[string][]byte

func (b memoryBlobs) Put(ctx context.Context, key string, r io.Reader) (int64, error) {
	content, err := io.ReadAll(r)
	if err != nil {
		return 0, err
	}
	b[key] = content
	return int64(len(content)), nil
}
