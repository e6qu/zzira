package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

// An object carries what people know about it that its attributes cannot say:
// why a service is tier 1, what the vendor answered about the renewal, which
// laptop is being replaced. Agents of the desk read and write these; the
// person who wrote one, and any site administrator, can take it away.

// ServiceAssetObjectComments are what has been said about one object, oldest
// first.
func (s *Store) ServiceAssetObjectComments(ctx context.Context, ws, actor, objectID string) ([]models.ServiceAssetObjectComment, error) {
	if _, _, err := s.ServiceAssetObject(ctx, ws, actor, objectID); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT c.id::text,c.object_id::text,c.author_id,COALESCE(u.display_name,''),c.body,
		to_char(c.created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"')
		FROM service_asset_object_comments c LEFT JOIN users u ON u.id=c.author_id
		WHERE c.object_id::text=$1 ORDER BY c.created_at,c.id`, objectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectAssetComments(rows)
}

// ServiceAssetInventoryComments are the comments on every object of one
// desk's inventory, by object id, so a page reads them in one query.
func (s *Store) ServiceAssetInventoryComments(ctx context.Context, ws, actor, deskID string) (map[string][]models.ServiceAssetObjectComment, error) {
	allowed, err := s.IsServiceAgent(ctx, ws, deskID, actor)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, ErrProjectPermission
	}
	rows, err := s.Pool.Query(ctx, `SELECT c.id::text,c.object_id::text,c.author_id,COALESCE(u.display_name,''),c.body,
		to_char(c.created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"')
		FROM service_asset_object_comments c
		JOIN service_asset_objects o ON o.id=c.object_id
		JOIN service_asset_schemas sc ON sc.id=o.schema_id
		JOIN service_desks sd ON sd.id=sc.service_desk_id
		LEFT JOIN users u ON u.id=c.author_id
		WHERE sd.workspace_id=$1 AND sc.service_desk_id=$2 ORDER BY c.created_at,c.id`, ws, deskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	comments, err := collectAssetComments(rows)
	if err != nil {
		return nil, err
	}
	byObject := map[string][]models.ServiceAssetObjectComment{}
	for _, comment := range comments {
		byObject[comment.ObjectID] = append(byObject[comment.ObjectID], comment)
	}
	return byObject, nil
}

func collectAssetComments(rows pgx.Rows) ([]models.ServiceAssetObjectComment, error) {
	comments := []models.ServiceAssetObjectComment{}
	for rows.Next() {
		var comment models.ServiceAssetObjectComment
		if err := rows.Scan(&comment.ID, &comment.ObjectID, &comment.AuthorID, &comment.AuthorName, &comment.Body, &comment.At); err != nil {
			return nil, err
		}
		comments = append(comments, comment)
	}
	return comments, rows.Err()
}

// CreateServiceAssetObjectComment says something about an object, as an agent
// of the desk it belongs to.
func (s *Store) CreateServiceAssetObjectComment(ctx context.Context, ws, actor, objectID, body string) (*models.ServiceAssetObjectComment, error) {
	if _, _, err := s.ServiceAssetObject(ctx, ws, actor, objectID); err != nil {
		return nil, err
	}
	if body = strings.TrimSpace(body); body == "" || len([]rune(body)) > 10000 {
		return nil, fmt.Errorf("a comment needs between 1 and 10000 characters")
	}
	comment := &models.ServiceAssetObjectComment{ObjectID: objectID, AuthorID: actor, Body: body}
	err := s.Pool.QueryRow(ctx, `INSERT INTO service_asset_object_comments(object_id,author_id,body)
		VALUES($1::uuid,$2,$3)
		RETURNING id::text,to_char(created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"')`,
		objectID, actor, body).Scan(&comment.ID, &comment.At)
	if err != nil {
		return nil, err
	}
	if err := s.Pool.QueryRow(ctx, `SELECT COALESCE(display_name,'') FROM users WHERE id=$1`, actor).Scan(&comment.AuthorName); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	return comment, nil
}

// DeleteServiceAssetObjectComment removes a comment. Whoever wrote it may,
// and so may a site administrator; nobody else.
func (s *Store) DeleteServiceAssetObjectComment(ctx context.Context, ws, actor, objectID, commentID string) error {
	if _, _, err := s.ServiceAssetObject(ctx, ws, actor, objectID); err != nil {
		return err
	}
	admin, err := s.IsAdmin(ctx, ws, actor)
	if err != nil {
		return err
	}
	result, err := s.Pool.Exec(ctx, `DELETE FROM service_asset_object_comments
		WHERE id::text=$1 AND object_id::text=$2 AND ($3 OR author_id=$4)`, commentID, objectID, admin, actor)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("that comment is not one you can delete")
	}
	return nil
}
