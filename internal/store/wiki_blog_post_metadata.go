package store

import (
	"context"
	"encoding/json"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

const wikiBlogPostPropertySelect = `SELECT bp.id::text,bp.blog_post_id::text,bp.key,bp.value,pv.version,pv.message,pv.author_id,to_char(pv.created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"')
  FROM wiki_blog_post_properties bp JOIN wiki_blog_post_property_versions pv ON pv.property_id=bp.id AND pv.version=bp.version
  JOIN wiki_blog_posts b ON b.id=bp.blog_post_id JOIN wiki_spaces s ON s.id=b.space_id`

func scanWikiBlogPostProperty(row pgx.Row) (*models.WikiContentProperty, error) {
	property := &models.WikiContentProperty{}
	err := row.Scan(&property.ID, &property.ContentID, &property.Key, &property.Value,
		&property.Version.Number, &property.Version.Message, &property.Version.AuthorID,
		&property.Version.CreatedAt)
	return property, err
}

func (s *Store) CanUpdateWikiBlogPost(ctx context.Context, ws, actor, id string) (bool, error) {
	_, err := s.WikiBlogPost(ctx, ws, actor, id)
	if err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) WikiBlogPostProperties(ctx context.Context, ws, actor, blogPostID, key string) ([]models.WikiContentProperty, error) {
	if _, err := s.WikiBlogPost(ctx, ws, actor, blogPostID); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, wikiBlogPostPropertySelect+` WHERE s.workspace_id=$1 AND `+wikiBlogPostVisible+` AND b.id::text=$3 AND b.status='current' AND ($4='' OR bp.key=$4) ORDER BY bp.key,bp.id`, ws, actor, blogPostID, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	properties := []models.WikiContentProperty{}
	for rows.Next() {
		property, err := scanWikiBlogPostProperty(rows)
		if err != nil {
			return nil, err
		}
		properties = append(properties, *property)
	}
	return properties, rows.Err()
}

func (s *Store) WikiBlogPostProperty(ctx context.Context, ws, actor, blogPostID, propertyID string) (*models.WikiContentProperty, error) {
	return scanWikiBlogPostProperty(s.Pool.QueryRow(ctx, wikiBlogPostPropertySelect+` WHERE s.workspace_id=$1 AND `+wikiBlogPostVisible+` AND b.id::text=$3 AND b.status='current' AND bp.id::text=$4`, ws, actor, blogPostID, propertyID))
}

func blogPostMetadataAction(ctx context.Context, tx pgx.Tx, ws, actor, entityType, entityID, op string, blog *models.WikiBlogPost, value any) error {
	seq, err := nextSeq(ctx, tx, ws)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{"wikiSpaceId": blog.SpaceID, "wiki_blogpost": blog, entityType: value})
	if err != nil {
		return err
	}
	return appendAction(ctx, tx, &models.Action{WorkspaceID: ws, Seq: seq, EntityType: entityType, EntityID: entityID, Op: op, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actor})
}

func (s *Store) CreateWikiBlogPostProperty(ctx context.Context, ws, actor, blogPostID, key string, value json.RawMessage) (*models.WikiContentProperty, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	blog, err := scanWikiBlogPost(tx.QueryRow(ctx, wikiBlogPostSelect+` WHERE s.workspace_id=$1 AND `+wikiBlogPostVisible+` AND `+wikiBlogPostWritable+` AND b.id::text=$3 AND b.status='current' FOR SHARE OF b`, ws, actor, blogPostID))
	if err != nil {
		return nil, err
	}
	var propertyID string
	if err = tx.QueryRow(ctx, `INSERT INTO wiki_blog_post_properties(blog_post_id,key,value,author_id) VALUES($1::bigint,$2,$3::jsonb,$4) RETURNING id::text`, blogPostID, key, value, actor).Scan(&propertyID); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO wiki_blog_post_property_versions(property_id,version,value,author_id) VALUES($1::bigint,1,$2::jsonb,$3)`, propertyID, value, actor); err != nil {
		return nil, err
	}
	property, err := scanWikiBlogPostProperty(tx.QueryRow(ctx, wikiBlogPostPropertySelect+` WHERE bp.id::text=$1`, propertyID))
	if err != nil {
		return nil, err
	}
	if err = blogPostMetadataAction(ctx, tx, ws, actor, "wiki_blogpost_property", property.ID, models.OpUpsert, blog, property); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return property, nil
}

func (s *Store) UpdateWikiBlogPostProperty(ctx context.Context, ws, actor, blogPostID, propertyID, key string, value json.RawMessage, version int, message string) (*models.WikiContentProperty, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	blog, err := scanWikiBlogPost(tx.QueryRow(ctx, wikiBlogPostSelect+` WHERE s.workspace_id=$1 AND `+wikiBlogPostVisible+` AND `+wikiBlogPostWritable+` AND b.id::text=$3 AND b.status='current' FOR SHARE OF b`, ws, actor, blogPostID))
	if err != nil {
		return nil, err
	}
	var oldKey string
	var oldVersion int
	if err = tx.QueryRow(ctx, `SELECT key,version FROM wiki_blog_post_properties WHERE blog_post_id::text=$1 AND id::text=$2 FOR UPDATE`, blogPostID, propertyID).Scan(&oldKey, &oldVersion); err != nil {
		return nil, err
	}
	if oldKey != key || version != oldVersion+1 {
		return nil, ErrWikiPropertyConflict
	}
	if _, err = tx.Exec(ctx, `UPDATE wiki_blog_post_properties SET value=$2::jsonb,version=$3,author_id=$4,updated_at=now() WHERE id::text=$1`, propertyID, value, version, actor); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO wiki_blog_post_property_versions(property_id,version,value,author_id,message) VALUES($1::bigint,$2,$3::jsonb,$4,$5)`, propertyID, version, value, actor, message); err != nil {
		return nil, err
	}
	property, err := scanWikiBlogPostProperty(tx.QueryRow(ctx, wikiBlogPostPropertySelect+` WHERE bp.id::text=$1`, propertyID))
	if err != nil {
		return nil, err
	}
	if err = blogPostMetadataAction(ctx, tx, ws, actor, "wiki_blogpost_property", property.ID, models.OpUpsert, blog, property); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return property, nil
}

func (s *Store) DeleteWikiBlogPostProperty(ctx context.Context, ws, actor, blogPostID, propertyID string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	blog, err := scanWikiBlogPost(tx.QueryRow(ctx, wikiBlogPostSelect+` WHERE s.workspace_id=$1 AND `+wikiBlogPostVisible+` AND `+wikiBlogPostWritable+` AND b.id::text=$3 AND b.status='current' FOR SHARE OF b`, ws, actor, blogPostID))
	if err != nil {
		return err
	}
	property, err := scanWikiBlogPostProperty(tx.QueryRow(ctx, wikiBlogPostPropertySelect+` WHERE bp.blog_post_id::text=$1 AND bp.id::text=$2 FOR UPDATE OF bp`, blogPostID, propertyID))
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM wiki_blog_post_properties WHERE id::text=$1`, propertyID); err != nil {
		return err
	}
	if err = blogPostMetadataAction(ctx, tx, ws, actor, "wiki_blogpost_property", property.ID, models.OpDelete, blog, property); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) SetWikiBlogPostClassification(ctx context.Context, ws, actor, id, levelID string) (*models.WikiBlogPost, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	blog, err := scanWikiBlogPost(tx.QueryRow(ctx, wikiBlogPostSelect+` WHERE s.workspace_id=$1 AND `+wikiBlogPostVisible+` AND `+wikiBlogPostWritable+` AND b.id::text=$3 AND b.status='current' FOR UPDATE OF b`, ws, actor, id))
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE wiki_blog_posts SET classification_level=$2 WHERE id::text=$1`, id, levelID); err != nil {
		return nil, err
	}
	blog.ClassificationLevel = levelID
	if err = wikiAction(ctx, tx, ws, actor, "wiki_blogpost", blog.ID, blog.SpaceID, blog); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return blog, nil
}

func (s *Store) WikiBlogPostLabels(ctx context.Context, ws, actor, id string) ([]models.WikiLabel, error) {
	if _, err := s.WikiBlogPost(ctx, ws, actor, id); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, wikiLabelSelect+` JOIN wiki_blog_post_labels bl ON bl.label_id=l.id WHERE bl.blog_post_id::text=$1 ORDER BY l.created_at,l.id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanWikiLabels(rows)
}

func (s *Store) WikiBlogPostsByLabel(ctx context.Context, ws, actor, labelID string) ([]*models.WikiBlogPost, error) {
	labels, err := s.WikiLabels(ctx, ws, actor)
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
	rows, err := s.Pool.Query(ctx, wikiBlogPostSelect+` JOIN wiki_blog_post_labels bl ON bl.blog_post_id=b.id WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiBlogPostVisible+` AND b.status='current' AND bl.label_id::text=$3 ORDER BY b.id`, ws, actor, labelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	blogs := []*models.WikiBlogPost{}
	for rows.Next() {
		blog, err := scanWikiBlogPost(rows)
		if err != nil {
			return nil, err
		}
		blogs = append(blogs, blog)
	}
	return blogs, rows.Err()
}

func (s *Store) AddWikiBlogPostLabels(ctx context.Context, ws, actor, id string, input []models.WikiLabel) ([]models.WikiLabel, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	blog, err := scanWikiBlogPost(tx.QueryRow(ctx, wikiBlogPostSelect+` WHERE s.workspace_id=$1 AND `+wikiBlogPostVisible+` AND `+wikiBlogPostWritable+` AND b.id::text=$3 AND b.status='current' FOR SHARE OF b`, ws, actor, id))
	if err != nil {
		return nil, err
	}
	for _, item := range input {
		label, err := ensureWikiLabel(ctx, tx, ws, item)
		if err != nil {
			return nil, err
		}
		tag, err := tx.Exec(ctx, `INSERT INTO wiki_blog_post_labels(blog_post_id,label_id,author_id) VALUES($1::bigint,$2::bigint,$3) ON CONFLICT DO NOTHING`, id, label.ID, actor)
		if err != nil {
			return nil, err
		}
		if tag.RowsAffected() > 0 {
			if err = blogPostMetadataAction(ctx, tx, ws, actor, "wiki_blogpost_label", id+":"+label.ID, models.OpUpsert, blog, label); err != nil {
				return nil, err
			}
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.WikiBlogPostLabels(ctx, ws, actor, id)
}

func (s *Store) RemoveWikiBlogPostLabel(ctx context.Context, ws, actor, id string, input models.WikiLabel) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	blog, err := scanWikiBlogPost(tx.QueryRow(ctx, wikiBlogPostSelect+` WHERE s.workspace_id=$1 AND `+wikiBlogPostVisible+` AND `+wikiBlogPostWritable+` AND b.id::text=$3 AND b.status='current' FOR SHARE OF b`, ws, actor, id))
	if err != nil {
		return err
	}
	var label models.WikiLabel
	if err = tx.QueryRow(ctx, `SELECT l.id::text,l.name,l.prefix,to_char(l.created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"') FROM wiki_blog_post_labels bl JOIN wiki_labels l ON l.id=bl.label_id WHERE bl.blog_post_id::text=$1 AND l.prefix=$2 AND l.name=$3 FOR UPDATE OF bl`, id, input.Prefix, input.Name).Scan(&label.ID, &label.Name, &label.Prefix, &label.CreatedAt); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM wiki_blog_post_labels WHERE blog_post_id::text=$1 AND label_id::text=$2`, id, label.ID); err != nil {
		return err
	}
	if err = blogPostMetadataAction(ctx, tx, ws, actor, "wiki_blogpost_label", id+":"+label.ID, models.OpDelete, blog, label); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) WikiBlogPostLikes(ctx context.Context, ws, actor, id string) ([]string, error) {
	blog, err := s.WikiBlogPost(ctx, ws, actor, id)
	if err != nil || blog.Status != "current" {
		if err == nil {
			err = pgx.ErrNoRows
		}
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT user_id FROM wiki_blog_post_likes WHERE blog_post_id::text=$1 ORDER BY created_at,user_id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	likes := []string{}
	for rows.Next() {
		var user string
		if err := rows.Scan(&user); err != nil {
			return nil, err
		}
		likes = append(likes, user)
	}
	return likes, rows.Err()
}

func (s *Store) SetWikiBlogPostLike(ctx context.Context, ws, actor, id string, liked bool) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	blog, err := scanWikiBlogPost(tx.QueryRow(ctx, wikiBlogPostSelect+` WHERE s.workspace_id=$1 AND `+wikiBlogPostVisible+` AND b.id::text=$3 AND b.status='current' FOR SHARE OF b`, ws, actor, id))
	if err != nil {
		return err
	}
	var changed bool
	if liked {
		tag, execErr := tx.Exec(ctx, `INSERT INTO wiki_blog_post_likes(blog_post_id,user_id) VALUES($1::bigint,$2) ON CONFLICT DO NOTHING`, id, actor)
		err, changed = execErr, tag.RowsAffected() > 0
	} else {
		tag, execErr := tx.Exec(ctx, `DELETE FROM wiki_blog_post_likes WHERE blog_post_id::text=$1 AND user_id=$2`, id, actor)
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
		if err = blogPostMetadataAction(ctx, tx, ws, actor, "wiki_blogpost_like", id+":"+actor, op, blog, map[string]any{"accountId": actor, "liked": liked}); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
