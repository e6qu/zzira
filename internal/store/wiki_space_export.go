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
	content, err := buildWikiSpaceExport(space, pages, posts, attachments)
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

// wikiSpaceExportAttachmentLimit caps the attachment bytes one export carries;
// attachments past it are listed without their files.
const wikiSpaceExportAttachmentLimit = 100 << 20

// wikiExportAttachment is a current attachment of an exported page or blog
// post, with its file when the export carries it.
type wikiExportAttachment struct {
	ID, PageID, BlogPostID, Filename string
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
	rows, err := s.Pool.Query(ctx, `SELECT a.id::text,COALESCE(a.page_id::text,''),COALESCE(a.blog_post_id::text,''),a.filename,v.blob_ref,a.size
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
		err := row.Scan(&value.attachment.ID, &value.attachment.PageID, &value.attachment.BlogPostID, &value.attachment.Filename, &value.ref, &value.size)
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
func buildWikiSpaceExport(space *models.WikiSpace, pages []*models.WikiPage, posts []*models.WikiBlogPost, attachments []wikiExportAttachment) ([]byte, error) {
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
			if err := write("attachments/"+attachment.ID+"/"+name, string(attachment.Content)); err != nil {
				return "", err
			}
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
	if err := archive.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}
