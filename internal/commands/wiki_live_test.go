package commands_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

func TestWikiLiveDocumentsMergeEditorsInRevisionOrder(t *testing.T) {
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
	ws, ana, ben, outsider := store.NewID("ws"), store.NewID("usr"), store.NewID("usr"), store.NewID("usr")
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES ($1,$1,'Live editing test')`, ws)
	for _, id := range []string{ana, ben, outsider} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES ($1,$2,'test',$1)`, id, id+"@example.test")
	}
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES ($1,$2,'admin'),($1,$3,'admin')`, ws, ana, ben)
	t.Cleanup(func() {
		for _, sql := range []string{
			`DELETE FROM wiki_live_documents WHERE workspace_id=$1`,
			`DELETE FROM wiki_content_drafts WHERE workspace_id=$1`,
			`DELETE FROM wiki_blog_post_versions WHERE blog_post_id IN (SELECT b.id FROM wiki_blog_posts b JOIN wiki_spaces s ON s.id=b.space_id WHERE s.workspace_id=$1)`,
			`DELETE FROM wiki_blog_posts WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`,
			`DELETE FROM wiki_page_versions WHERE page_id IN (SELECT p.id FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id WHERE s.workspace_id=$1)`,
			`DELETE FROM wiki_pages WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`,
			`DELETE FROM wiki_spaces WHERE workspace_id=$1`,
			`DELETE FROM actions WHERE workspace_id=$1`,
			`DELETE FROM memberships WHERE workspace_id=$1`,
			`DELETE FROM workspaces WHERE id=$1`,
		} {
			exec(sql, ws)
		}
		for _, id := range []string{ana, ben, outsider} {
			exec(`DELETE FROM users WHERE id=$1`, id)
		}
	})
	service := &commands.Service{Store: st}
	space, err := service.CreateWikiSpace(ctx, ws, ana, "LIVE", "Live editing", "Together", false)
	if err != nil {
		t.Fatal(err)
	}
	page, err := service.SaveWikiPage(ctx, ws, ana, models.WikiPage{SpaceID: space.ID, Title: "Plan", Status: "current", Body: models.WikiBody{Representation: "storage", Value: "<p>Ship it</p>"}})
	if err != nil {
		t.Fatal(err)
	}

	// Opening the document hands the whole text to a new editor.
	opened, err := st.WikiLiveDocument(ctx, ws, ana, "page", page.ID, "", -1)
	if err != nil || opened.Body == nil || *opened.Body != "<p>Ship it</p>" || opened.Revision != 0 || opened.Session == "" {
		t.Fatalf("opened = %+v, %v", opened, err)
	}
	session := opened.Session
	if _, err := st.WikiLiveDocument(ctx, ws, outsider, "page", page.ID, "", -1); err == nil {
		t.Fatal("a non-member opened the live document")
	}

	// Ana's change lands at the latest revision and becomes the page's draft.
	applied, err := st.ApplyWikiLiveChanges(ctx, ws, ana, "page", page.ID, session, 0, []store.WikiLiveChange{{Position: 3, Delete: 0, Insert: "We "}})
	if err != nil || applied.Revision != 1 || len(applied.Changes) != 1 || applied.Changes[0].AuthorID != ana {
		t.Fatalf("applied = %+v, %v", applied, err)
	}
	draft, err := st.WikiContentDraft(ctx, ws, ana, "page", page.ID)
	if err != nil || draft.Body.Value != "<p>We Ship it</p>" {
		t.Fatalf("draft = %+v, %v", draft, err)
	}

	// Ben edited revision 0 too; his change is refused with what he missed.
	stale, err := st.ApplyWikiLiveChanges(ctx, ws, ben, "page", page.ID, session, 0, []store.WikiLiveChange{{Position: 10, Delete: 0, Insert: " today"}})
	if !errors.Is(err, store.ErrWikiLiveStale) || stale.Body != nil || len(stale.Changes) != 1 || stale.Changes[0].Insert != "We " || stale.Revision != 1 {
		t.Fatalf("stale = %+v, %v", stale, err)
	}
	// Rebased past the three units Ana inserted before it, it lands.
	if _, err := st.ApplyWikiLiveChanges(ctx, ws, ben, "page", page.ID, session, 1, []store.WikiLiveChange{{Position: 13, Delete: 0, Insert: " today"}}); err != nil {
		t.Fatal(err)
	}
	caughtUp, err := st.WikiLiveDocument(ctx, ws, ana, "page", page.ID, session, 1)
	if err != nil || caughtUp.Body != nil || len(caughtUp.Changes) != 1 || caughtUp.Changes[0].AuthorID != ben || caughtUp.Revision != 2 {
		t.Fatalf("caught up = %+v, %v", caughtUp, err)
	}
	if draft, err = st.WikiContentDraft(ctx, ws, ana, "page", page.ID); err != nil || draft.Body.Value != "<p>We Ship it today</p>" {
		t.Fatalf("merged draft = %+v, %v", draft, err)
	}

	// Positions count UTF-16 units, and a change may not split a character.
	if _, err := st.ApplyWikiLiveChanges(ctx, ws, ana, "page", page.ID, session, 2, []store.WikiLiveChange{{Position: 3, Insert: "🚀"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApplyWikiLiveChanges(ctx, ws, ana, "page", page.ID, session, 3, []store.WikiLiveChange{{Position: 4, Delete: 1}}); !errors.Is(err, store.ErrWikiValidation) {
		t.Fatalf("split character error = %v", err)
	}
	if _, err := st.ApplyWikiLiveChanges(ctx, ws, ana, "page", page.ID, session, 3, []store.WikiLiveChange{{Position: 999, Delete: 1}}); !errors.Is(err, store.ErrWikiValidation) {
		t.Fatalf("out of range error = %v", err)
	}
	// Text that is not yet valid storage is shared but not drafted.
	if _, err := st.ApplyWikiLiveChanges(ctx, ws, ana, "page", page.ID, session, 3, []store.WikiLiveChange{{Position: 0, Insert: "<stro"}}); err != nil {
		t.Fatal(err)
	}
	if draft, err = st.WikiContentDraft(ctx, ws, ana, "page", page.ID); err != nil || draft.Body.Value != "<p>🚀We Ship it today</p>" {
		t.Fatalf("draft after partial markup = %+v, %v", draft, err)
	}

	// Publishing a new version restarts the session from the page.
	published, err := service.SaveWikiPage(ctx, ws, ben, models.WikiPage{ID: page.ID, SpaceID: space.ID, Title: "Plan", Status: "current", Body: models.WikiBody{Representation: "storage", Value: "<p>Shipped</p>"}, Version: models.WikiVersion{Number: page.Version.Number + 1}})
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := st.WikiLiveDocument(ctx, ws, ana, "page", page.ID, session, 4)
	if err != nil || restarted.Session == session || restarted.Body == nil || *restarted.Body != "<p>Shipped</p>" || restarted.Version != published.Version.Number || restarted.Revision != 0 {
		t.Fatalf("restarted = %+v, %v", restarted, err)
	}
	if _, err := st.ApplyWikiLiveChanges(ctx, ws, ana, "page", page.ID, session, 4, []store.WikiLiveChange{{Position: 0, Insert: "x"}}); !errors.Is(err, store.ErrWikiLiveStale) {
		t.Fatalf("old session error = %v", err)
	}

	// Closing ends the session, and the next editor starts a new one.
	if err := st.CloseWikiLiveDocument(ctx, ws, "page", page.ID); err != nil {
		t.Fatal(err)
	}
	reopened, err := st.WikiLiveDocument(ctx, ws, ben, "page", page.ID, restarted.Session, 0)
	if err != nil || reopened.Session == restarted.Session || reopened.Body == nil {
		t.Fatalf("reopened = %+v, %v", reopened, err)
	}

	// A blog post is edited live the same way, with its own draft.
	post, err := service.SaveWikiBlogPost(ctx, ws, ana, models.WikiBlogPost{SpaceID: space.ID, Title: "Update", Status: "current", Body: models.WikiBody{Representation: "storage", Value: "<p>Notes</p>"}})
	if err != nil {
		t.Fatal(err)
	}
	postDoc, err := st.WikiLiveDocument(ctx, ws, ben, "blogpost", post.ID, "", -1)
	if err != nil || postDoc.Body == nil || *postDoc.Body != "<p>Notes</p>" {
		t.Fatalf("blog post document = %+v, %v", postDoc, err)
	}
	if _, err := st.ApplyWikiLiveChanges(ctx, ws, ben, "blogpost", post.ID, postDoc.Session, 0, []store.WikiLiveChange{{Position: 8, Insert: " added"}}); err != nil {
		t.Fatal(err)
	}
	postDraft, err := st.WikiContentDraft(ctx, ws, ben, "blogpost", post.ID)
	if err != nil || postDraft.Body.Value != "<p>Notes added</p>" {
		t.Fatalf("blog post draft = %+v, %v", postDraft, err)
	}
	// The page's and the blog post's documents are separate even when their ids meet.
	if pageDoc, err := st.WikiLiveDocument(ctx, ws, ben, "page", page.ID, reopened.Session, 0); err != nil || pageDoc.Body != nil {
		t.Fatalf("page document after blog post edit = %+v, %v", pageDoc, err)
	}
	if _, err := service.SaveWikiBlogPost(ctx, ws, ana, models.WikiBlogPost{ID: post.ID, SpaceID: space.ID, Title: "Update", Status: "current", Body: models.WikiBody{Representation: "storage", Value: "<p>Published notes</p>"}, Version: models.WikiVersion{Number: post.Version.Number + 1}}); err != nil {
		t.Fatal(err)
	}
	if restartedPost, err := st.WikiLiveDocument(ctx, ws, ben, "blogpost", post.ID, postDoc.Session, 1); err != nil || restartedPost.Session == postDoc.Session || restartedPost.Body == nil || *restartedPost.Body != "<p>Published notes</p>" {
		t.Fatalf("restarted blog post document = %+v, %v", restartedPost, err)
	}
	if _, err := st.WikiLiveDocument(ctx, ws, ana, "comment", post.ID, "", -1); !errors.Is(err, store.ErrWikiValidation) {
		t.Fatalf("unsupported kind error = %v", err)
	}
}
