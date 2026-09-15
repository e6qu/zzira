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
	content, err := buildWikiSpaceExport(space, pages, posts)
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
	if len(files) != 3 {
		t.Fatalf("export files = %v", files)
	}
	for name, wants := range map[string][]string{
		"index.html":        {"<title>Operations &lt;team&gt;</title>", "Runbooks &amp; notes", `<a href="pages/11.html">Restart &amp; recover</a>`, `<a href="blogposts/21.html">Release notes</a>`},
		"pages/11.html":     {"<title>Restart &amp; recover</title>", "Check the dashboard."},
		"blogposts/21.html": {"<h1>Release notes</h1>", "Shipped."},
	} {
		for _, want := range wants {
			if !strings.Contains(files[name], want) {
				t.Fatalf("%s lacks %q: %s", name, want, files[name])
			}
		}
	}
}
