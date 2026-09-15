package store

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/models"
)

func TestBuildWikiSpaceExport(t *testing.T) {
	space := &models.WikiSpace{ID: "7", Key: "OPS", Name: "Operations <team>", Description: "Runbooks & notes"}
	pages := []*models.WikiPage{{ID: "11", Title: "Restart & recover", Body: models.WikiBody{Value: "<p>Check the dashboard.</p>", Representation: "storage"}}}
	posts := []*models.WikiBlogPost{{ID: "21", Title: "Release notes", Body: models.WikiBody{Value: "<p>Shipped.</p>", Representation: "storage"}}}
	attachments := []wikiExportAttachment{
		{ID: "31", PageID: "11", Filename: "runbook/../plan <v2>.txt", Content: []byte("step one"), Included: true},
		{ID: "32", PageID: "11", Filename: "disk.iso"},
		{ID: "33", BlogPostID: "21", Filename: "notes.txt", Content: []byte("shipped notes"), Included: true},
	}
	content, err := buildWikiSpaceExport(space, pages, posts, attachments)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, file := range reader.File {
		opened, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(opened)
		_ = opened.Close()
		if err != nil {
			t.Fatal(err)
		}
		files[file.Name] = string(data)
	}
	if len(files) != 5 {
		t.Fatalf("export files = %v", files)
	}
	for name, wants := range map[string][]string{
		"index.html":        {"<title>Operations &lt;team&gt;</title>", "Runbooks &amp; notes", `<a href="pages/11.html">Restart &amp; recover</a>`, `<a href="blogposts/21.html">Release notes</a>`},
		"pages/11.html":     {"<title>Restart &amp; recover</title>", "Check the dashboard.", `<a href="../attachments/31/runbook_.._plan%20%3Cv2%3E.txt">runbook/../plan &lt;v2&gt;.txt</a>`, "<li>disk.iso (too large to include in this export)</li>"},
		"blogposts/21.html": {"<h1>Release notes</h1>", "Shipped.", `<a href="../attachments/33/notes.txt">notes.txt</a>`},
		"attachments/31/runbook_.._plan <v2>.txt": {"step one"},
		"attachments/33/notes.txt":                {"shipped notes"},
	} {
		for _, want := range wants {
			if !strings.Contains(files[name], want) {
				t.Fatalf("%s lacks %q: %s", name, want, files[name])
			}
		}
	}
}
