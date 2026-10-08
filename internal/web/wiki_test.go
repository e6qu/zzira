package web

import (
	"bytes"
	"errors"
	"log"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/render"
)

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
