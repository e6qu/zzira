package store

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"html"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/wikimarkup"
)

const apiTaskWikiSpaceExport = "wiki-space-export"

// ErrWikiSpaceExportNotFound is an export that does not exist or belongs to
// someone else.
var ErrWikiSpaceExportNotFound = errors.New("space export not found")

type wikiSpaceExportPayload struct {
	SpaceID  string `json:"spaceId"`
	SpaceKey string `json:"spaceKey"`
}

// WikiSpaceExportTask is an export a person asked for, as the space page
// lists it.
type WikiSpaceExportTask struct {
	ID, Status, Message string
	SubmittedAt         time.Time
}

// WikiSpaceExportPath is where the person who asked for an export downloads it.
func WikiSpaceExportPath(spaceID, taskID string) string {
	return "/wiki/spaces/" + spaceID + "/exports/" + taskID + ".zip"
}

// EnqueueWikiSpaceExport queues an HTML export of a space for one of its
// administrators.
func (s *Store) EnqueueWikiSpaceExport(ctx context.Context, ws, actor, spaceKey string) (APITask, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return APITask{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	space, err := lockSpaceForAdmin(ctx, tx, ws, actor, spaceKey)
	if err != nil {
		return APITask{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return APITask{}, err
	}
	task, err := queuedAPITask(ws, actor, "Export space "+space.Key, apiTaskWikiSpaceExport, wikiSpaceExportPayload{SpaceID: space.ID, SpaceKey: space.Key})
	if err != nil {
		return APITask{}, err
	}
	if err = s.enqueueAPITask(ctx, &task); err != nil {
		return APITask{}, err
	}
	return task, nil
}

// WikiSpaceExportTasks lists a person's latest exports of a space.
func (s *Store) WikiSpaceExportTasks(ctx context.Context, ws, actor, spaceID string) ([]WikiSpaceExportTask, error) {
	rows, err := s.Pool.Query(ctx, `SELECT jira_id::text,status,message,submitted_at FROM api_tasks
		WHERE workspace_id=$1 AND kind=$2 AND submitted_by=$3 AND payload::jsonb->>'spaceId'=$4
		ORDER BY submitted_at DESC LIMIT 5`, ws, apiTaskWikiSpaceExport, actor, spaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tasks := []WikiSpaceExportTask{}
	for rows.Next() {
		var task WikiSpaceExportTask
		if err := rows.Scan(&task.ID, &task.Status, &task.Message, &task.SubmittedAt); err != nil {
			return nil, err
		}
		task.SubmittedAt = task.SubmittedAt.UTC()
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}

// WikiSpaceExport returns an export's zip to the person who asked for it.
func (s *Store) WikiSpaceExport(ctx context.Context, ws, spaceID, taskID, userID string) ([]byte, error) {
	var content []byte
	err := s.Pool.QueryRow(ctx, `SELECT e.content FROM wiki_space_exports e JOIN api_tasks t ON t.id=e.task_id
		WHERE e.workspace_id=$1 AND e.space_id=$2 AND (t.id=$3 OR t.jira_id::text=$3) AND e.requested_by=$4`, ws, spaceID, taskID, userID).Scan(&content)
	if err != nil {
		return nil, ErrWikiSpaceExportNotFound
	}
	return content, nil
}

// executeWikiSpaceExport writes the space's current pages and blog posts that
// the person who asked can see into a zip, and emails them the link.
func (s *Store) executeWikiSpaceExport(ctx context.Context, task APITask, blobs BlobReader) error {
	var payload wikiSpaceExportPayload
	if err := json.Unmarshal(task.Payload, &payload); err != nil {
		return fmt.Errorf("decode space export: %w", err)
	}
	space, err := s.WikiSpace(ctx, task.WorkspaceID, task.SubmittedBy, payload.SpaceID)
	if err != nil {
		return err
	}
	pages, err := s.WikiPages(ctx, task.WorkspaceID, task.SubmittedBy, space.ID, "current", "")
	if err != nil {
		return err
	}
	posts, err := s.WikiBlogPosts(ctx, task.WorkspaceID, task.SubmittedBy, space.ID, "current", "", "created-date")
	if err != nil {
		return err
	}
	var attachments []wikiExportAttachment
	if blobs != nil {
		if attachments, err = s.wikiSpaceExportAttachments(ctx, task.WorkspaceID, task.SubmittedBy, pages, posts, blobs); err != nil {
			return err
		}
	}
	extras, err := s.wikiSpaceExportExtras(ctx, task.WorkspaceID, task.SubmittedBy, pages, posts)
	if err != nil {
		return err
	}
	content, err := buildWikiSpaceExport(space, pages, posts, attachments, extras)
	if err != nil {
		return err
	}
	included := 0
	for _, attachment := range attachments {
		if attachment.Included {
			included++
		}
	}
	if _, err = s.Pool.Exec(ctx, `INSERT INTO wiki_space_exports(task_id,workspace_id,space_id,requested_by,content) VALUES($1,$2,$3,$4,$5)
		ON CONFLICT (task_id) DO UPDATE SET content=EXCLUDED.content`, task.ID, task.WorkspaceID, space.ID, task.SubmittedBy, content); err != nil {
		return err
	}
	path := WikiSpaceExportPath(space.ID, task.WireID())
	var email string
	if err = s.Pool.QueryRow(ctx, `SELECT email FROM users WHERE id=$1`, task.SubmittedBy).Scan(&email); err == nil && email != "" {
		if _, err = s.Pool.Exec(ctx, `INSERT INTO email_outbox(workspace_id,recipient,subject,body,dedupe_key) VALUES($1,$2,$3,$4,$5)
			ON CONFLICT(dedupe_key) WHERE dedupe_key IS NOT NULL DO NOTHING`, task.WorkspaceID, email, "Your export of "+space.Name+" is ready",
			fmt.Sprintf("The HTML export of %d pages, %d blog posts and %d attachments is ready to download:\n%s", len(pages), len(posts), included, path), "wiki-space-export:"+task.ID); err != nil {
			return err
		}
	}
	return s.CompleteAPITask(ctx, task, fmt.Sprintf("Exported %d pages, %d blog posts and %d attachments.", len(pages), len(posts), included), map[string]any{"fileUrl": path, "pageCount": len(pages), "blogPostCount": len(posts), "attachmentCount": included})
}

// wikiSpaceExportExtras reads what the exported pages and blog posts carry
// beside their bodies: the labels on them and the comments under them, as the
// person exporting can see them.
func (s *Store) wikiSpaceExportExtras(ctx context.Context, ws, user string, pages []*models.WikiPage, posts []*models.WikiBlogPost) (wikiSpaceExtras, error) {
	extras := wikiSpaceExtras{Labels: map[string][]string{}, Comments: map[string][]wikiSpaceManifestComment{},
		Restrictions: map[string][]wikiSpaceManifestRestriction{}, History: map[string][]wikiSpaceManifestVersion{}}
	for _, page := range pages {
		restrictions, err := s.wikiSpaceExportRestrictions(ctx, ws, user, page.ID)
		if err != nil {
			return extras, err
		}
		if len(restrictions) > 0 {
			extras.Restrictions[page.ID] = restrictions
		}
		history, err := s.wikiSpaceExportHistory(ctx, ws, user, page)
		if err != nil {
			return extras, err
		}
		if len(history) > 0 {
			extras.History[page.ID] = history
		}
		labels, err := s.WikiPageLabels(ctx, ws, user, page.ID)
		if err != nil {
			return extras, err
		}
		for _, label := range labels {
			extras.Labels[page.ID] = append(extras.Labels[page.ID], label.Name)
		}
		comments, err := s.WikiFooterComments(ctx, ws, user, page.ID)
		if err != nil {
			return extras, err
		}
		for _, comment := range comments {
			extras.Comments[page.ID] = append(extras.Comments[page.ID], wikiSpaceManifestComment{Body: comment.Body.Value, Author: comment.AuthorName})
		}
	}
	for _, post := range posts {
		comments, err := s.WikiBlogFooterComments(ctx, ws, user, post.ID)
		if err != nil {
			return extras, err
		}
		for _, comment := range comments {
			extras.Comments[post.ID] = append(extras.Comments[post.ID], wikiSpaceManifestComment{Body: comment.Body.Value, Author: comment.AuthorName})
		}
	}
	return extras, nil
}

// wikiSpaceExportRestrictions is who may read and who may edit a page, named
// the way another site can find them: a person by email, a group by name.
func (s *Store) wikiSpaceExportRestrictions(ctx context.Context, ws, user, pageID string) ([]wikiSpaceManifestRestriction, error) {
	restrictions, err := s.WikiPageRestrictions(ctx, ws, user, pageID)
	if err != nil {
		return nil, err
	}
	out := []wikiSpaceManifestRestriction{}
	for _, restriction := range restrictions {
		if len(restriction.Users) == 0 && len(restriction.Groups) == 0 {
			continue
		}
		carried := wikiSpaceManifestRestriction{Operation: restriction.Operation}
		for _, person := range restriction.Users {
			var email string
			if err := s.Pool.QueryRow(ctx, `SELECT email FROM users WHERE id=$1`, person.AccountID).Scan(&email); err != nil {
				continue
			}
			carried.Users = append(carried.Users, email)
		}
		for _, group := range restriction.Groups {
			if group.Name != "" {
				carried.Groups = append(carried.Groups, group.Name)
			}
		}
		if len(carried.Users) > 0 || len(carried.Groups) > 0 {
			out = append(out, carried)
		}
	}
	return out, nil
}

// wikiSpaceExportHistory is what a page said before its current version,
// oldest first, with who wrote each version and when.
func (s *Store) wikiSpaceExportHistory(ctx context.Context, ws, user string, page *models.WikiPage) ([]wikiSpaceManifestVersion, error) {
	versions, err := s.WikiVersions(ctx, ws, user, page.ID)
	if err != nil {
		return nil, err
	}
	if len(versions) <= 1 {
		return nil, nil
	}
	bodies, err := s.WikiPageVersionBodies(ctx, ws, user, page.ID)
	if err != nil {
		return nil, err
	}
	names := map[string]string{}
	out := []wikiSpaceManifestVersion{}
	for _, version := range versions {
		if version.Number >= page.Version.Number {
			continue
		}
		body, ok := bodies[version.Number]
		if !ok {
			continue
		}
		author := names[version.AuthorID]
		if author == "" && version.AuthorID != "" {
			if person, err := s.UserByID(ctx, version.AuthorID); err == nil {
				author = person.DisplayName
				names[version.AuthorID] = author
			}
		}
		out = append(out, wikiSpaceManifestVersion{
			Number: version.Number, Title: body.Title, Body: body.Body, Author: author,
			Message: version.Message, CreatedAt: version.CreatedAt, MinorEdit: version.MinorEdit,
		})
	}
	return out, nil
}

// wikiSpaceExportAttachmentLimit caps the attachment bytes one export carries;
// attachments past it are listed without their files.
const wikiSpaceExportAttachmentLimit = 100 << 20

// wikiExportAttachment is a current attachment of an exported page or blog
// post, with its file when the export carries it.
type wikiExportAttachment struct {
	ID, PageID, BlogPostID, Filename string
	MediaType                        string
	Content                          []byte
	Included                         bool
}

// wikiSpaceExportAttachments reads the current attachments of the exported
// pages and blog posts that the person exporting can see, with their files up
// to the export's attachment limit. A file that cannot be read is listed
// without it.
func (s *Store) wikiSpaceExportAttachments(ctx context.Context, ws, user string, pages []*models.WikiPage, posts []*models.WikiBlogPost, blobs BlobReader) ([]wikiExportAttachment, error) {
	pageIDs, postIDs := make([]string, 0, len(pages)), make([]string, 0, len(posts))
	for _, page := range pages {
		pageIDs = append(pageIDs, page.ID)
	}
	for _, post := range posts {
		postIDs = append(postIDs, post.ID)
	}
	rows, err := s.Pool.Query(ctx, `SELECT a.id::text,COALESCE(a.page_id::text,''),COALESCE(a.blog_post_id::text,''),a.filename,COALESCE(a.media_type,''),v.blob_ref,a.size
		FROM wiki_attachments a LEFT JOIN wiki_pages p ON p.id=a.page_id LEFT JOIN wiki_blog_posts b ON b.id=a.blog_post_id
		JOIN wiki_spaces s ON s.id=COALESCE(p.space_id,b.space_id)
		JOIN wiki_attachment_versions v ON v.attachment_id=a.id AND v.version=a.version
		WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiAttachmentVisible+` AND a.status='current'
		  AND (a.page_id::text=ANY($3) OR a.blog_post_id::text=ANY($4)) ORDER BY a.id`, ws, user, pageIDs, postIDs)
	if err != nil {
		return nil, err
	}
	type stored struct {
		attachment wikiExportAttachment
		ref        string
		size       int64
	}
	found, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (stored, error) {
		var value stored
		err := row.Scan(&value.attachment.ID, &value.attachment.PageID, &value.attachment.BlogPostID, &value.attachment.Filename, &value.attachment.MediaType, &value.ref, &value.size)
		return value, err
	})
	if err != nil {
		return nil, err
	}
	attachments := make([]wikiExportAttachment, 0, len(found))
	var total int64
	for _, value := range found {
		if total+value.size <= wikiSpaceExportAttachmentLimit {
			if reader, _, getErr := blobs.Get(ctx, value.ref); getErr == nil {
				content, readErr := io.ReadAll(io.LimitReader(reader, wikiSpaceExportAttachmentLimit-total))
				_ = reader.Close()
				if readErr == nil {
					value.attachment.Content, value.attachment.Included = content, true
					total += int64(len(content))
				}
			}
		}
		attachments = append(attachments, value.attachment)
	}
	return attachments, nil
}

// buildWikiSpaceExport writes a space as a small HTML site in a zip: an index
// linking every page and blog post, a file for each with its body rendered as
// the space shows it and a list of its attachments, and the attachment files
// the export carries.
// wikiSpaceManifest is the machine-readable half of an export: what a space
// holds, in the storage the site keeps, so another site can read it back.
type wikiSpaceManifest struct {
	Version     int                     `json:"version"`
	Key         string                  `json:"key"`
	Name        string                  `json:"name"`
	Description string                  `json:"description,omitempty"`
	Pages       []wikiSpaceManifestPage `json:"pages,omitempty"`
	BlogPosts   []wikiSpaceManifestPage `json:"blogPosts,omitempty"`
}

// wikiSpaceManifestPage is one page or blog post as an export carries it:
// what it says, what it was labelled, what people said under it, and the
// files attached to it.
type wikiSpaceManifestPage struct {
	ID       string `json:"id"`
	ParentID string `json:"parentId,omitempty"`
	Title    string `json:"title"`
	Body     string `json:"body"`
	// Representation is what the body is written in, which is "storage" for
	// everything this site keeps.
	Representation string                        `json:"representation"`
	Labels         []string                      `json:"labels,omitempty"`
	Comments       []wikiSpaceManifestComment    `json:"comments,omitempty"`
	Attachments    []wikiSpaceManifestAttachment `json:"attachments,omitempty"`
	// Restrictions are who may read and who may edit the page, by email and
	// by group name: an account id means nothing on another site.
	Restrictions []wikiSpaceManifestRestriction `json:"restrictions,omitempty"`
	// History is what the page said before its current version, oldest first,
	// so an import can bring the page's past with it.
	History []wikiSpaceManifestVersion `json:"history,omitempty"`
}

// wikiSpaceManifestRestriction is one operation of a page and who may do it.
type wikiSpaceManifestRestriction struct {
	Operation string   `json:"operation"`
	Users     []string `json:"users,omitempty"`
	Groups    []string `json:"groups,omitempty"`
}

// wikiSpaceManifestVersion is one earlier version of a page: what it said,
// who wrote it and when, on the site the export came from.
type wikiSpaceManifestVersion struct {
	Number    int    `json:"number"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	Author    string `json:"author,omitempty"`
	Message   string `json:"message,omitempty"`
	CreatedAt string `json:"createdAt,omitempty"`
	MinorEdit bool   `json:"minorEdit,omitempty"`
}

// wikiSpaceManifestComment is one comment under a page or blog post. Who
// wrote it is carried as a name: an import into another site has nobody to
// attribute it to, so the person importing owns what they bring.
type wikiSpaceManifestComment struct {
	Body   string `json:"body"`
	Author string `json:"author,omitempty"`
	Parent int    `json:"parent,omitempty"`
}

// wikiSpaceManifestAttachment names a file the archive carries, by the path
// it is written at.
type wikiSpaceManifestAttachment struct {
	Path      string `json:"path"`
	Filename  string `json:"filename"`
	MediaType string `json:"mediaType,omitempty"`
}

// wikiSpaceManifestFormat is the shape of space.json this site writes. An
// import reads this version and the ones before it.
const wikiSpaceManifestFormat = 3

// withExtras puts each page's labels, comments and files beside its body.
func withExtras(pages []wikiSpaceManifestPage, extras wikiSpaceExtras, files map[string][]wikiSpaceManifestAttachment) []wikiSpaceManifestPage {
	for i := range pages {
		pages[i].Labels = extras.Labels[pages[i].ID]
		pages[i].Comments = extras.Comments[pages[i].ID]
		pages[i].Attachments = files[pages[i].ID]
		pages[i].Restrictions = extras.Restrictions[pages[i].ID]
		pages[i].History = extras.History[pages[i].ID]
	}
	return pages
}

// manifestPages is a space's pages as an export carries them, parents first
// so an import can raise a page after the page it belongs under.
func manifestPages(pages []*models.WikiPage) []wikiSpaceManifestPage {
	out := make([]wikiSpaceManifestPage, 0, len(pages))
	for _, page := range pages {
		representation := page.Body.Representation
		if representation == "" {
			representation = "storage"
		}
		out = append(out, wikiSpaceManifestPage{
			ID: page.ID, ParentID: page.ParentID, Title: page.Title, Body: page.Body.Value, Representation: representation,
		})
	}
	return out
}

// manifestPosts is the same for blog posts, which have no parent.
func manifestPosts(posts []*models.WikiBlogPost) []wikiSpaceManifestPage {
	out := make([]wikiSpaceManifestPage, 0, len(posts))
	for _, post := range posts {
		representation := post.Body.Representation
		if representation == "" {
			representation = "storage"
		}
		out = append(out, wikiSpaceManifestPage{
			ID: post.ID, Title: post.Title, Body: post.Body.Value, Representation: representation,
		})
	}
	return out
}

// wikiSpaceExtras is what a page or blog post carries beside its body, keyed
// by its id: the labels on it and the comments under it.
type wikiSpaceExtras struct {
	// Restrictions is who may read and edit each page, and History what each
	// page said before now.
	Restrictions map[string][]wikiSpaceManifestRestriction
	History      map[string][]wikiSpaceManifestVersion
	Labels       map[string][]string
	Comments     map[string][]wikiSpaceManifestComment
}

func buildWikiSpaceExport(space *models.WikiSpace, pages []*models.WikiPage, posts []*models.WikiBlogPost, attachments []wikiExportAttachment, extras wikiSpaceExtras) ([]byte, error) {
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	modified := time.Now().UTC()
	document := func(title, body string) string {
		return "<!doctype html>\n<html lang=\"en\"><head><meta charset=\"utf-8\"><title>" + html.EscapeString(title) + "</title></head><body><main><h1>" + html.EscapeString(title) + "</h1>" + body + "</main></body></html>\n"
	}
	write := func(name, content string) error {
		file, err := archive.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: modified})
		if err != nil {
			return err
		}
		_, err = file.Write([]byte(content))
		return err
	}
	rendered := func(storage string) string {
		body, err := wikimarkup.Render(storage)
		if err != nil {
			return "<pre>" + html.EscapeString(storage) + "</pre>"
		}
		return body
	}
	// carried is where each attachment's file ended up, so the manifest can
	// name a file an import can read back.
	carried := map[string]wikiSpaceManifestAttachment{}
	// attached lists a page's or blog post's attachments and writes the files
	// the export carries, named so they stay inside their folder.
	attached := func(pageID, postID string) (string, error) {
		var list strings.Builder
		for _, attachment := range attachments {
			if (pageID == "" || attachment.PageID != pageID) && (postID == "" || attachment.BlogPostID != postID) {
				continue
			}
			label := html.EscapeString(attachment.Filename)
			if !attachment.Included {
				list.WriteString("<li>" + label + " (too large to include in this export)</li>")
				continue
			}
			name := strings.NewReplacer("/", "_", "\\", "_").Replace(strings.TrimSpace(attachment.Filename))
			if name == "" || name == "." || name == ".." {
				name = "attachment"
			}
			path := "attachments/" + attachment.ID + "/" + name
			if err := write(path, string(attachment.Content)); err != nil {
				return "", err
			}
			carried[attachment.ID] = wikiSpaceManifestAttachment{Path: path, Filename: attachment.Filename, MediaType: attachment.MediaType}
			list.WriteString(`<li><a href="../attachments/` + attachment.ID + "/" + url.PathEscape(name) + `">` + label + "</a></li>")
		}
		if list.Len() == 0 {
			return "", nil
		}
		return "<h2>Attachments</h2><ul>" + list.String() + "</ul>", nil
	}
	var index strings.Builder
	index.WriteString("<h2>Pages</h2><ul>")
	for _, page := range pages {
		name := "pages/" + page.ID + ".html"
		files, err := attached(page.ID, "")
		if err != nil {
			return nil, err
		}
		if err := write(name, document(page.Title, rendered(page.Body.Value)+files)); err != nil {
			return nil, err
		}
		index.WriteString(`<li><a href="` + name + `">` + html.EscapeString(page.Title) + "</a></li>")
	}
	index.WriteString("</ul><h2>Blog posts</h2><ul>")
	for _, post := range posts {
		name := "blogposts/" + post.ID + ".html"
		files, err := attached("", post.ID)
		if err != nil {
			return nil, err
		}
		if err := write(name, document(post.Title, rendered(post.Body.Value)+files)); err != nil {
			return nil, err
		}
		index.WriteString(`<li><a href="` + name + `">` + html.EscapeString(post.Title) + "</a></li>")
	}
	index.WriteString("</ul>")
	if err := write("index.html", document(space.Name, "<p>"+html.EscapeString(space.Description)+"</p>"+index.String())); err != nil {
		return nil, err
	}
	// The HTML is for reading; space.json is for moving. An import reads the
	// manifest, because rendered HTML cannot be turned back into the storage
	// the site keeps without losing what it was.
	// Files of a page, by page id, from what was actually written above.
	files := map[string][]wikiSpaceManifestAttachment{}
	for _, attachment := range attachments {
		file, ok := carried[attachment.ID]
		if !ok {
			continue
		}
		owner := attachment.PageID
		if owner == "" {
			owner = attachment.BlogPostID
		}
		files[owner] = append(files[owner], file)
	}
	manifest, err := json.Marshal(wikiSpaceManifest{
		Version: wikiSpaceManifestFormat, Key: space.Key, Name: space.Name, Description: space.Description,
		Pages:     withExtras(manifestPages(pages), extras, files),
		BlogPosts: withExtras(manifestPosts(posts), extras, files),
	})
	if err != nil {
		return nil, err
	}
	if err := write("space.json", string(manifest)); err != nil {
		return nil, err
	}
	if err := archive.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}
