package store

import (
	"context"
	"encoding/json"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

const wikiAttachmentPropertySelect = `SELECT cp.id::text,cp.attachment_id::text,cp.key,cp.value,cp.version,v.message,v.author_id,to_char(v.created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"') FROM wiki_attachment_properties cp JOIN wiki_attachment_property_versions v ON v.property_id=cp.id AND v.version=cp.version`

func scanWikiAttachmentProperty(row pgx.Row) (*models.WikiAttachmentProperty, error) {
	property := &models.WikiAttachmentProperty{}
	err := row.Scan(&property.ID, &property.AttachmentID, &property.Key, &property.Value, &property.Version.Number, &property.Version.Message, &property.Version.AuthorID, &property.Version.CreatedAt)
	return property, err
}

func (s *Store) WikiAttachmentProperties(ctx context.Context, ws, user, attachmentID, key string) ([]models.WikiAttachmentProperty, error) {
	if _, err := s.WikiAttachment(ctx, ws, user, attachmentID); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, wikiAttachmentPropertySelect+` WHERE cp.attachment_id::text=$1 AND ($2='' OR cp.key=$2) ORDER BY cp.key,cp.id`, attachmentID, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	properties := []models.WikiAttachmentProperty{}
	for rows.Next() {
		property, err := scanWikiAttachmentProperty(rows)
		if err != nil {
			return nil, err
		}
		properties = append(properties, *property)
	}
	return properties, rows.Err()
}

func (s *Store) WikiAttachmentProperty(ctx context.Context, ws, user, attachmentID, propertyID string) (*models.WikiAttachmentProperty, error) {
	if _, err := s.WikiAttachment(ctx, ws, user, attachmentID); err != nil {
		return nil, err
	}
	return scanWikiAttachmentProperty(s.Pool.QueryRow(ctx, wikiAttachmentPropertySelect+` WHERE cp.attachment_id::text=$1 AND cp.id::text=$2`, attachmentID, propertyID))
}

func wikiAttachmentPropertyAction(ctx context.Context, tx pgx.Tx, ws, actor, spaceID, pageID string, property *models.WikiAttachmentProperty, op string) error {
	seq, err := nextSeq(ctx, tx, ws)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{"wikiSpaceId": spaceID, "wiki_attachment_property": map[string]any{"id": property.ID, "attachmentId": property.AttachmentID, "pageId": pageID, "key": property.Key, "value": property.Value, "version": property.Version}})
	if err != nil {
		return err
	}
	return appendAction(ctx, tx, &models.Action{WorkspaceID: ws, Seq: seq, EntityType: "wiki_attachment_property", EntityID: property.ID, Op: op, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actor})
}

func (s *Store) CreateWikiAttachmentProperty(ctx context.Context, ws, actor, attachmentID, key string, value json.RawMessage) (*models.WikiAttachmentProperty, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var pageID, spaceID, propertyID string
	err = tx.QueryRow(ctx, `SELECT p.id::text,s.id::text FROM wiki_attachments a JOIN wiki_pages p ON p.id=a.page_id JOIN wiki_spaces s ON s.id=p.space_id WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiPageVisible+` AND `+wikiPageWritable+` AND a.id::text=$3 FOR SHARE OF a,p`, ws, actor, attachmentID).Scan(&pageID, &spaceID)
	if err != nil {
		return nil, err
	}
	err = tx.QueryRow(ctx, `INSERT INTO wiki_attachment_properties(attachment_id,key,value,author_id) VALUES($1::bigint,$2,$3::jsonb,$4) RETURNING id::text`, attachmentID, key, value, actor).Scan(&propertyID)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO wiki_attachment_property_versions(property_id,version,value,author_id) VALUES($1::bigint,1,$2::jsonb,$3)`, propertyID, value, actor); err != nil {
		return nil, err
	}
	property, err := scanWikiAttachmentProperty(tx.QueryRow(ctx, wikiAttachmentPropertySelect+` WHERE cp.id::text=$1`, propertyID))
	if err != nil {
		return nil, err
	}
	if err = wikiAttachmentPropertyAction(ctx, tx, ws, actor, spaceID, pageID, property, models.OpUpsert); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return property, nil
}

func (s *Store) UpdateWikiAttachmentProperty(ctx context.Context, ws, actor, attachmentID, propertyID, key string, value json.RawMessage, version int, message string) (*models.WikiAttachmentProperty, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var pageID, spaceID, oldKey string
	var oldVersion int
	err = tx.QueryRow(ctx, `SELECT p.id::text,s.id::text,cp.key,cp.version FROM wiki_attachment_properties cp JOIN wiki_attachments a ON a.id=cp.attachment_id JOIN wiki_pages p ON p.id=a.page_id JOIN wiki_spaces s ON s.id=p.space_id WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiPageVisible+` AND `+wikiPageWritable+` AND a.id::text=$3 AND cp.id::text=$4 FOR UPDATE OF cp`, ws, actor, attachmentID, propertyID).Scan(&pageID, &spaceID, &oldKey, &oldVersion)
	if err != nil {
		return nil, err
	}
	if version != oldVersion+1 {
		return nil, ErrWikiPropertyConflict
	}
	if key == "" {
		key = oldKey
	}
	if _, err = tx.Exec(ctx, `UPDATE wiki_attachment_properties SET key=$2,value=$3::jsonb,version=$4,author_id=$5,updated_at=now() WHERE id::text=$1`, propertyID, key, value, version, actor); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO wiki_attachment_property_versions(property_id,version,value,author_id,message) VALUES($1::bigint,$2,$3::jsonb,$4,$5)`, propertyID, version, value, actor, message); err != nil {
		return nil, err
	}
	property, err := scanWikiAttachmentProperty(tx.QueryRow(ctx, wikiAttachmentPropertySelect+` WHERE cp.id::text=$1`, propertyID))
	if err != nil {
		return nil, err
	}
	if err = wikiAttachmentPropertyAction(ctx, tx, ws, actor, spaceID, pageID, property, models.OpUpsert); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return property, nil
}

func (s *Store) DeleteWikiAttachmentProperty(ctx context.Context, ws, actor, attachmentID, propertyID string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var pageID, spaceID string
	property, err := scanWikiAttachmentProperty(tx.QueryRow(ctx, wikiAttachmentPropertySelect+` JOIN wiki_attachments a ON a.id=cp.attachment_id JOIN wiki_pages p ON p.id=a.page_id JOIN wiki_spaces s ON s.id=p.space_id WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiPageVisible+` AND `+wikiPageWritable+` AND a.id::text=$3 AND cp.id::text=$4 FOR UPDATE OF cp`, ws, actor, attachmentID, propertyID))
	if err != nil {
		return err
	}
	if err = tx.QueryRow(ctx, `SELECT p.id::text,p.space_id::text FROM wiki_attachments a JOIN wiki_pages p ON p.id=a.page_id WHERE a.id::text=$1`, attachmentID).Scan(&pageID, &spaceID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM wiki_attachment_properties WHERE id::text=$1`, propertyID); err != nil {
		return err
	}
	if err = wikiAttachmentPropertyAction(ctx, tx, ws, actor, spaceID, pageID, property, models.OpDelete); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) WikiAttachmentLabels(ctx context.Context, ws, user, attachmentID string) ([]models.WikiLabel, error) {
	if _, err := s.WikiAttachment(ctx, ws, user, attachmentID); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, wikiLabelSelect+` JOIN wiki_attachment_labels al ON al.label_id=l.id WHERE al.attachment_id::text=$1 ORDER BY l.created_at,l.id`, attachmentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanWikiLabels(rows)
}

func (s *Store) WikiAttachmentsByLabel(ctx context.Context, ws, user, labelID string) ([]*models.WikiAttachment, error) {
	labels, err := s.WikiLabels(ctx, ws, user)
	if err != nil {
		return nil, err
	}
	found := false
	for _, label := range labels {
		found = found || label.ID == labelID
	}
	if !found {
		return nil, pgx.ErrNoRows
	}
	rows, err := s.Pool.Query(ctx, wikiAttachmentSelect+` JOIN wiki_attachment_labels al ON al.attachment_id=a.id WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiPageVisible+` AND a.status='current' AND al.label_id::text=$3 ORDER BY a.created_at,a.id`, ws, user, labelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	attachments := []*models.WikiAttachment{}
	for rows.Next() {
		attachment, err := scanWikiAttachment(rows)
		if err != nil {
			return nil, err
		}
		attachments = append(attachments, attachment)
	}
	return attachments, rows.Err()
}

func wikiAttachmentLabelAction(ctx context.Context, tx pgx.Tx, ws, actor, spaceID, pageID, attachmentID string, label models.WikiLabel, attached bool) error {
	seq, err := nextSeq(ctx, tx, ws)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{"wikiSpaceId": spaceID, "wiki_label": map[string]any{"id": label.ID, "name": label.Name, "prefix": label.Prefix, "pageId": pageID, "attachmentId": attachmentID, "attached": attached}})
	if err != nil {
		return err
	}
	op := models.OpUpsert
	if !attached {
		op = models.OpDelete
	}
	return appendAction(ctx, tx, &models.Action{WorkspaceID: ws, Seq: seq, EntityType: "wiki_label", EntityID: "attachment:" + attachmentID + ":" + label.ID, Op: op, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actor})
}

func (s *Store) AddWikiAttachmentLabels(ctx context.Context, ws, actor, attachmentID string, input []models.WikiLabel) ([]models.WikiLabel, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var pageID, spaceID string
	err = tx.QueryRow(ctx, `SELECT p.id::text,s.id::text FROM wiki_attachments a JOIN wiki_pages p ON p.id=a.page_id JOIN wiki_spaces s ON s.id=p.space_id WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiPageVisible+` AND `+wikiPageWritable+` AND a.id::text=$3 FOR SHARE OF a,p`, ws, actor, attachmentID).Scan(&pageID, &spaceID)
	if err != nil {
		return nil, err
	}
	for _, item := range input {
		label, err := ensureWikiLabel(ctx, tx, ws, item)
		if err != nil {
			return nil, err
		}
		tag, err := tx.Exec(ctx, `INSERT INTO wiki_attachment_labels(attachment_id,label_id,author_id) VALUES($1::bigint,$2::bigint,$3) ON CONFLICT DO NOTHING`, attachmentID, label.ID, actor)
		if err != nil {
			return nil, err
		}
		if tag.RowsAffected() > 0 {
			if err = wikiAttachmentLabelAction(ctx, tx, ws, actor, spaceID, pageID, attachmentID, label, true); err != nil {
				return nil, err
			}
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.WikiAttachmentLabels(ctx, ws, actor, attachmentID)
}

func (s *Store) RemoveWikiAttachmentLabel(ctx context.Context, ws, actor, attachmentID string, input models.WikiLabel) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var pageID, spaceID string
	var label models.WikiLabel
	err = tx.QueryRow(ctx, `SELECT p.id::text,s.id::text,l.id::text,l.name,l.prefix,to_char(l.created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"') FROM wiki_attachment_labels al JOIN wiki_attachments a ON a.id=al.attachment_id JOIN wiki_pages p ON p.id=a.page_id JOIN wiki_spaces s ON s.id=p.space_id JOIN wiki_labels l ON l.id=al.label_id WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiPageVisible+` AND `+wikiPageWritable+` AND a.id::text=$3 AND l.prefix=$4 AND l.name=$5 FOR UPDATE OF al`, ws, actor, attachmentID, input.Prefix, input.Name).Scan(&pageID, &spaceID, &label.ID, &label.Name, &label.Prefix, &label.CreatedAt)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM wiki_attachment_labels WHERE attachment_id::text=$1 AND label_id::text=$2`, attachmentID, label.ID); err != nil {
		return err
	}
	if err = wikiAttachmentLabelAction(ctx, tx, ws, actor, spaceID, pageID, attachmentID, label, false); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
