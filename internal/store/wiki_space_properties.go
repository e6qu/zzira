package store

import (
	"context"
	"encoding/json"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

const wikiSpacePropertySelect = `SELECT sp.id::text,sp.space_id::text,sp.key,sp.value,sv.version,sv.message,sv.author_id,to_char(sv.created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"')
  FROM wiki_space_properties sp JOIN wiki_space_property_versions sv ON sv.property_id=sp.id AND sv.version=sp.version
  JOIN wiki_spaces s ON s.id=sp.space_id`

func wikiSpacePropertyAction(ctx context.Context, tx pgx.Tx, ws, actor, spaceID, op string, property *models.WikiContentProperty) error {
	seq, err := nextSeq(ctx, tx, ws)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{"wikiSpaceId": spaceID, "wiki_space_property": property})
	if err != nil {
		return err
	}
	return appendAction(ctx, tx, &models.Action{WorkspaceID: ws, Seq: seq, EntityType: "wiki_space_property", EntityID: property.ID, Op: op, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actor})
}

func scanWikiSpaceProperty(row pgx.Row) (*models.WikiContentProperty, error) {
	property := &models.WikiContentProperty{}
	err := row.Scan(&property.ID, &property.ContentID, &property.Key, &property.Value, &property.Version.Number, &property.Version.Message, &property.Version.AuthorID, &property.Version.CreatedAt)
	return property, err
}

func (s *Store) WikiSpaceProperties(ctx context.Context, ws, actor, spaceID, key string) ([]models.WikiContentProperty, error) {
	if _, err := s.WikiSpace(ctx, ws, actor, spaceID); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, wikiSpacePropertySelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND s.id::text=$3 AND ($4='' OR sp.key=$4) ORDER BY sp.key,sp.id`, ws, actor, spaceID, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	properties := []models.WikiContentProperty{}
	for rows.Next() {
		property, err := scanWikiSpaceProperty(rows)
		if err != nil {
			return nil, err
		}
		properties = append(properties, *property)
	}
	return properties, rows.Err()
}

func (s *Store) WikiSpaceProperty(ctx context.Context, ws, actor, spaceID, propertyID string) (*models.WikiContentProperty, error) {
	return scanWikiSpaceProperty(s.Pool.QueryRow(ctx, wikiSpacePropertySelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND s.id::text=$3 AND sp.id::text=$4`, ws, actor, spaceID, propertyID))
}

func (s *Store) CreateWikiSpaceProperty(ctx context.Context, ws, actor, spaceID, key string, value json.RawMessage) (*models.WikiContentProperty, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := wikiSpaceAdmin(ctx, tx, ws, actor, spaceID); err != nil {
		return nil, err
	}
	space, err := scanWikiSpace(tx.QueryRow(ctx, wikiSpaceSelect+` WHERE s.workspace_id=$1 AND s.id::text=$2 FOR SHARE`, ws, spaceID))
	if err != nil {
		return nil, err
	}
	var propertyID string
	if err = tx.QueryRow(ctx, `INSERT INTO wiki_space_properties(space_id,key,value,author_id) VALUES($1::bigint,$2,$3::jsonb,$4) RETURNING id::text`, spaceID, key, value, actor).Scan(&propertyID); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO wiki_space_property_versions(property_id,version,value,author_id) VALUES($1::bigint,1,$2::jsonb,$3)`, propertyID, value, actor); err != nil {
		return nil, err
	}
	property, err := scanWikiSpaceProperty(tx.QueryRow(ctx, wikiSpacePropertySelect+` WHERE sp.id::text=$1`, propertyID))
	if err != nil {
		return nil, err
	}
	if err = wikiSpacePropertyAction(ctx, tx, ws, actor, space.ID, models.OpUpsert, property); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return property, nil
}

func (s *Store) UpdateWikiSpaceProperty(ctx context.Context, ws, actor, spaceID, propertyID, key string, value json.RawMessage, version int, message string) (*models.WikiContentProperty, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := wikiSpaceAdmin(ctx, tx, ws, actor, spaceID); err != nil {
		return nil, err
	}
	space, err := scanWikiSpace(tx.QueryRow(ctx, wikiSpaceSelect+` WHERE s.workspace_id=$1 AND s.id::text=$2 FOR SHARE`, ws, spaceID))
	if err != nil {
		return nil, err
	}
	var oldKey string
	var oldVersion int
	if err = tx.QueryRow(ctx, `SELECT key,version FROM wiki_space_properties WHERE space_id::text=$1 AND id::text=$2 FOR UPDATE`, spaceID, propertyID).Scan(&oldKey, &oldVersion); err != nil {
		return nil, err
	}
	if oldKey != key || version != oldVersion+1 {
		return nil, ErrWikiPropertyConflict
	}
	if _, err = tx.Exec(ctx, `UPDATE wiki_space_properties SET value=$2::jsonb,version=$3,author_id=$4,updated_at=now() WHERE id::text=$1`, propertyID, value, version, actor); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO wiki_space_property_versions(property_id,version,value,author_id,message) VALUES($1::bigint,$2,$3::jsonb,$4,$5)`, propertyID, version, value, actor, message); err != nil {
		return nil, err
	}
	property, err := scanWikiSpaceProperty(tx.QueryRow(ctx, wikiSpacePropertySelect+` WHERE sp.id::text=$1`, propertyID))
	if err != nil {
		return nil, err
	}
	if err = wikiSpacePropertyAction(ctx, tx, ws, actor, space.ID, models.OpUpsert, property); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return property, nil
}

func (s *Store) DeleteWikiSpaceProperty(ctx context.Context, ws, actor, spaceID, propertyID string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := wikiSpaceAdmin(ctx, tx, ws, actor, spaceID); err != nil {
		return err
	}
	space, err := scanWikiSpace(tx.QueryRow(ctx, wikiSpaceSelect+` WHERE s.workspace_id=$1 AND s.id::text=$2 FOR SHARE`, ws, spaceID))
	if err != nil {
		return err
	}
	property, err := scanWikiSpaceProperty(tx.QueryRow(ctx, wikiSpacePropertySelect+` WHERE sp.space_id::text=$1 AND sp.id::text=$2 FOR UPDATE OF sp`, spaceID, propertyID))
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM wiki_space_properties WHERE id::text=$1`, propertyID); err != nil {
		return err
	}
	if err = wikiSpacePropertyAction(ctx, tx, ws, actor, space.ID, models.OpDelete, property); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
