package store

import (
	"context"
	"encoding/json"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

const wikiLabelSelect = `SELECT l.id::text,l.name,l.prefix,to_char(l.created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"') FROM wiki_labels l`

func scanWikiLabels(rows pgx.Rows) ([]models.WikiLabel, error) {
	labels := []models.WikiLabel{}
	for rows.Next() {
		var label models.WikiLabel
		if err := rows.Scan(&label.ID, &label.Name, &label.Prefix, &label.CreatedAt); err != nil {
			return nil, err
		}
		labels = append(labels, label)
	}
	return labels, rows.Err()
}

func (s *Store) WikiSpaceByKey(ctx context.Context, ws, user, key string) (*models.WikiSpace, error) {
	return scanWikiSpace(s.Pool.QueryRow(ctx, wikiSpaceSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND s.key=$3`, ws, user, key))
}

func (s *Store) WikiLabels(ctx context.Context, ws, user string) ([]models.WikiLabel, error) {
	rows, err := s.Pool.Query(ctx, wikiLabelSelect+` WHERE l.workspace_id=$1 AND (
		EXISTS (SELECT 1 FROM wiki_page_labels pl JOIN wiki_pages p ON p.id=pl.page_id JOIN wiki_spaces s ON s.id=p.space_id WHERE pl.label_id=l.id AND `+wikiSpaceVisible+` AND `+wikiPageVisible+` AND p.status='current')
		OR EXISTS (SELECT 1 FROM wiki_blog_post_labels bl JOIN wiki_blog_posts b ON b.id=bl.blog_post_id JOIN wiki_spaces s ON s.id=b.space_id WHERE bl.label_id=l.id AND `+wikiSpaceVisible+` AND `+wikiBlogPostVisible+` AND b.status='current')
		OR EXISTS (SELECT 1 FROM wiki_space_labels sl JOIN wiki_spaces s ON s.id=sl.space_id WHERE sl.label_id=l.id AND `+wikiSpaceVisible+`)
		OR EXISTS (SELECT 1 FROM wiki_attachment_labels al JOIN wiki_attachments a ON a.id=al.attachment_id JOIN wiki_pages p ON p.id=a.page_id JOIN wiki_spaces s ON s.id=p.space_id WHERE al.label_id=l.id AND `+wikiSpaceVisible+` AND `+wikiPageVisible+` AND a.status='current')
	) ORDER BY l.created_at,l.id`, ws, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanWikiLabels(rows)
}

func (s *Store) WikiPageLabels(ctx context.Context, ws, user, pageID string) ([]models.WikiLabel, error) {
	if _, err := s.WikiPage(ctx, ws, user, pageID); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, wikiLabelSelect+` JOIN wiki_page_labels pl ON pl.label_id=l.id WHERE pl.page_id::text=$1 ORDER BY l.created_at,l.id`, pageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanWikiLabels(rows)
}

func (s *Store) WikiSpaceLabels(ctx context.Context, ws, user, spaceID string, content bool) ([]models.WikiLabel, error) {
	if _, err := s.WikiSpace(ctx, ws, user, spaceID); err != nil {
		return nil, err
	}
	query := wikiLabelSelect + ` JOIN wiki_space_labels sl ON sl.label_id=l.id WHERE sl.space_id::text=$1 ORDER BY l.created_at,l.id`
	args := []any{spaceID}
	if content {
		query = wikiLabelSelect + ` WHERE EXISTS (SELECT 1 FROM wiki_page_labels pl JOIN wiki_pages p ON p.id=pl.page_id JOIN wiki_spaces s ON s.id=p.space_id WHERE pl.label_id=l.id AND p.space_id::text=$1 AND p.status='current' AND ` + wikiPageVisible + `) OR EXISTS (SELECT 1 FROM wiki_blog_post_labels bl JOIN wiki_blog_posts b ON b.id=bl.blog_post_id JOIN wiki_spaces s ON s.id=b.space_id WHERE bl.label_id=l.id AND b.space_id::text=$1 AND b.status='current' AND ` + wikiBlogPostVisible + `) OR EXISTS (SELECT 1 FROM wiki_attachment_labels al JOIN wiki_attachments a ON a.id=al.attachment_id JOIN wiki_pages p ON p.id=a.page_id JOIN wiki_spaces s ON s.id=p.space_id WHERE al.label_id=l.id AND p.space_id::text=$1 AND p.status='current' AND a.status='current' AND ` + wikiPageVisible + `) ORDER BY l.created_at,l.id`
		args = append(args, user)
	}
	rows, err := s.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanWikiLabels(rows)
}

func (s *Store) WikiPagesByLabel(ctx context.Context, ws, user, labelID string) ([]*models.WikiPage, error) {
	labels, err := s.WikiLabels(ctx, ws, user)
	if err != nil {
		return nil, err
	}
	found := false
	for _, label := range labels {
		if label.ID == labelID {
			found = true
			break
		}
	}
	if !found {
		return nil, pgx.ErrNoRows
	}
	rows, err := s.Pool.Query(ctx, wikiPageSelect+` JOIN wiki_page_labels pl ON pl.page_id=p.id WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiPageVisible+` AND p.status='current' AND pl.label_id::text=$3 ORDER BY p.id`, ws, user, labelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	pages := []*models.WikiPage{}
	for rows.Next() {
		page, err := scanWikiPage(rows)
		if err != nil {
			return nil, err
		}
		pages = append(pages, page)
	}
	return pages, rows.Err()
}

func wikiLabelAction(ctx context.Context, tx pgx.Tx, ws, actor, spaceID, entityID string, label models.WikiLabel, pageID string, attached bool) error {
	seq, err := nextSeq(ctx, tx, ws)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{"wikiSpaceId": spaceID, "wiki_label": map[string]any{"id": label.ID, "name": label.Name, "prefix": label.Prefix, "pageId": pageID, "attached": attached}})
	if err != nil {
		return err
	}
	op := models.OpUpsert
	if !attached {
		op = models.OpDelete
	}
	return appendAction(ctx, tx, &models.Action{WorkspaceID: ws, Seq: seq, EntityType: "wiki_label", EntityID: entityID, Op: op, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actor})
}

func ensureWikiLabel(ctx context.Context, tx pgx.Tx, ws string, label models.WikiLabel) (models.WikiLabel, error) {
	err := tx.QueryRow(ctx, `INSERT INTO wiki_labels(workspace_id,prefix,name) VALUES ($1,$2,$3) ON CONFLICT(workspace_id,prefix,name) DO UPDATE SET name=EXCLUDED.name RETURNING id::text,name,prefix,to_char(created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"')`, ws, label.Prefix, label.Name).Scan(&label.ID, &label.Name, &label.Prefix, &label.CreatedAt)
	return label, err
}

func (s *Store) AddWikiPageLabels(ctx context.Context, ws, actor, pageID string, input []models.WikiLabel) ([]models.WikiLabel, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var spaceID string
	if err := tx.QueryRow(ctx, `SELECT p.space_id::text FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiPageVisible+` AND `+wikiPageWritable+` AND p.status='current' AND p.id::text=$3 FOR SHARE OF p`, ws, actor, pageID).Scan(&spaceID); err != nil {
		return nil, err
	}
	for _, item := range input {
		label, err := ensureWikiLabel(ctx, tx, ws, item)
		if err != nil {
			return nil, err
		}
		tag, err := tx.Exec(ctx, `INSERT INTO wiki_page_labels(page_id,label_id,author_id) VALUES ($1::bigint,$2::bigint,$3) ON CONFLICT DO NOTHING`, pageID, label.ID, actor)
		if err != nil {
			return nil, err
		}
		if tag.RowsAffected() > 0 {
			if err := wikiLabelAction(ctx, tx, ws, actor, spaceID, pageID+":"+label.ID, label, pageID, true); err != nil {
				return nil, err
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.WikiPageLabels(ctx, ws, actor, pageID)
}

func (s *Store) RemoveWikiPageLabel(ctx context.Context, ws, actor, pageID string, input models.WikiLabel) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var spaceID string
	var label models.WikiLabel
	err = tx.QueryRow(ctx, `SELECT p.space_id::text,l.id::text,l.name,l.prefix,to_char(l.created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"') FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id JOIN wiki_page_labels pl ON pl.page_id=p.id JOIN wiki_labels l ON l.id=pl.label_id WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiPageVisible+` AND `+wikiPageWritable+` AND p.status='current' AND p.id::text=$3 AND l.prefix=$4 AND l.name=$5 FOR UPDATE OF pl`, ws, actor, pageID, input.Prefix, input.Name).Scan(&spaceID, &label.ID, &label.Name, &label.Prefix, &label.CreatedAt)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM wiki_page_labels WHERE page_id::text=$1 AND label_id::text=$2`, pageID, label.ID); err != nil {
		return err
	}
	if err := wikiLabelAction(ctx, tx, ws, actor, spaceID, pageID+":"+label.ID, label, pageID, false); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) AddWikiSpaceLabels(ctx context.Context, ws, actor, spaceID string, input []models.WikiLabel) ([]models.WikiLabel, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := projectAdmin(ctx, tx, ws, actor); err != nil {
		return nil, err
	}
	if _, err := scanWikiSpace(tx.QueryRow(ctx, wikiSpaceSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND s.id::text=$3 FOR SHARE OF s`, ws, actor, spaceID)); err != nil {
		return nil, err
	}
	for _, item := range input {
		label, err := ensureWikiLabel(ctx, tx, ws, item)
		if err != nil {
			return nil, err
		}
		tag, err := tx.Exec(ctx, `INSERT INTO wiki_space_labels(space_id,label_id,author_id) VALUES ($1::bigint,$2::bigint,$3) ON CONFLICT DO NOTHING`, spaceID, label.ID, actor)
		if err != nil {
			return nil, err
		}
		if tag.RowsAffected() > 0 {
			if err := wikiLabelAction(ctx, tx, ws, actor, spaceID, "space:"+spaceID+":"+label.ID, label, "", true); err != nil {
				return nil, err
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.WikiSpaceLabels(ctx, ws, actor, spaceID, false)
}

func (s *Store) RemoveWikiSpaceLabel(ctx context.Context, ws, actor, spaceID string, input models.WikiLabel) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := projectAdmin(ctx, tx, ws, actor); err != nil {
		return err
	}
	var label models.WikiLabel
	err = tx.QueryRow(ctx, `SELECT l.id::text,l.name,l.prefix,to_char(l.created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"') FROM wiki_space_labels sl JOIN wiki_spaces s ON s.id=sl.space_id JOIN wiki_labels l ON l.id=sl.label_id WHERE s.workspace_id=$1 AND s.id::text=$2 AND l.prefix=$3 AND l.name=$4 AND (NOT s.private OR s.author_id=$5) FOR UPDATE OF sl`, ws, spaceID, input.Prefix, input.Name, actor).Scan(&label.ID, &label.Name, &label.Prefix, &label.CreatedAt)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM wiki_space_labels WHERE space_id::text=$1 AND label_id::text=$2`, spaceID, label.ID); err != nil {
		return err
	}
	if err := wikiLabelAction(ctx, tx, ws, actor, spaceID, "space:"+spaceID+":"+label.ID, label, "", false); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
