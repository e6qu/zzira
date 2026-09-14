package store

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"time"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/wikimarkup"
	"github.com/jackc/pgx/v5"
)

// Tasks live in the bodies of pages and blog posts as Confluence task lists.
// The task store is what those bodies say: saving a body adds the tasks it
// gained, updates the ones it changed and removes the ones it lost, and
// completing a task ticks it in the body.

type WikiTaskFilter struct {
	TaskIDs, SpaceIDs, PageIDs, BlogPostIDs []string
	CreatedBy, AssignedTo, CompletedBy      []string
	Status                                  string
	IncludeBlank                            bool
	CreatedFrom, CreatedTo                  *time.Time
	DueFrom, DueTo                          *time.Time
	CompletedFrom, CompletedTo              *time.Time
}

const wikiTaskSelect = `SELECT t.id::text,t.local_id,s.id::text,COALESCE(p.id::text,''),COALESCE(b.id::text,''),t.status,t.body,
  t.created_by,creator.display_name,COALESCE(t.assigned_to,''),COALESCE(assigned.display_name,''),
  COALESCE(t.completed_by,''),COALESCE(completed.display_name,''),
  to_char(t.created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.MS"Z"'),
  to_char(t.updated_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.MS"Z"'),
  COALESCE(to_char(t.due_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.MS"Z"'),''),
  COALESCE(to_char(t.completed_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.MS"Z"'),'')
  FROM wiki_tasks t
  LEFT JOIN wiki_pages p ON p.id=t.page_id
  LEFT JOIN wiki_blog_posts b ON b.id=t.blog_post_id
  JOIN wiki_spaces s ON s.id=COALESCE(p.space_id,b.space_id)
  JOIN users creator ON creator.id=t.created_by
  LEFT JOIN users assigned ON assigned.id=t.assigned_to
  LEFT JOIN users completed ON completed.id=t.completed_by`

// A task is as visible as the published page or blog post it is in, and
// changing it takes permission to edit that page or post.
var wikiTaskVisible = `((p.id IS NOT NULL AND p.status='current' AND ` + wikiPageVisible + `) OR (b.id IS NOT NULL AND b.status='current' AND ` + wikiBlogPostVisible + `))`

var wikiTaskWritable = `((p.id IS NOT NULL AND p.status='current' AND ` + wikiPageVisible + ` AND ` + wikiPageWritable + `) OR (b.id IS NOT NULL AND b.status='current' AND ` + wikiBlogPostVisible + ` AND ` + wikiBlogPostWritable + `))`

func scanWikiTask(row pgx.Row) (*models.WikiTask, error) {
	task := &models.WikiTask{Body: models.WikiBody{Representation: "storage"}}
	err := row.Scan(&task.ID, &task.LocalID, &task.SpaceID, &task.PageID, &task.BlogPostID, &task.Status,
		&task.Body.Value, &task.CreatedBy, &task.CreatedName, &task.AssignedTo,
		&task.AssignedName, &task.CompletedBy, &task.CompletedName, &task.CreatedAt,
		&task.UpdatedAt, &task.DueAt, &task.CompletedAt)
	return task, err
}

func scanWikiTasks(rows pgx.Rows) ([]*models.WikiTask, error) {
	var tasks []*models.WikiTask
	for rows.Next() {
		task, err := scanWikiTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}

func (s *Store) WikiTask(ctx context.Context, ws, user, id string) (*models.WikiTask, error) {
	return scanWikiTask(s.Pool.QueryRow(ctx, wikiTaskSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiTaskVisible+` AND t.id::text=$3`, ws, user, id))
}

func (s *Store) WikiTasks(ctx context.Context, ws, user string, filter WikiTaskFilter) ([]*models.WikiTask, error) {
	rows, err := s.Pool.Query(ctx, wikiTaskSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiTaskVisible+`
    AND (COALESCE(cardinality($3::text[]),0)=0 OR t.id::text=ANY($3::text[]))
    AND (COALESCE(cardinality($4::text[]),0)=0 OR s.id::text=ANY($4::text[]))
    AND ((COALESCE(cardinality($5::text[]),0)=0 AND COALESCE(cardinality($17::text[]),0)=0) OR p.id::text=ANY($5::text[]) OR b.id::text=ANY($17::text[]))
    AND (COALESCE(cardinality($6::text[]),0)=0 OR t.created_by=ANY($6::text[]))
    AND (COALESCE(cardinality($7::text[]),0)=0 OR t.assigned_to=ANY($7::text[]))
    AND (COALESCE(cardinality($8::text[]),0)=0 OR t.completed_by=ANY($8::text[]))
    AND ($9='' OR t.status=$9)
    AND ($10 OR btrim(regexp_replace(t.body,'<[^>]*>','','g'))<>'')
    AND ($11::timestamptz IS NULL OR t.created_at >= $11)
    AND ($12::timestamptz IS NULL OR t.created_at <= $12)
    AND ($13::timestamptz IS NULL OR t.due_at >= $13)
    AND ($14::timestamptz IS NULL OR t.due_at <= $14)
    AND ($15::timestamptz IS NULL OR t.completed_at >= $15)
    AND ($16::timestamptz IS NULL OR t.completed_at <= $16)
    ORDER BY t.created_at,t.id`, ws, user, filter.TaskIDs, filter.SpaceIDs, filter.PageIDs,
		filter.CreatedBy, filter.AssignedTo, filter.CompletedBy, filter.Status, filter.IncludeBlank,
		filter.CreatedFrom, filter.CreatedTo, filter.DueFrom, filter.DueTo, filter.CompletedFrom, filter.CompletedTo, filter.BlogPostIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanWikiTasks(rows)
}

// wikiBodyChanged brings what hangs off a body into line with a new version
// of it: inline comments follow their passages and the task store follows
// the task lists.
func wikiBodyChanged(ctx context.Context, tx pgx.Tx, ws, actor, contentType, contentID, body string) error {
	if err := relocateInlineComments(ctx, tx, ws, actor, contentType, contentID, body); err != nil {
		return err
	}
	return reconcileWikiTasks(ctx, tx, ws, actor, contentType, contentID, body)
}

type storedWikiTask struct {
	id, status, body, assignee, due string
}

// reconcileWikiTasks makes the task store say what a page or blog post body
// says. The person a task mentions first is assigned to it if they belong to
// the site, and its first date is when it is due. A task that becomes
// complete is completed by whoever saved the body.
func reconcileWikiTasks(ctx context.Context, tx pgx.Tx, ws, actor, contentType, contentID, body string) error {
	parsed, err := wikimarkup.Tasks(body)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrWikiValidation, err)
	}
	column := "page_id"
	if contentType == "blogpost" {
		column = "blog_post_id"
	}
	rows, err := tx.Query(ctx, `SELECT id::text,local_id,status,body,COALESCE(assigned_to,''),COALESCE(to_char(due_at AT TIME ZONE 'UTC','YYYY-MM-DD'),'')
		FROM wiki_tasks WHERE `+column+`::text=$1 FOR UPDATE`, contentID)
	if err != nil {
		return err
	}
	stored := map[string]storedWikiTask{}
	for rows.Next() {
		var localID string
		var task storedWikiTask
		if err := rows.Scan(&task.id, &localID, &task.status, &task.body, &task.assignee, &task.due); err != nil {
			rows.Close()
			return err
		}
		stored[localID] = task
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	var changed []string
	inBody := map[string]bool{}
	for _, task := range parsed {
		inBody[task.ID] = true
		assignee := ""
		if task.Assignee != "" {
			var member bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM memberships m JOIN users u ON u.id=m.user_id AND u.active WHERE m.workspace_id=$1 AND m.user_id=$2)`, ws, task.Assignee).Scan(&member); err != nil {
				return err
			}
			if member {
				assignee = task.Assignee
			}
		}
		due := ""
		if _, err := time.Parse("2006-01-02", task.Due); err == nil {
			due = task.Due
		}
		old, found := stored[task.ID]
		if !found {
			var id string
			if err := tx.QueryRow(ctx, `INSERT INTO wiki_tasks(`+column+`,local_id,body,status,created_by,assigned_to,due_at,completed_by,completed_at)
				VALUES($1::bigint,$2,$3,$4,$5,NULLIF($6,''),CASE WHEN $7='' THEN NULL ELSE ($7||' 00:00:00+00')::timestamptz END,
				  CASE WHEN $4='complete' THEN $5 END,CASE WHEN $4='complete' THEN now() END) RETURNING id::text`,
				contentID, task.ID, task.Body, task.Status, actor, assignee, due).Scan(&id); err != nil {
				return err
			}
			changed = append(changed, id)
			continue
		}
		if old.status == task.Status && old.body == task.Body && old.assignee == assignee && old.due == due {
			continue
		}
		if _, err := tx.Exec(ctx, `UPDATE wiki_tasks SET body=$2,assigned_to=NULLIF($3,''),
			due_at=CASE WHEN $4='' THEN NULL ELSE ($4||' 00:00:00+00')::timestamptz END,
			completed_by=CASE WHEN $5='complete' AND status='complete' THEN completed_by WHEN $5='complete' THEN $6 END,
			completed_at=CASE WHEN $5='complete' AND status='complete' THEN completed_at WHEN $5='complete' THEN now() END,
			status=$5,updated_at=now() WHERE id::text=$1`, old.id, task.Body, assignee, due, task.Status, actor); err != nil {
			return err
		}
		changed = append(changed, old.id)
	}
	for localID, old := range stored {
		if inBody[localID] {
			continue
		}
		task, err := scanWikiTask(tx.QueryRow(ctx, wikiTaskSelect+` WHERE t.id::text=$1`, old.id))
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM wiki_tasks WHERE id::text=$1`, old.id); err != nil {
			return err
		}
		if err := wikiTaskAction(ctx, tx, ws, actor, task, models.OpDelete); err != nil {
			return err
		}
	}
	for _, id := range changed {
		task, err := scanWikiTask(tx.QueryRow(ctx, wikiTaskSelect+` WHERE t.id::text=$1`, id))
		if err != nil {
			return err
		}
		if err := wikiTaskAction(ctx, tx, ws, actor, task, models.OpUpsert); err != nil {
			return err
		}
	}
	return nil
}

// wikiTaskAction records a task change for replicas, carrying what decides
// who may see a task on a blog post.
func wikiTaskAction(ctx context.Context, tx pgx.Tx, ws, actor string, task *models.WikiTask, op string) error {
	body := map[string]any{"wikiSpaceId": task.SpaceID, "wiki_task": task}
	if task.BlogPostID != "" {
		var published, private bool
		var authorID string
		if err := tx.QueryRow(ctx, `SELECT published,private,author_id FROM wiki_blog_posts WHERE id::text=$1`, task.BlogPostID).Scan(&published, &private, &authorID); err != nil {
			return err
		}
		body["blogPublished"], body["blogPrivate"], body["blogAuthorId"] = published, private, authorID
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	seq, err := nextSeq(ctx, tx, ws)
	if err != nil {
		return err
	}
	return appendAction(ctx, tx, &models.Action{WorkspaceID: ws, Seq: seq, EntityType: "wiki_task", EntityID: task.ID, Op: op, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actor})
}

// CreateWikiTask adds a task to the end of a page as a new version of it: the
// task, its assignee and its due date are written into the body, where
// Confluence keeps them.
func (s *Store) CreateWikiTask(ctx context.Context, ws, actor string, input models.WikiTask) (*models.WikiTask, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	page, err := scanWikiPage(tx.QueryRow(ctx, wikiPageSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiPageVisible+` AND `+wikiPageWritable+` AND p.status='current' AND p.id::text=$3 FOR UPDATE OF p`, ws, actor, input.PageID))
	if err != nil {
		return nil, err
	}
	taskBody := input.Body.Value
	if input.AssignedTo != "" {
		var member bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM memberships m JOIN users u ON u.id=m.user_id AND u.active WHERE m.workspace_id=$1 AND m.user_id=$2)`, ws, input.AssignedTo).Scan(&member); err != nil {
			return nil, err
		}
		if !member {
			return nil, fmt.Errorf("%w: task assignee must be an active workspace member", ErrWikiValidation)
		}
		taskBody += ` <ac:link><ri:user ri:account-id="` + html.EscapeString(input.AssignedTo) + `" /></ac:link>`
	}
	if input.DueAt != "" {
		due, err := time.Parse(time.RFC3339, input.DueAt)
		if err != nil {
			return nil, fmt.Errorf("%w: task due date must be RFC 3339", ErrWikiValidation)
		}
		taskBody += ` <time datetime="` + due.UTC().Format("2006-01-02") + `" />`
	}
	existing, err := wikimarkup.Tasks(page.Body.Value)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrWikiValidation, err)
	}
	localID := wikimarkup.NextTaskID(existing)
	previousBody := page.Body.Value
	body := previousBody + wikimarkup.TaskList(localID, taskBody)
	if _, err := wikimarkup.Render(body); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrWikiValidation, err)
	}
	version := page.Version.Number + 1
	if _, err := tx.Exec(ctx, `UPDATE wiki_pages SET body=$2,version=$3 WHERE id::text=$1`, page.ID, body, version); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO wiki_page_versions(page_id,version,title,body,status,author_id,message,minor_edit) VALUES ($1::bigint,$2,$3,$4,'current',$5,'Added a task',false)`, page.ID, version, page.Title, body, actor); err != nil {
		return nil, err
	}
	if err := reconcileWikiTasks(ctx, tx, ws, actor, "page", page.ID, body); err != nil {
		return nil, err
	}
	if err := notifyWikiMentions(ctx, tx, ws, actor, "wiki_page", page.ID, page.Title, wikiPageReadableBy, page.ID, previousBody, body); err != nil {
		return nil, err
	}
	saved, err := scanWikiPage(tx.QueryRow(ctx, wikiPageSelect+` WHERE p.id::text=$1`, page.ID))
	if err != nil {
		return nil, err
	}
	if err := wikiAction(ctx, tx, ws, actor, "wiki_page", saved.ID, saved.SpaceID, saved); err != nil {
		return nil, err
	}
	if err := wikiWatchNotifications(ctx, tx, ws, actor, saved, false); err != nil {
		return nil, err
	}
	task, err := scanWikiTask(tx.QueryRow(ctx, wikiTaskSelect+` WHERE t.page_id::text=$1 AND t.local_id=$2`, page.ID, localID))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return task, nil
}

// UpdateWikiTask completes or reopens a task by ticking it in its page or blog
// post. Ticking a task changes the body in place rather than making a new
// version.
func (s *Store) UpdateWikiTask(ctx context.Context, ws, actor, id, status string) (*models.WikiTask, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	current, err := scanWikiTask(tx.QueryRow(ctx, wikiTaskSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiTaskWritable+` AND t.id::text=$3 FOR UPDATE OF t`, ws, actor, id))
	if err != nil {
		return nil, err
	}
	if current.Status == status {
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return current, nil
	}
	contentType, contentID, table, versions, column := "page", current.PageID, "wiki_pages", "wiki_page_versions", "page_id"
	if current.BlogPostID != "" {
		contentType, contentID, table, versions, column = "blogpost", current.BlogPostID, "wiki_blog_posts", "wiki_blog_post_versions", "blog_post_id"
	}
	var body string
	if err := tx.QueryRow(ctx, `SELECT body FROM `+table+` WHERE id::text=$1 FOR UPDATE`, contentID).Scan(&body); err != nil {
		return nil, err
	}
	updated, err := wikimarkup.SetTaskStatus(body, current.LocalID, status)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrWikiValidation, err)
	}
	if _, err := tx.Exec(ctx, `UPDATE `+table+` SET body=$2 WHERE id::text=$1`, contentID, updated); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE `+versions+` SET body=$2 WHERE `+column+`::text=$1 AND version=(SELECT version FROM `+table+` WHERE id::text=$1)`, contentID, updated); err != nil {
		return nil, err
	}
	if err := reconcileWikiTasks(ctx, tx, ws, actor, contentType, contentID, updated); err != nil {
		return nil, err
	}
	if contentType == "page" {
		page, err := scanWikiPage(tx.QueryRow(ctx, wikiPageSelect+` WHERE p.id::text=$1`, contentID))
		if err != nil {
			return nil, err
		}
		if err := wikiAction(ctx, tx, ws, actor, "wiki_page", page.ID, page.SpaceID, page); err != nil {
			return nil, err
		}
	} else {
		post, err := scanWikiBlogPost(tx.QueryRow(ctx, wikiBlogPostSelect+` WHERE b.id::text=$1`, contentID))
		if err != nil {
			return nil, err
		}
		if err := wikiAction(ctx, tx, ws, actor, "wiki_blogpost", post.ID, post.SpaceID, post); err != nil {
			return nil, err
		}
	}
	task, err := scanWikiTask(tx.QueryRow(ctx, wikiTaskSelect+` WHERE t.id::text=$1`, id))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return task, nil
}
