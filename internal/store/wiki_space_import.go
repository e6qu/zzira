package store

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/e6qu/zzira/internal/models"
)

// An export carries a manifest so a space can be read back: the pages and blog
// posts in the storage the site keeps, rather than the HTML the export is for
// reading. Importing one makes a new space from it, which is how a space moves
// between sites and how one is restored after somebody empties it.

// WikiSpaceImportResult is what an import made.
type WikiSpaceImportResult struct {
	Space     *models.WikiSpace
	Pages     int
	BlogPosts int
}

// ImportWikiSpace reads an export and makes a space from it. The key and name
// are the importer's, because a site rarely wants the key the export came
// from, and never wants it when the space is being restored beside the
// original.
func (s *Store) ImportWikiSpace(ctx context.Context, ws, actor, key, name string, archive []byte) (WikiSpaceImportResult, error) {
	manifest, err := readWikiSpaceManifest(archive)
	if err != nil {
		return WikiSpaceImportResult{}, err
	}
	if strings.TrimSpace(key) == "" {
		key = manifest.Key
	}
	if strings.TrimSpace(name) == "" {
		name = manifest.Name
	}
	space, err := s.CreateWikiSpaceFull(ctx, ws, actor, CreateWikiSpaceInput{
		Key: key, Name: name, Description: manifest.Description,
	})
	if err != nil {
		return WikiSpaceImportResult{}, err
	}
	result := WikiSpaceImportResult{Space: space}
	// A page is raised under the page it belonged under, so the tree comes
	// back the shape it left: the manifest lists parents before children.
	made := map[string]string{}
	for _, page := range manifest.Pages {
		body := models.WikiBody{Representation: page.Representation, Value: page.Body}
		if body.Representation == "" {
			body.Representation = "storage"
		}
		saved, err := s.SaveWikiPage(ctx, ws, actor, models.WikiPage{
			SpaceID: space.ID, Title: page.Title, Body: body, Status: "current", ParentID: made[page.ParentID],
		})
		if err != nil {
			return result, fmt.Errorf("import the page %q: %w", page.Title, err)
		}
		made[page.ID] = saved.ID
		result.Pages++
	}
	for _, post := range manifest.BlogPosts {
		body := models.WikiBody{Representation: post.Representation, Value: post.Body}
		if body.Representation == "" {
			body.Representation = "storage"
		}
		if _, err := s.SaveWikiBlogPost(ctx, ws, actor, models.WikiBlogPost{
			SpaceID: space.ID, Title: post.Title, Body: body, Status: "current",
		}); err != nil {
			return result, fmt.Errorf("import the blog post %q: %w", post.Title, err)
		}
		result.BlogPosts++
	}
	return result, nil
}

// readWikiSpaceManifest reads space.json out of an export. An archive without
// one is an export from before exports carried a manifest, or not an export at
// all, and says so rather than making an empty space.
func readWikiSpaceManifest(archive []byte) (wikiSpaceManifest, error) {
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return wikiSpaceManifest{}, fmt.Errorf("%w: that file is not a space export", ErrWikiValidation)
	}
	for _, file := range reader.File {
		if file.Name != "space.json" {
			continue
		}
		opened, err := file.Open()
		if err != nil {
			return wikiSpaceManifest{}, err
		}
		defer func() { _ = opened.Close() }()
		raw, err := io.ReadAll(io.LimitReader(opened, 64<<20))
		if err != nil {
			return wikiSpaceManifest{}, err
		}
		var manifest wikiSpaceManifest
		if err := json.Unmarshal(raw, &manifest); err != nil {
			return wikiSpaceManifest{}, fmt.Errorf("%w: the export's manifest cannot be read", ErrWikiValidation)
		}
		if manifest.Version != 1 {
			return wikiSpaceManifest{}, fmt.Errorf("%w: this export was written by a later version of the site", ErrWikiValidation)
		}
		return manifest, nil
	}
	return wikiSpaceManifest{}, fmt.Errorf("%w: that export carries no manifest, so there is nothing to read back", ErrWikiValidation)
}
