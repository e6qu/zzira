package store

import (
	"context"
	"encoding/json"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

const wikiPagePropertySelect = `SELECT pp.id::text,pp.page_id::text,pp.key,pp.value,pv.version,pv.message,pv.author_id,to_char(pv.created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"')
  FROM wiki_page_properties pp JOIN wiki_page_property_versions pv ON pv.property_id=pp.id AND pv.version=pp.version
  JOIN wiki_pages p ON p.id=pp.page_id JOIN wiki_spaces s ON s.id=p.space_id`

func scanWikiPageProperty(row pgx.Row) (*models.WikiContentProperty, error) {
	property := &models.WikiContentProperty{}
	err := row.Scan(&property.ID, &property.ContentID, &property.Key, &property.Value, &property.Version.Number, &property.Version.Message, &property.Version.AuthorID, &property.Version.CreatedAt)
	return property, err
}

func (s *Store) WikiPageProperties(ctx context.Context, ws, actor, pageID, key string) ([]models.WikiContentProperty, error) {
	if _, err := s.WikiPage(ctx, ws, actor, pageID); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, wikiPagePropertySelect+` WHERE s.workspace_id=$1 AND `+wikiPageVisible+` AND p.id::text=$3 AND p.status='current' AND ($4='' OR pp.key=$4) ORDER BY pp.key,pp.id`, ws, actor, pageID, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	properties := []models.WikiContentProperty{}
	for rows.Next() {
		property, err := scanWikiPageProperty(rows)
		if err != nil {
			return nil, err
		}
		properties = append(properties, *property)
	}
	return properties, rows.Err()
}

func (s *Store) WikiPageProperty(ctx context.Context, ws, actor, pageID, propertyID string) (*models.WikiContentProperty, error) {
	return scanWikiPageProperty(s.Pool.QueryRow(ctx, wikiPagePropertySelect+` WHERE s.workspace_id=$1 AND `+wikiPageVisible+` AND p.id::text=$3 AND p.status='current' AND pp.id::text=$4`, ws, actor, pageID, propertyID))
}

func (s *Store) CreateWikiPageProperty(ctx context.Context, ws, actor, pageID, key string, value json.RawMessage) (*models.WikiContentProperty, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	page, err := scanWikiPage(tx.QueryRow(ctx, wikiPageSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiPageVisible+` AND `+wikiPageWritable+` AND p.id::text=$3 AND p.status='current' FOR SHARE OF p`, ws, actor, pageID))
	if err != nil {
		return nil, err
	}
	var propertyID string
	if err = tx.QueryRow(ctx, `INSERT INTO wiki_page_properties(page_id,key,value,author_id) VALUES($1::bigint,$2,$3::jsonb,$4) RETURNING id::text`, pageID, key, value, actor).Scan(&propertyID); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO wiki_page_property_versions(property_id,version,value,author_id) VALUES($1::bigint,1,$2::jsonb,$3)`, propertyID, value, actor); err != nil {
		return nil, err
	}
	property, err := scanWikiPageProperty(tx.QueryRow(ctx, wikiPagePropertySelect+` WHERE pp.id::text=$1`, propertyID))
	if err != nil {
		return nil, err
	}
	if err = pageGovernanceAction(ctx, tx, ws, actor, "wiki_page_property", property.ID, models.OpUpsert, page, property); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return property, nil
}

func (s *Store) UpdateWikiPageProperty(ctx context.Context, ws, actor, pageID, propertyID, key string, value json.RawMessage, version int, message string) (*models.WikiContentProperty, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	page, err := scanWikiPage(tx.QueryRow(ctx, wikiPageSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiPageVisible+` AND `+wikiPageWritable+` AND p.id::text=$3 AND p.status='current' FOR SHARE OF p`, ws, actor, pageID))
	if err != nil {
		return nil, err
	}
	var oldKey string
	var oldVersion int
	if err = tx.QueryRow(ctx, `SELECT key,version FROM wiki_page_properties WHERE page_id::text=$1 AND id::text=$2 FOR UPDATE`, pageID, propertyID).Scan(&oldKey, &oldVersion); err != nil {
		return nil, err
	}
	if oldKey != key || version != oldVersion+1 {
		return nil, ErrWikiPropertyConflict
	}
	if _, err = tx.Exec(ctx, `UPDATE wiki_page_properties SET value=$2::jsonb,version=$3,author_id=$4,updated_at=now() WHERE id::text=$1`, propertyID, value, version, actor); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO wiki_page_property_versions(property_id,version,value,author_id,message) VALUES($1::bigint,$2,$3::jsonb,$4,$5)`, propertyID, version, value, actor, message); err != nil {
		return nil, err
	}
	property, err := scanWikiPageProperty(tx.QueryRow(ctx, wikiPagePropertySelect+` WHERE pp.id::text=$1`, propertyID))
	if err != nil {
		return nil, err
	}
	if err = pageGovernanceAction(ctx, tx, ws, actor, "wiki_page_property", property.ID, models.OpUpsert, page, property); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return property, nil
}

func (s *Store) DeleteWikiPageProperty(ctx context.Context, ws, actor, pageID, propertyID string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	page, err := scanWikiPage(tx.QueryRow(ctx, wikiPageSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiPageVisible+` AND `+wikiPageWritable+` AND p.id::text=$3 AND p.status='current' FOR SHARE OF p`, ws, actor, pageID))
	if err != nil {
		return err
	}
	property, err := scanWikiPageProperty(tx.QueryRow(ctx, wikiPagePropertySelect+` WHERE pp.page_id::text=$1 AND pp.id::text=$2 FOR UPDATE OF pp`, pageID, propertyID))
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM wiki_page_properties WHERE id::text=$1`, propertyID); err != nil {
		return err
	}
	if err = pageGovernanceAction(ctx, tx, ws, actor, "wiki_page_property", property.ID, models.OpDelete, page, property); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
