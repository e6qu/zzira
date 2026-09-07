package store

import (
	"context"
	"fmt"
	"time"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

type WikiTaskFilter struct {
	TaskIDs, SpaceIDs, PageIDs         []string
	CreatedBy, AssignedTo, CompletedBy []string
	Status                             string
	IncludeBlank                       bool
	CreatedFrom, CreatedTo             *time.Time
	DueFrom, DueTo                     *time.Time
	CompletedFrom, CompletedTo         *time.Time
}

const wikiTaskSelect = `SELECT t.id::text,t.local_id,s.id::text,p.id::text,t.status,t.body,
  t.created_by,creator.display_name,COALESCE(t.assigned_to,''),COALESCE(assigned.display_name,''),
  COALESCE(t.completed_by,''),COALESCE(completed.display_name,''),
  to_char(t.created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.MS"Z"'),
  to_char(t.updated_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.MS"Z"'),
  COALESCE(to_char(t.due_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.MS"Z"'),''),
  COALESCE(to_char(t.completed_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.MS"Z"'),'')
  FROM wiki_tasks t
  JOIN wiki_pages p ON p.id=t.page_id
  JOIN wiki_spaces s ON s.id=p.space_id
  JOIN users creator ON creator.id=t.created_by
  LEFT JOIN users assigned ON assigned.id=t.assigned_to
  LEFT JOIN users completed ON completed.id=t.completed_by`

func scanWikiTask(row pgx.Row) (*models.WikiTask, error) {
	task := &models.WikiTask{Body: models.WikiBody{Representation: "storage"}}
	err := row.Scan(&task.ID, &task.LocalID, &task.SpaceID, &task.PageID, &task.Status,
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
	return scanWikiTask(s.Pool.QueryRow(ctx, wikiTaskSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiPageVisible+` AND p.status='current' AND t.id::text=$3`, ws, user, id))
}

func (s *Store) WikiTasks(ctx context.Context, ws, user string, filter WikiTaskFilter) ([]*models.WikiTask, error) {
	rows, err := s.Pool.Query(ctx, wikiTaskSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiPageVisible+` AND p.status='current'
    AND (COALESCE(cardinality($3::text[]),0)=0 OR t.id::text=ANY($3::text[]))
    AND (COALESCE(cardinality($4::text[]),0)=0 OR s.id::text=ANY($4::text[]))
    AND (COALESCE(cardinality($5::text[]),0)=0 OR p.id::text=ANY($5::text[]))
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
		filter.CreatedFrom, filter.CreatedTo, filter.DueFrom, filter.DueTo, filter.CompletedFrom, filter.CompletedTo)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanWikiTasks(rows)
}

func wikiTaskAssignee(ctx context.Context, tx pgx.Tx, ws, assignedTo string) error {
	if assignedTo == "" {
		return nil
	}
	var valid bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM memberships m JOIN users u ON u.id=m.user_id AND u.active WHERE m.workspace_id=$1 AND m.user_id=$2)`, ws, assignedTo).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return fmt.Errorf("%w: task assignee must be an active workspace member", ErrWikiValidation)
	}
	return nil
}

func (s *Store) CreateWikiTask(ctx context.Context, ws, actor string, input models.WikiTask) (*models.WikiTask, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var spaceID string
	if err := tx.QueryRow(ctx, `SELECT s.id::text FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiPageVisible+` AND `+wikiPageWritable+` AND p.status='current' AND p.id::text=$3 FOR SHARE OF p`, ws, actor, input.PageID).Scan(&spaceID); err != nil {
		return nil, err
	}
	if err := wikiTaskAssignee(ctx, tx, ws, input.AssignedTo); err != nil {
		return nil, err
	}
	if err := tx.QueryRow(ctx, `INSERT INTO wiki_tasks(page_id,local_id,body,created_by,assigned_to,due_at) VALUES($1::bigint,$2,$3,$4,NULLIF($5,''),NULLIF($6,'')::timestamptz) RETURNING id::text`, input.PageID, NewID("local"), input.Body.Value, actor, input.AssignedTo, input.DueAt).Scan(&input.ID); err != nil {
		return nil, err
	}
	input.LocalID = input.ID
	if _, err := tx.Exec(ctx, `UPDATE wiki_tasks SET local_id=$2 WHERE id::text=$1`, input.ID, input.LocalID); err != nil {
		return nil, err
	}
	task, err := scanWikiTask(tx.QueryRow(ctx, wikiTaskSelect+` WHERE t.id::text=$1`, input.ID))
	if err != nil {
		return nil, err
	}
	if err := wikiAction(ctx, tx, ws, actor, "wiki_task", task.ID, spaceID, task); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return task, nil
}

func (s *Store) UpdateWikiTask(ctx context.Context, ws, actor, id, status string) (*models.WikiTask, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	current, err := scanWikiTask(tx.QueryRow(ctx, wikiTaskSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiPageVisible+` AND `+wikiPageWritable+` AND p.status='current' AND t.id::text=$3 FOR UPDATE OF t`, ws, actor, id))
	if err != nil {
		return nil, err
	}
	if current.Status == status {
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return current, nil
	}
	if _, err := tx.Exec(ctx, `UPDATE wiki_tasks SET status=$2,updated_at=now(),completed_by=CASE WHEN $2='complete' THEN $3 ELSE NULL END,completed_at=CASE WHEN $2='complete' THEN now() ELSE NULL END WHERE id::text=$1`, id, status, actor); err != nil {
		return nil, err
	}
	task, err := scanWikiTask(tx.QueryRow(ctx, wikiTaskSelect+` WHERE t.id::text=$1`, id))
	if err != nil {
		return nil, err
	}
	if err := wikiAction(ctx, tx, ws, actor, "wiki_task", task.ID, task.SpaceID, task); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return task, nil
}
