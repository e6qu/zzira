package web

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/render"
	"github.com/e6qu/zzira/internal/store"
	"github.com/jackc/pgx/v5"
)

func TestWikiPageArchiveDuringMetadataLoad(t *testing.T) {
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
	ws, actor := store.NewID("ws"), store.NewID("usr")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Archive race')`, ws)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Reader')`, actor, actor+"@example.invalid")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'admin')`, ws, actor)
	t.Cleanup(func() {
		for _, query := range []string{
			`DELETE FROM actions WHERE workspace_id=$1`,
			`UPDATE wiki_spaces SET homepage_id=NULL WHERE workspace_id=$1`,
			`DELETE FROM wiki_page_versions WHERE page_id IN (SELECT p.id FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id WHERE s.workspace_id=$1)`,
			`DELETE FROM wiki_pages WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`,
			`DELETE FROM wiki_space_role_assignments WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`,
			`DELETE FROM wiki_space_permission_grants WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`,
			`DELETE FROM wiki_space_roles WHERE workspace_id=$1`,
			`DELETE FROM wiki_spaces WHERE workspace_id=$1`,
			`DELETE FROM memberships WHERE workspace_id=$1`,
			`DELETE FROM workspaces WHERE id=$1`,
		} {
			exec(query, ws)
		}
		exec(`DELETE FROM users WHERE id=$1`, actor)
	})
	space, err := st.CreateWikiSpaceFull(ctx, ws, actor, store.CreateWikiSpaceInput{Key: "RACE", Name: "Archive race"})
	if err != nil {
		t.Fatal(err)
	}
	page, err := st.SaveWikiPage(ctx, ws, actor, models.WikiPage{SpaceID: space.ID, Title: "A page", Status: "current", Body: models.WikiBody{Value: "<p>Content</p>", Representation: "storage"}})
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{Store: st}
	request := httptest.NewRequest(http.MethodGet, wikiPageURL(page), nil)
	if h.redirectArchivedWikiPage(httptest.NewRecorder(), request, ws, actor, page.ID, pgx.ErrNoRows) {
		t.Fatal("a current page must not loop on an unrelated missing row")
	}
	if _, err := st.ArchiveWikiPages(ctx, ws, actor, []string{page.ID}, false); err != nil {
		t.Fatal(err)
	}
	for _, load := range []func() error{
		func() error { _, err := st.WikiPageLikes(ctx, ws, actor, page.ID); return err },
		func() error { _, err := st.WikiWatchStatus(ctx, ws, actor, actor, "content", page.ID); return err },
	} {
		cause := load()
		if !errors.Is(cause, pgx.ErrNoRows) {
			t.Fatalf("archived metadata: %v", cause)
		}
		response := httptest.NewRecorder()
		if !h.redirectArchivedWikiPage(response, request, ws, actor, page.ID, cause) || response.Code != http.StatusSeeOther || response.Header().Get("Location") != wikiPageURL(page) {
			t.Fatalf("archive race response: %d %s", response.Code, response.Body.String())
		}
	}
	if h.redirectArchivedWikiPage(httptest.NewRecorder(), request, ws, actor, page.ID, errors.New("database unavailable")) ||
		h.redirectArchivedWikiPage(httptest.NewRecorder(), request, ws, "outsider", page.ID, pgx.ErrNoRows) {
		t.Fatal("database errors and unreadable pages must not redirect")
	}
}

func TestWikiWebErrorEscapesLogInput(t *testing.T) {
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(previous) })
	status, message := wikiWebError(errors.New("database value\r\nforged entry\x1b[31m"))
	if status != 500 || strings.Contains(message, "database value") {
		t.Fatalf("unexpected error response: %d %q", status, message)
	}
	logged := output.String()
	if strings.Count(logged, "\n") != 1 || strings.ContainsAny(logged, "\r\x1b") || !strings.Contains(logged, `database value\r\nforged entry\x1b[31m`) {
		t.Fatalf("unsafe log entry: %q", logged)
	}
}

func TestWikiEditorsIdentifyTheDraftOwner(t *testing.T) {
	for _, template := range []string{"page_wiki_page", "page_wiki_blogpost"} {
		for _, account := range []string{"writer-one", "writer-two"} {
			t.Run(template+"/"+account, func(t *testing.T) {
				data := wikiData{Space: &models.WikiSpace{ID: "100", Name: "Team"}, Editing: true, CanEdit: true,
					Page:     &models.WikiPage{ID: "200", Status: "current", Title: "Page"},
					BlogPost: &models.WikiBlogPost{ID: "300", Status: "current", Title: "News"}}
				var output bytes.Buffer
				if err := render.Page(&output, template, pageData{User: &models.User{ID: account}, Data: data}); err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(output.String(), `data-wiki-live-sync data-live-account="`+account+`"`) {
					t.Fatal("the editor does not identify the account for draft recovery")
				}
			})
		}
	}
}
