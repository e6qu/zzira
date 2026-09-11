package store

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/wikimarkup"
	"github.com/jackc/pgx/v5"
)

func pageGovernanceAction(ctx context.Context, tx pgx.Tx, ws, actor, entityType, entityID, op string, page *models.WikiPage, value any) error {
	seq, err := nextSeq(ctx, tx, ws)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{"wikiSpaceId": page.SpaceID, "wiki_page": page, entityType: value})
	if err != nil {
		return err
	}
	return appendAction(ctx, tx, &models.Action{WorkspaceID: ws, Seq: seq, EntityType: entityType, EntityID: entityID, Op: op, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actor})
}

func (s *Store) SetWikiPageClassification(ctx context.Context, ws, actor, id, levelID string) (*models.WikiPage, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	page, err := scanWikiPage(tx.QueryRow(ctx, wikiPageSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiPageVisible+` AND `+wikiPageWritable+` AND p.id::text=$3 AND p.status='current' FOR UPDATE OF p`, ws, actor, id))
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE wiki_pages SET classification_level=$2 WHERE id::text=$1`, id, levelID); err != nil {
		return nil, err
	}
	page.ClassificationLevel = levelID
	if err = wikiAction(ctx, tx, ws, actor, "wiki_page", page.ID, page.SpaceID, page); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return page, nil
}

func (s *Store) WikiPageLikes(ctx context.Context, ws, actor, id string) ([]string, error) {
	page, err := s.WikiPage(ctx, ws, actor, id)
	if err != nil || page.Status != "current" {
		if err == nil {
			err = pgx.ErrNoRows
		}
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT l.user_id FROM wiki_page_likes l JOIN users u ON u.id=l.user_id AND u.active WHERE l.page_id::text=$1 ORDER BY l.created_at,l.user_id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	likes := []string{}
	for rows.Next() {
		var accountID string
		if err := rows.Scan(&accountID); err != nil {
			return nil, err
		}
		likes = append(likes, accountID)
	}
	return likes, rows.Err()
}

func (s *Store) SetWikiPageLike(ctx context.Context, ws, actor, id string, liked bool) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	page, err := scanWikiPage(tx.QueryRow(ctx, wikiPageSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiPageVisible+` AND p.id::text=$3 AND p.status='current' FOR SHARE OF p`, ws, actor, id))
	if err != nil {
		return err
	}
	var changed bool
	if liked {
		tag, execErr := tx.Exec(ctx, `INSERT INTO wiki_page_likes(page_id,user_id) VALUES($1::bigint,$2) ON CONFLICT DO NOTHING`, id, actor)
		err, changed = execErr, tag.RowsAffected() > 0
	} else {
		tag, execErr := tx.Exec(ctx, `DELETE FROM wiki_page_likes WHERE page_id::text=$1 AND user_id=$2`, id, actor)
		err, changed = execErr, tag.RowsAffected() > 0
	}
	if err != nil {
		return err
	}
	if changed {
		op := models.OpUpsert
		if !liked {
			op = models.OpDelete
		}
		if err = pageGovernanceAction(ctx, tx, ws, actor, "wiki_page_like", id+":"+actor, op, page, map[string]any{"accountId": actor, "liked": liked}); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) RedactWikiPage(ctx context.Context, ws, actor, id, createdAt string, version int, cleanHistory bool, titlePointers, bodyPointers []models.WikiRedactionPointer) (*models.WikiPage, []models.WikiRedactionResult, []models.WikiRedactionResult, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	page, err := scanWikiPage(tx.QueryRow(ctx, wikiPageSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiPageVisible+` AND `+wikiPageWritable+` AND p.id::text=$3 AND p.status='current' FOR UPDATE OF p`, ws, actor, id))
	if err != nil {
		return nil, nil, nil, err
	}
	requestedTime, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("%w: createdAt must be an RFC 3339 timestamp", ErrWikiValidation)
	}
	currentTime, _ := time.Parse(time.RFC3339, page.Version.CreatedAt)
	if !requestedTime.Equal(currentTime) || (version != 0 && version != page.Version.Number) {
		return nil, nil, nil, ErrWikiConflict
	}
	if len(titlePointers)+len(bodyPointers) == 0 || len(titlePointers)+len(bodyPointers) > 100 {
		return nil, nil, nil, fmt.Errorf("%w: provide between 1 and 100 redaction pointers", ErrWikiValidation)
	}
	titleRanges, err := normalizeBlogRedactions(page.Title, titlePointers, map[string]bool{"": true, "/": true, "/title": true, "/value": true})
	if err != nil {
		return nil, nil, nil, err
	}
	bodyRanges, err := normalizeBlogRedactions(page.Body.Value, bodyPointers, map[string]bool{"": true, "/": true, "/value": true, "/storage/value": true, "/body/storage/value": true})
	if err != nil {
		return nil, nil, nil, err
	}
	redactedTitle, redactedBody := applyBlogRedactions(page.Title, titleRanges), applyBlogRedactions(page.Body.Value, bodyRanges)
	if strings.TrimSpace(redactedTitle) == "" {
		return nil, nil, nil, fmt.Errorf("%w: redaction cannot remove the complete page title", ErrWikiValidation)
	}
	if _, err := wikimarkup.Render(redactedBody); err != nil {
		return nil, nil, nil, fmt.Errorf("%w: redaction ranges must select body text without markup", ErrWikiValidation)
	}
	if cleanHistory {
		rows, err := tx.Query(ctx, `SELECT version,title,body FROM wiki_page_versions WHERE page_id::text=$1 FOR UPDATE`, id)
		if err != nil {
			return nil, nil, nil, err
		}
		type historical struct {
			version     int
			title, body string
		}
		versions := []historical{}
		for rows.Next() {
			var item historical
			if err := rows.Scan(&item.version, &item.title, &item.body); err != nil {
				rows.Close()
				return nil, nil, nil, err
			}
			versions = append(versions, item)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, nil, nil, err
		}
		rows.Close()
		for _, item := range versions {
			if _, err := tx.Exec(ctx, `UPDATE wiki_page_versions SET title=$3,body=$4 WHERE page_id::text=$1 AND version=$2`, id, item.version, scrubBlogHistory(item.title, titleRanges), scrubBlogHistory(item.body, bodyRanges)); err != nil {
				return nil, nil, nil, err
			}
		}
	}
	newVersion := page.Version.Number + 1
	if _, err := tx.Exec(ctx, `UPDATE wiki_pages SET title=$2,body=$3,version=$4 WHERE id::text=$1`, id, redactedTitle, redactedBody, newVersion); err != nil {
		return nil, nil, nil, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO wiki_page_versions(page_id,version,title,body,status,author_id,message) VALUES($1::bigint,$2,$3,$4,'current',$5,'Sensitive content redacted')`, id, newVersion, redactedTitle, redactedBody, actor); err != nil {
		return nil, nil, nil, err
	}
	titleResults := make([]models.WikiRedactionResult, 0, len(titleRanges))
	bodyResults := make([]models.WikiRedactionResult, 0, len(bodyRanges))
	insert := func(section string, ranges []blogRedactionRange, results *[]models.WikiRedactionResult) error {
		ordered := append([]blogRedactionRange(nil), ranges...)
		sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].order < ordered[j].order })
		for _, item := range ordered {
			var redactionID string
			if err := tx.QueryRow(ctx, `INSERT INTO wiki_page_redactions(page_id,version,section,pointer,from_index,to_index,reason,actor_id) VALUES($1::bigint,$2,$3,$4,$5,$6,$7,$8) RETURNING id::text`, id, newVersion, section, item.pointer, item.from, item.to, item.reason, actor).Scan(&redactionID); err != nil {
				return err
			}
			*results = append(*results, models.WikiRedactionResult{Pointer: item.pointer, From: item.from, To: item.to, Reason: item.reason, RedactionID: redactionID})
		}
		return nil
	}
	if err := insert("title", titleRanges, &titleResults); err != nil {
		return nil, nil, nil, err
	}
	if err := insert("body", bodyRanges, &bodyResults); err != nil {
		return nil, nil, nil, err
	}
	updated, err := scanWikiPage(tx.QueryRow(ctx, wikiPageSelect+` WHERE p.id::text=$1`, id))
	if err != nil {
		return nil, nil, nil, err
	}
	if err := wikiAction(ctx, tx, ws, actor, "wiki_page", updated.ID, updated.SpaceID, updated); err != nil {
		return nil, nil, nil, err
	}
	detail, _ := json.Marshal(map[string]any{"cleanHistory": cleanHistory, "previousVersion": page.Version.Number, "version": newVersion, "titleRedactions": len(titleResults), "bodyRedactions": len(bodyResults)})
	if _, err := tx.Exec(ctx, `INSERT INTO organization_audit_events(organization_id,actor_id,action,target_type,target_id,detail) SELECT organization_id,$2,'wiki.page.redacted','wiki_page',$3,$4::jsonb FROM sites WHERE workspace_id=$1`, ws, actor, id, detail); err != nil {
		return nil, nil, nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, nil, err
	}
	return updated, titleResults, bodyResults, nil
}

func (s *Store) WikiPageCustomContent(ctx context.Context, ws, actor, pageID, contentType, order string) ([]models.WikiBlogCustomContent, error) {
	page, err := s.WikiPage(ctx, ws, actor, pageID)
	if err != nil || page.Status != "current" {
		if err == nil {
			err = pgx.ErrNoRows
		}
		return nil, err
	}
	var representation string
	if err := s.Pool.QueryRow(ctx, `SELECT body_representation FROM wiki_custom_content_types WHERE type=$1`, contentType).Scan(&representation); err != nil {
		return nil, err
	}
	orders := map[string]string{"": "cc.id", "id": "cc.id", "-id": "cc.id DESC", "created-date": "cc.created_at,cc.id", "-created-date": "cc.created_at DESC,cc.id DESC", "modified-date": "cc.created_at,cc.id", "-modified-date": "cc.created_at DESC,cc.id DESC", "title": "cc.title,cc.id", "-title": "cc.title DESC,cc.id DESC"}
	orderSQL, ok := orders[order]
	if !ok {
		return nil, fmt.Errorf("%w: unsupported custom content sort order", ErrWikiValidation)
	}
	rows, err := s.Pool.Query(ctx, `SELECT cc.id::text,cc.custom_type,cc.status,cc.title,cc.space_id::text,cc.parent_page_id::text,cc.author_id,to_char(cc.created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),ct.body_representation,cc.body,cc.version,cc.author_id,to_char(cc.created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"') FROM wiki_content cc JOIN wiki_custom_content_types ct ON ct.type=cc.custom_type WHERE cc.type='custom' AND cc.parent_page_id::text=$1 AND cc.custom_type=$2 AND cc.status='current' ORDER BY `+orderSQL, pageID, contentType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []models.WikiBlogCustomContent{}
	for rows.Next() {
		var value models.WikiBlogCustomContent
		if err := rows.Scan(&value.ID, &value.Type, &value.Status, &value.Title, &value.SpaceID, &value.PageID, &value.AuthorID, &value.CreatedAt, &value.BodyRepresentation, &value.Body.Value, &value.Version.Number, &value.Version.AuthorID, &value.Version.CreatedAt); err != nil {
			return nil, err
		}
		value.Body.Representation = representation
		values = append(values, value)
	}
	return values, rows.Err()
}
