package store

import (
	"context"
	"encoding/json"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

// Comment properties are content properties on a footer or inline comment.
// Reading them needs permission to see the comment; changing them needs
// permission to edit it, which is the comment's author or an administrator
// within a space that allows comment updates — the same rule the comment
// itself is edited under.

const wikiCommentPropertySelect = `SELECT cp.id::text,cp.comment_id::text,cp.key,cp.value,cv.version,cv.message,cv.author_id,to_char(cv.created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"')
  FROM wiki_comment_properties cp JOIN wiki_comment_property_versions cv ON cv.property_id=cp.id AND cv.version=cp.version`

// wikiCommentReadable selects a comment the caller may see, as $3.
var wikiCommentReadable = `SELECT c.id FROM wiki_footer_comments c
	LEFT JOIN wiki_attachments ca ON ca.id=c.attachment_id
	LEFT JOIN wiki_pages p ON p.id=COALESCE(c.page_id,ca.page_id)
	LEFT JOIN wiki_blog_posts bp ON bp.id=COALESCE(c.blog_post_id,ca.blog_post_id)
	JOIN wiki_spaces s ON s.id=COALESCE(p.space_id,bp.space_id)
	WHERE s.workspace_id=$1 AND ` + wikiSpaceVisible + ` AND ` + wikiCommentVisible + ` AND c.id::text=$3`

// wikiCommentEditable narrows that to a comment the caller may edit.
var wikiCommentEditable = wikiCommentReadable + ` AND ` + wikiSpaceCanUpdateComment + ` AND (c.author_id=$2 OR EXISTS (
	SELECT 1 FROM memberships cam WHERE cam.workspace_id=s.workspace_id AND cam.user_id=$2 AND cam.role='admin'))`

func scanWikiCommentProperty(row pgx.Row) (*models.WikiContentProperty, error) {
	property := &models.WikiContentProperty{}
	err := row.Scan(&property.ID, &property.ContentID, &property.Key, &property.Value, &property.Version.Number, &property.Version.Message, &property.Version.AuthorID, &property.Version.CreatedAt)
	return property, err
}

// requireComment reports a comment the caller may not see as missing, so an id
// never confirms a comment exists to someone who cannot read it.
func requireComment(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, query, ws, actor, commentID string) error {
	var id int64
	return q.QueryRow(ctx, query+` LIMIT 1`, ws, actor, commentID).Scan(&id)
}

func (s *Store) WikiCommentProperties(ctx context.Context, ws, actor, commentID, key string) ([]models.WikiContentProperty, error) {
	if err := requireComment(ctx, s.Pool, wikiCommentReadable, ws, actor, commentID); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, wikiCommentPropertySelect+` WHERE cp.comment_id::text=$1 AND ($2='' OR cp.key=$2) ORDER BY cp.key,cp.id`, commentID, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	properties := []models.WikiContentProperty{}
	for rows.Next() {
		property, err := scanWikiCommentProperty(rows)
		if err != nil {
			return nil, err
		}
		properties = append(properties, *property)
	}
	return properties, rows.Err()
}

func (s *Store) WikiCommentProperty(ctx context.Context, ws, actor, commentID, propertyID string) (*models.WikiContentProperty, error) {
	if err := requireComment(ctx, s.Pool, wikiCommentReadable, ws, actor, commentID); err != nil {
		return nil, err
	}
	return scanWikiCommentProperty(s.Pool.QueryRow(ctx, wikiCommentPropertySelect+` WHERE cp.comment_id::text=$1 AND cp.id::text=$2`, commentID, propertyID))
}

// commentPropertyWriteGate checks the caller may edit the comment. One who may
// see it but not edit it is refused as forbidden; one who may not see it at all
// is told it does not exist.
func (s *Store) commentPropertyWriteGate(ctx context.Context, tx pgx.Tx, ws, actor, commentID string) error {
	if err := requireComment(ctx, tx, wikiCommentReadable, ws, actor, commentID); err != nil {
		return err
	}
	if err := requireComment(ctx, tx, wikiCommentEditable, ws, actor, commentID); err != nil {
		if err == pgx.ErrNoRows {
			return ErrProjectPermission
		}
		return err
	}
	return nil
}

func (s *Store) commentPropertyAction(ctx context.Context, tx pgx.Tx, ws, actor, commentID string, property *models.WikiContentProperty, op string) error {
	var spaceID string
	if err := tx.QueryRow(ctx, `SELECT COALESCE(p.space_id,bp.space_id)::text FROM wiki_footer_comments c
		LEFT JOIN wiki_attachments ca ON ca.id=c.attachment_id
		LEFT JOIN wiki_pages p ON p.id=COALESCE(c.page_id,ca.page_id)
		LEFT JOIN wiki_blog_posts bp ON bp.id=COALESCE(c.blog_post_id,ca.blog_post_id)
		WHERE c.id::text=$1`, commentID).Scan(&spaceID); err != nil {
		return err
	}
	if op == models.OpDelete {
		return wikiAction(ctx, tx, ws, actor, "wiki_comment_property_delete", property.ID, spaceID, property)
	}
	return wikiAction(ctx, tx, ws, actor, "wiki_comment_property", property.ID, spaceID, property)
}

func (s *Store) CreateWikiCommentProperty(ctx context.Context, ws, actor, commentID, key string, value json.RawMessage) (*models.WikiContentProperty, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = s.commentPropertyWriteGate(ctx, tx, ws, actor, commentID); err != nil {
		return nil, err
	}
	var propertyID string
	if err = tx.QueryRow(ctx, `INSERT INTO wiki_comment_properties(comment_id,key,value,author_id) VALUES($1::bigint,$2,$3::jsonb,$4) RETURNING id::text`, commentID, key, value, actor).Scan(&propertyID); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO wiki_comment_property_versions(property_id,version,value,author_id) VALUES($1::bigint,1,$2::jsonb,$3)`, propertyID, value, actor); err != nil {
		return nil, err
	}
	property, err := scanWikiCommentProperty(tx.QueryRow(ctx, wikiCommentPropertySelect+` WHERE cp.id::text=$1`, propertyID))
	if err != nil {
		return nil, err
	}
	if err = s.commentPropertyAction(ctx, tx, ws, actor, commentID, property, models.OpUpsert); err != nil {
		return nil, err
	}
	return property, tx.Commit(ctx)
}

func (s *Store) UpdateWikiCommentProperty(ctx context.Context, ws, actor, commentID, propertyID, key string, value json.RawMessage, version int, message string) (*models.WikiContentProperty, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = s.commentPropertyWriteGate(ctx, tx, ws, actor, commentID); err != nil {
		return nil, err
	}
	var oldKey string
	var oldVersion int
	if err = tx.QueryRow(ctx, `SELECT key,version FROM wiki_comment_properties WHERE comment_id::text=$1 AND id::text=$2 FOR UPDATE`, commentID, propertyID).Scan(&oldKey, &oldVersion); err != nil {
		return nil, err
	}
	// The key names the property and the version guards against a lost update,
	// exactly as for page properties.
	if oldKey != key || version != oldVersion+1 {
		return nil, ErrWikiPropertyConflict
	}
	if _, err = tx.Exec(ctx, `UPDATE wiki_comment_properties SET value=$2::jsonb,version=$3,author_id=$4,updated_at=now() WHERE id::text=$1`, propertyID, value, version, actor); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO wiki_comment_property_versions(property_id,version,value,author_id,message) VALUES($1::bigint,$2,$3::jsonb,$4,$5)`, propertyID, version, value, actor, message); err != nil {
		return nil, err
	}
	property, err := scanWikiCommentProperty(tx.QueryRow(ctx, wikiCommentPropertySelect+` WHERE cp.id::text=$1`, propertyID))
	if err != nil {
		return nil, err
	}
	if err = s.commentPropertyAction(ctx, tx, ws, actor, commentID, property, models.OpUpsert); err != nil {
		return nil, err
	}
	return property, tx.Commit(ctx)
}

func (s *Store) DeleteWikiCommentProperty(ctx context.Context, ws, actor, commentID, propertyID string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = s.commentPropertyWriteGate(ctx, tx, ws, actor, commentID); err != nil {
		return err
	}
	property, err := scanWikiCommentProperty(tx.QueryRow(ctx, wikiCommentPropertySelect+` WHERE cp.comment_id::text=$1 AND cp.id::text=$2 FOR UPDATE OF cp`, commentID, propertyID))
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM wiki_comment_properties WHERE id::text=$1`, propertyID); err != nil {
		return err
	}
	if err = s.commentPropertyAction(ctx, tx, ws, actor, commentID, property, models.OpDelete); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
