package store

import (
	"context"
	"fmt"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

func (s *Store) WikiBlogCustomContent(ctx context.Context, ws, actor, blogPostID, contentType, order string) ([]models.WikiBlogCustomContent, error) {
	blog, err := s.WikiBlogPost(ctx, ws, actor, blogPostID)
	if err != nil || blog.Status != "current" {
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
	rows, err := s.Pool.Query(ctx, `SELECT cc.id::text,cc.custom_type,cc.status,cc.title,cc.space_id::text,cc.parent_blog_post_id::text,cc.author_id,to_char(cc.created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),ct.body_representation,cc.body,cc.version,cc.author_id,to_char(cc.created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"') FROM wiki_content cc JOIN wiki_custom_content_types ct ON ct.type=cc.custom_type WHERE cc.type='custom' AND cc.parent_blog_post_id::text=$1 AND cc.custom_type=$2 AND cc.status='current' ORDER BY `+orderSQL, blogPostID, contentType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []models.WikiBlogCustomContent{}
	for rows.Next() {
		var value models.WikiBlogCustomContent
		if err := rows.Scan(&value.ID, &value.Type, &value.Status, &value.Title, &value.SpaceID, &value.BlogPostID, &value.AuthorID, &value.CreatedAt, &value.BodyRepresentation, &value.Body.Value, &value.Version.Number, &value.Version.AuthorID, &value.Version.CreatedAt); err != nil {
			return nil, err
		}
		value.Body.Representation = representation
		values = append(values, value)
	}
	return values, rows.Err()
}
