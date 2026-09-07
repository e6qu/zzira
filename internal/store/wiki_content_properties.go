package store

import (
	"context"
	"encoding/json"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

const wikiContentPropertySelect = `SELECT cp.id::text,cp.content_id::text,cp.key,cp.value,pv.version,pv.message,pv.author_id,to_char(pv.created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"')
  FROM wiki_content_properties cp JOIN wiki_content_property_versions pv ON pv.property_id=cp.id AND pv.version=cp.version
  JOIN wiki_content c ON c.id=cp.content_id JOIN wiki_spaces s ON s.id=c.space_id LEFT JOIN wiki_pages p ON p.id=c.root_page_id`

func scanWikiContentProperty(row pgx.Row) (*models.WikiContentProperty, error) {
	property := &models.WikiContentProperty{}
	err := row.Scan(&property.ID, &property.ContentID, &property.Key, &property.Value,
		&property.Version.Number, &property.Version.Message, &property.Version.AuthorID,
		&property.Version.CreatedAt)
	return property, err
}

func (s *Store) WikiContentProperties(ctx context.Context, ws, user, contentID, contentType, key string) ([]models.WikiContentProperty, error) {
	if _, err := s.WikiContent(ctx, ws, user, contentID, contentType); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, wikiContentPropertySelect+` WHERE s.workspace_id=$1 AND `+wikiContentVisible+` AND c.id::text=$3 AND c.type=$4 AND c.status='current' AND ($5='' OR cp.key=$5) ORDER BY cp.key,cp.id`, ws, user, contentID, contentType, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	properties := []models.WikiContentProperty{}
	for rows.Next() {
		property, err := scanWikiContentProperty(rows)
		if err != nil {
			return nil, err
		}
		properties = append(properties, *property)
	}
	return properties, rows.Err()
}

func (s *Store) WikiContentProperty(ctx context.Context, ws, user, contentID, contentType, propertyID string) (*models.WikiContentProperty, error) {
	return scanWikiContentProperty(s.Pool.QueryRow(ctx, wikiContentPropertySelect+` WHERE s.workspace_id=$1 AND `+wikiContentVisible+` AND c.id::text=$3 AND c.type=$4 AND c.status='current' AND cp.id::text=$5`, ws, user, contentID, contentType, propertyID))
}

func contentPropertyAction(ctx context.Context, tx pgx.Tx, ws, actor, spaceID, rootPage string, property *models.WikiContentProperty, op string) error {
	seq, err := nextSeq(ctx, tx, ws)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{"wikiSpaceId": spaceID, "rootPageId": rootPage, "wiki_content_property": property})
	if err != nil {
		return err
	}
	return appendAction(ctx, tx, &models.Action{WorkspaceID: ws, Seq: seq, EntityType: "wiki_content_property", EntityID: property.ID, Op: op, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actor})
}

func (s *Store) CreateWikiContentProperty(ctx context.Context, ws, actor, contentID, contentType, key string, value json.RawMessage) (*models.WikiContentProperty, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var spaceID, rootPage string
	if err = tx.QueryRow(ctx, `SELECT c.space_id::text,COALESCE(c.root_page_id::text,'') FROM wiki_content c JOIN wiki_spaces s ON s.id=c.space_id LEFT JOIN wiki_pages p ON p.id=c.root_page_id WHERE s.workspace_id=$1 AND `+wikiContentWritable+` AND c.id::text=$3 AND c.type=$4 AND c.status='current' FOR SHARE OF c`, ws, actor, contentID, contentType).Scan(&spaceID, &rootPage); err != nil {
		return nil, err
	}
	var propertyID string
	if err = tx.QueryRow(ctx, `INSERT INTO wiki_content_properties(content_id,key,value,author_id) VALUES($1::bigint,$2,$3::jsonb,$4) RETURNING id::text`, contentID, key, value, actor).Scan(&propertyID); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO wiki_content_property_versions(property_id,version,value,author_id) VALUES($1::bigint,1,$2::jsonb,$3)`, propertyID, value, actor); err != nil {
		return nil, err
	}
	property, err := scanWikiContentProperty(tx.QueryRow(ctx, wikiContentPropertySelect+` WHERE cp.id::text=$1`, propertyID))
	if err != nil {
		return nil, err
	}
	if err = contentPropertyAction(ctx, tx, ws, actor, spaceID, rootPage, property, models.OpUpsert); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return property, nil
}

func (s *Store) UpdateWikiContentProperty(ctx context.Context, ws, actor, contentID, contentType, propertyID, key string, value json.RawMessage, version int, message string) (*models.WikiContentProperty, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var spaceID, rootPage, oldKey string
	var oldVersion int
	if err = tx.QueryRow(ctx, `SELECT c.space_id::text,COALESCE(c.root_page_id::text,''),cp.key,cp.version FROM wiki_content_properties cp JOIN wiki_content c ON c.id=cp.content_id JOIN wiki_spaces s ON s.id=c.space_id LEFT JOIN wiki_pages p ON p.id=c.root_page_id WHERE s.workspace_id=$1 AND `+wikiContentWritable+` AND c.id::text=$3 AND c.type=$4 AND c.status='current' AND cp.id::text=$5 FOR UPDATE OF cp`, ws, actor, contentID, contentType, propertyID).Scan(&spaceID, &rootPage, &oldKey, &oldVersion); err != nil {
		return nil, err
	}
	if oldKey != key || version != oldVersion+1 {
		return nil, ErrWikiPropertyConflict
	}
	if _, err = tx.Exec(ctx, `UPDATE wiki_content_properties SET value=$2::jsonb,version=$3,author_id=$4,updated_at=now() WHERE id::text=$1`, propertyID, value, version, actor); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO wiki_content_property_versions(property_id,version,value,author_id,message) VALUES($1::bigint,$2,$3::jsonb,$4,$5)`, propertyID, version, value, actor, message); err != nil {
		return nil, err
	}
	property, err := scanWikiContentProperty(tx.QueryRow(ctx, wikiContentPropertySelect+` WHERE cp.id::text=$1`, propertyID))
	if err != nil {
		return nil, err
	}
	if err = contentPropertyAction(ctx, tx, ws, actor, spaceID, rootPage, property, models.OpUpsert); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return property, nil
}

func (s *Store) DeleteWikiContentProperty(ctx context.Context, ws, actor, contentID, contentType, propertyID string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	property, err := scanWikiContentProperty(tx.QueryRow(ctx, wikiContentPropertySelect+` WHERE s.workspace_id=$1 AND `+wikiContentWritable+` AND c.id::text=$3 AND c.type=$4 AND c.status='current' AND cp.id::text=$5 FOR UPDATE OF cp`, ws, actor, contentID, contentType, propertyID))
	if err != nil {
		return err
	}
	var spaceID, rootPage string
	if err = tx.QueryRow(ctx, `SELECT space_id::text,COALESCE(root_page_id::text,'') FROM wiki_content WHERE id::text=$1`, contentID).Scan(&spaceID, &rootPage); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM wiki_content_properties WHERE id::text=$1`, propertyID); err != nil {
		return err
	}
	if err = contentPropertyAction(ctx, tx, ws, actor, spaceID, rootPage, property, models.OpDelete); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
