package store

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
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
	// Labels, Comments and Attachments are what came back with the content.
	Labels      int
	Comments    int
	Attachments int
}

// ImportWikiSpace reads an export and makes a space from it. The key and name
// are the importer's, because a site rarely wants the key the export came
// from, and never wants it when the space is being restored beside the
// original.
// BlobWriter is where an import puts the files it carries.
type BlobWriter interface {
	Put(ctx context.Context, key string, r io.Reader) (int64, error)
}

func (s *Store) ImportWikiSpace(ctx context.Context, ws, actor, key, name string, archive []byte, blobs BlobWriter) (WikiSpaceImportResult, error) {
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
		if err := s.importWikiContentExtras(ctx, ws, actor, saved.ID, "", page, archive, blobs, &result); err != nil {
			return result, fmt.Errorf("import what %q carries: %w", page.Title, err)
		}
	}
	for _, post := range manifest.BlogPosts {
		body := models.WikiBody{Representation: post.Representation, Value: post.Body}
		if body.Representation == "" {
			body.Representation = "storage"
		}
		saved, err := s.SaveWikiBlogPost(ctx, ws, actor, models.WikiBlogPost{
			SpaceID: space.ID, Title: post.Title, Body: body, Status: "current",
		})
		if err != nil {
			return result, fmt.Errorf("import the blog post %q: %w", post.Title, err)
		}
		result.BlogPosts++
		if err := s.importWikiContentExtras(ctx, ws, actor, "", saved.ID, post, archive, blobs, &result); err != nil {
			return result, fmt.Errorf("import what %q carries: %w", post.Title, err)
		}
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
		if manifest.Version < 1 || manifest.Version > wikiSpaceManifestVersion {
			return wikiSpaceManifest{}, fmt.Errorf("%w: this export was written by a later version of the site", ErrWikiValidation)
		}
		return manifest, nil
	}
	return wikiSpaceManifest{}, fmt.Errorf("%w: that export carries no manifest, so there is nothing to read back", ErrWikiValidation)
}

// importWikiContentExtras brings back what a page or blog post carried beside
// its body: its labels, the comments under it, and the files the archive
// holds. A comment says who wrote it on the site it came from, which is a
// name this site may know nothing about, so the import owns them and keeps
// the original author in the text.
func (s *Store) importWikiContentExtras(ctx context.Context, ws, actor, pageID, blogPostID string, content wikiSpaceManifestPage, archive []byte, blobs BlobWriter, result *WikiSpaceImportResult) error {
	if pageID != "" && len(content.Labels) > 0 {
		labels := make([]models.WikiLabel, 0, len(content.Labels))
		for _, label := range content.Labels {
			labels = append(labels, models.WikiLabel{Name: label, Prefix: "global"})
		}
		if _, err := s.AddWikiPageLabels(ctx, ws, actor, pageID, labels); err != nil {
			return err
		}
		result.Labels += len(labels)
	}
	for _, comment := range content.Comments {
		body := comment.Body
		if comment.Author != "" {
			body = "<p><em>" + html.EscapeString(comment.Author) + " wrote, in the space this was imported from:</em></p>" + body
		}
		if _, err := s.CreateWikiFooterComment(ctx, ws, actor, models.WikiFooterComment{
			PageID: pageID, BlogPostID: blogPostID, Body: models.WikiBody{Representation: "storage", Value: body},
		}); err != nil {
			return err
		}
		result.Comments++
	}
	if blobs == nil {
		return nil
	}
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return err
	}
	for _, file := range content.Attachments {
		carried, err := readArchiveFile(reader, file.Path)
		if err != nil {
			// A manifest may name a file the archive did not carry, which is
			// what an export does with one too large to include.
			continue
		}
		ref := NewID("blob")
		size, err := blobs.Put(ctx, ref, bytes.NewReader(carried))
		if err != nil {
			return err
		}
		mediaType := file.MediaType
		if mediaType == "" {
			mediaType = "application/octet-stream"
		}
		if pageID != "" {
			_, err = s.SaveWikiAttachment(ctx, ws, actor, pageID, "", file.Filename, mediaType, "", "Imported with the space", false, size, ref)
		} else {
			_, err = s.SaveWikiBlogAttachment(ctx, ws, actor, blogPostID, "", file.Filename, mediaType, "", "Imported with the space", false, size, ref)
		}
		if err != nil {
			return err
		}
		result.Attachments++
	}
	return nil
}

// readArchiveFile is one file of the archive by the path the manifest names.
func readArchiveFile(reader *zip.Reader, path string) ([]byte, error) {
	for _, file := range reader.File {
		if file.Name != path {
			continue
		}
		opened, err := file.Open()
		if err != nil {
			return nil, err
		}
		defer func() { _ = opened.Close() }()
		return io.ReadAll(io.LimitReader(opened, wikiSpaceExportAttachmentLimit))
	}
	return nil, fmt.Errorf("the archive does not carry %q", path)
}
