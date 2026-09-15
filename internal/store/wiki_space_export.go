package store

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
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
func (s *Store) executeWikiSpaceExport(ctx context.Context, task APITask) error {
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
	content, err := buildWikiSpaceExport(space, pages, posts)
	if err != nil {
		return err
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
			fmt.Sprintf("The HTML export of %d pages and %d blog posts is ready to download:\n%s", len(pages), len(posts), path), "wiki-space-export:"+task.ID); err != nil {
			return err
		}
	}
	return s.CompleteAPITask(ctx, task, fmt.Sprintf("Exported %d pages and %d blog posts.", len(pages), len(posts)), map[string]any{"fileUrl": path, "pageCount": len(pages), "blogPostCount": len(posts)})
}

// buildWikiSpaceExport writes a space as a small HTML site in a zip: an index
// linking every page and blog post, and a file for each with its body rendered
// as the space shows it.
func buildWikiSpaceExport(space *models.WikiSpace, pages []*models.WikiPage, posts []*models.WikiBlogPost) ([]byte, error) {
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
	var index strings.Builder
	index.WriteString("<h2>Pages</h2><ul>")
	for _, page := range pages {
		name := "pages/" + page.ID + ".html"
		if err := write(name, document(page.Title, rendered(page.Body.Value))); err != nil {
			return nil, err
		}
		index.WriteString(`<li><a href="` + name + `">` + html.EscapeString(page.Title) + "</a></li>")
	}
	index.WriteString("</ul><h2>Blog posts</h2><ul>")
	for _, post := range posts {
		name := "blogposts/" + post.ID + ".html"
		if err := write(name, document(post.Title, rendered(post.Body.Value))); err != nil {
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
