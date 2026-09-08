package store

import (
	"context"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

func (s *Store) WikiWhiteboardData(ctx context.Context, ws, actor, whiteboardID string) (*models.WikiWhiteboardData, error) {
	if _, err := s.WikiContent(ctx, ws, actor, whiteboardID, "whiteboard"); err != nil {
		return nil, err
	}
	data := &models.WikiWhiteboardData{}
	rows, err := s.Pool.Query(ctx, `SELECT id::text,whiteboard_id::text,object_type,title,body,x,y,width,height,color FROM wiki_whiteboard_objects WHERE whiteboard_id::text=$1 ORDER BY id`, whiteboardID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var object models.WikiWhiteboardObject
		if err := rows.Scan(&object.ID, &object.WhiteboardID, &object.Type, &object.Title, &object.Body, &object.X, &object.Y, &object.Width, &object.Height, &object.Color); err != nil {
			rows.Close()
			return nil, err
		}
		data.Objects = append(data.Objects, object)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	rows, err = s.Pool.Query(ctx, `SELECT c.id::text,c.whiteboard_id::text,c.from_object_id::text,c.to_object_id::text,c.label,c.line_style,f.x+f.width/2,f.y+f.height/2,t.x+t.width/2,t.y+t.height/2 FROM wiki_whiteboard_connectors c JOIN wiki_whiteboard_objects f ON f.id=c.from_object_id JOIN wiki_whiteboard_objects t ON t.id=c.to_object_id WHERE c.whiteboard_id::text=$1 ORDER BY c.id`, whiteboardID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var connector models.WikiWhiteboardConnector
		if err := rows.Scan(&connector.ID, &connector.WhiteboardID, &connector.FromObjectID, &connector.ToObjectID, &connector.Label, &connector.Style, &connector.FromX, &connector.FromY, &connector.ToX, &connector.ToY); err != nil {
			return nil, err
		}
		data.Connectors = append(data.Connectors, connector)
	}
	return data, rows.Err()
}

func lockWikiWhiteboard(ctx context.Context, tx pgx.Tx, ws, actor, id string) (*models.WikiContent, error) {
	return scanWikiContent(tx.QueryRow(ctx, wikiContentSelect+` WHERE s.workspace_id=$1 AND `+wikiContentWritableFor("whiteboard")+` AND c.id::text=$3 AND c.type='whiteboard' AND c.status='current' FOR UPDATE OF c`, ws, actor, id))
}

func (s *Store) SaveWikiWhiteboardObject(ctx context.Context, ws, actor, whiteboardID string, object models.WikiWhiteboardObject) (*models.WikiWhiteboardObject, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	whiteboard, err := lockWikiWhiteboard(ctx, tx, ws, actor, whiteboardID)
	if err != nil {
		return nil, err
	}
	object.WhiteboardID = whiteboardID
	if object.ID == "" {
		err = tx.QueryRow(ctx, `INSERT INTO wiki_whiteboard_objects(whiteboard_id,object_type,title,body,x,y,width,height,color,created_by) VALUES($1::bigint,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id::text`, whiteboardID, object.Type, object.Title, object.Body, object.X, object.Y, object.Width, object.Height, object.Color, actor).Scan(&object.ID)
	} else {
		tag, updateErr := tx.Exec(ctx, `UPDATE wiki_whiteboard_objects SET object_type=$3,title=$4,body=$5,x=$6,y=$7,width=$8,height=$9,color=$10,updated_at=now() WHERE whiteboard_id::text=$1 AND id::text=$2`, whiteboardID, object.ID, object.Type, object.Title, object.Body, object.X, object.Y, object.Width, object.Height, object.Color)
		err = updateErr
		if err == nil && tag.RowsAffected() == 0 {
			err = pgx.ErrNoRows
		}
	}
	if err != nil {
		return nil, err
	}
	if err := finishWikiContentMutation(ctx, tx, ws, actor, whiteboard); err != nil {
		return nil, err
	}
	return &object, nil
}

func (s *Store) DeleteWikiWhiteboardObject(ctx context.Context, ws, actor, whiteboardID, objectID string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	whiteboard, err := lockWikiWhiteboard(ctx, tx, ws, actor, whiteboardID)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM wiki_whiteboard_objects WHERE whiteboard_id::text=$1 AND id::text=$2`, whiteboardID, objectID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return finishWikiContentMutation(ctx, tx, ws, actor, whiteboard)
}

func (s *Store) SaveWikiWhiteboardConnector(ctx context.Context, ws, actor, whiteboardID string, connector models.WikiWhiteboardConnector) (*models.WikiWhiteboardConnector, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	whiteboard, err := lockWikiWhiteboard(ctx, tx, ws, actor, whiteboardID)
	if err != nil {
		return nil, err
	}
	connector.WhiteboardID = whiteboardID
	err = tx.QueryRow(ctx, `INSERT INTO wiki_whiteboard_connectors(whiteboard_id,from_object_id,to_object_id,label,line_style,created_by) SELECT $1::bigint,$2::bigint,$3::bigint,$4,$5,$6 WHERE EXISTS(SELECT 1 FROM wiki_whiteboard_objects WHERE whiteboard_id::text=$1::text AND id::text=$2::text) AND EXISTS(SELECT 1 FROM wiki_whiteboard_objects WHERE whiteboard_id::text=$1::text AND id::text=$3::text) RETURNING id::text`, whiteboardID, connector.FromObjectID, connector.ToObjectID, connector.Label, connector.Style, actor).Scan(&connector.ID)
	if err != nil {
		return nil, err
	}
	if err := finishWikiContentMutation(ctx, tx, ws, actor, whiteboard); err != nil {
		return nil, err
	}
	return &connector, nil
}

func (s *Store) DeleteWikiWhiteboardConnector(ctx context.Context, ws, actor, whiteboardID, connectorID string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	whiteboard, err := lockWikiWhiteboard(ctx, tx, ws, actor, whiteboardID)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM wiki_whiteboard_connectors WHERE whiteboard_id::text=$1 AND id::text=$2`, whiteboardID, connectorID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return finishWikiContentMutation(ctx, tx, ws, actor, whiteboard)
}
