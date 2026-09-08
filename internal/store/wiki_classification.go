package store

import (
	"context"

	"github.com/e6qu/zzira/internal/models"
)

func (s *Store) SetWikiContentClassification(ctx context.Context, ws, actor, id, contentType, levelID string) (*models.WikiContent, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	content, err := scanWikiContent(tx.QueryRow(ctx, wikiContentSelect+` WHERE s.workspace_id=$1 AND `+wikiContentWritableFor(contentType)+` AND c.id::text=$3 AND c.type=$4 AND c.status='current' FOR UPDATE OF c`, ws, actor, id, contentType))
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE wiki_content SET classification_level=$2,updated_at=now() WHERE id::text=$1`, id, levelID); err != nil {
		return nil, err
	}
	content.ClassificationLevel = levelID
	if err = wikiContentAction(ctx, tx, ws, actor, content, models.OpUpsert); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return content, nil
}
