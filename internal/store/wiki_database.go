package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

func (s *Store) WikiDatabaseData(ctx context.Context, ws, actor, databaseID string) (*models.WikiDatabaseData, error) {
	if _, err := s.WikiContent(ctx, ws, actor, databaseID, "database"); err != nil {
		return nil, err
	}
	data := &models.WikiDatabaseData{}
	rows, err := s.Pool.Query(ctx, `SELECT id::text,database_id::text,column_key,name,field_type,options,position FROM wiki_database_columns WHERE database_id::text=$1 ORDER BY position,id`, databaseID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var column models.WikiDatabaseColumn
		var options []byte
		if err := rows.Scan(&column.ID, &column.DatabaseID, &column.Key, &column.Name, &column.Type, &options, &column.Position); err != nil {
			rows.Close()
			return nil, err
		}
		if err := json.Unmarshal(options, &column.Options); err != nil {
			rows.Close()
			return nil, err
		}
		data.Columns = append(data.Columns, column)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	rows, err = s.Pool.Query(ctx, `SELECT id::text,database_id::text,values,COALESCE(created_by,''),to_char(created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),to_char(updated_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"') FROM wiki_database_rows WHERE database_id::text=$1 ORDER BY id`, databaseID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var row models.WikiDatabaseRow
		var values []byte
		if err := rows.Scan(&row.ID, &row.DatabaseID, &values, &row.CreatedBy, &row.CreatedAt, &row.UpdatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		if err := json.Unmarshal(values, &row.Values); err != nil {
			rows.Close()
			return nil, err
		}
		data.Rows = append(data.Rows, row)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	rows, err = s.Pool.Query(ctx, `SELECT id::text,database_id::text,name,sort_key,sort_direction,filter_key,filter_value FROM wiki_database_views WHERE database_id::text=$1 ORDER BY lower(name),id`, databaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var view models.WikiDatabaseView
		if err := rows.Scan(&view.ID, &view.DatabaseID, &view.Name, &view.SortKey, &view.SortDirection, &view.FilterKey, &view.FilterValue); err != nil {
			return nil, err
		}
		data.Views = append(data.Views, view)
	}
	return data, rows.Err()
}

func lockWikiDatabase(ctx context.Context, tx pgx.Tx, ws, actor, databaseID string) (*models.WikiContent, error) {
	return scanWikiContent(tx.QueryRow(ctx, wikiContentSelect+` WHERE s.workspace_id=$1 AND `+wikiContentWritableFor("database")+` AND c.id::text=$3 AND c.type='database' AND c.status='current' FOR UPDATE OF c`, ws, actor, databaseID))
}

func (s *Store) AddWikiDatabaseColumn(ctx context.Context, ws, actor, databaseID string, column models.WikiDatabaseColumn) (*models.WikiDatabaseColumn, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	database, err := lockWikiDatabase(ctx, tx, ws, actor, databaseID)
	if err != nil {
		return nil, err
	}
	rawOptions, err := json.Marshal(column.Options)
	if err != nil {
		return nil, fmt.Errorf("encode database column options: %w", err)
	}
	column.DatabaseID = databaseID
	err = tx.QueryRow(ctx, `INSERT INTO wiki_database_columns(database_id,column_key,name,field_type,options,position) VALUES($1::bigint,$2,$3,$4,$5,COALESCE((SELECT max(position)+1 FROM wiki_database_columns WHERE database_id=$1::bigint),0)) RETURNING id::text,position`, databaseID, column.Key, column.Name, column.Type, rawOptions).Scan(&column.ID, &column.Position)
	if err != nil {
		return nil, err
	}
	if err := finishWikiContentMutation(ctx, tx, ws, actor, database); err != nil {
		return nil, err
	}
	return &column, nil
}

func (s *Store) DeleteWikiDatabaseColumn(ctx context.Context, ws, actor, databaseID, columnID string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	database, err := lockWikiDatabase(ctx, tx, ws, actor, databaseID)
	if err != nil {
		return err
	}
	var key string
	if err := tx.QueryRow(ctx, `DELETE FROM wiki_database_columns WHERE database_id::text=$1 AND id::text=$2 RETURNING column_key`, databaseID, columnID).Scan(&key); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE wiki_database_rows SET values=values-$2,updated_at=now() WHERE database_id::text=$1`, databaseID, key); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE wiki_database_views SET sort_key=CASE WHEN sort_key=$2 THEN '' ELSE sort_key END,filter_key=CASE WHEN filter_key=$2 THEN '' ELSE filter_key END,filter_value=CASE WHEN filter_key=$2 THEN '' ELSE filter_value END WHERE database_id::text=$1`, databaseID, key); err != nil {
		return err
	}
	return finishWikiContentMutation(ctx, tx, ws, actor, database)
}

func (s *Store) SaveWikiDatabaseRow(ctx context.Context, ws, actor, databaseID, rowID string, values map[string]string) (*models.WikiDatabaseRow, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	database, err := lockWikiDatabase(ctx, tx, ws, actor, databaseID)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return nil, fmt.Errorf("encode database row values: %w", err)
	}
	row := &models.WikiDatabaseRow{ID: rowID, DatabaseID: databaseID, Values: values, CreatedBy: actor}
	if rowID == "" {
		err = tx.QueryRow(ctx, `INSERT INTO wiki_database_rows(database_id,values,created_by) VALUES($1::bigint,$2,$3) RETURNING id::text,to_char(created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),to_char(updated_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"')`, databaseID, raw, actor).Scan(&row.ID, &row.CreatedAt, &row.UpdatedAt)
	} else {
		err = tx.QueryRow(ctx, `UPDATE wiki_database_rows SET values=$3,updated_at=now() WHERE database_id::text=$1 AND id::text=$2 RETURNING COALESCE(created_by,''),to_char(created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),to_char(updated_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"')`, databaseID, rowID, raw).Scan(&row.CreatedBy, &row.CreatedAt, &row.UpdatedAt)
	}
	if err != nil {
		return nil, err
	}
	if err := finishWikiContentMutation(ctx, tx, ws, actor, database); err != nil {
		return nil, err
	}
	return row, nil
}

func (s *Store) DeleteWikiDatabaseRow(ctx context.Context, ws, actor, databaseID, rowID string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	database, err := lockWikiDatabase(ctx, tx, ws, actor, databaseID)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM wiki_database_rows WHERE database_id::text=$1 AND id::text=$2`, databaseID, rowID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return finishWikiContentMutation(ctx, tx, ws, actor, database)
}

func (s *Store) SaveWikiDatabaseView(ctx context.Context, ws, actor, databaseID string, view models.WikiDatabaseView) (*models.WikiDatabaseView, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	database, err := lockWikiDatabase(ctx, tx, ws, actor, databaseID)
	if err != nil {
		return nil, err
	}
	view.DatabaseID = databaseID
	err = tx.QueryRow(ctx, `INSERT INTO wiki_database_views(database_id,name,sort_key,sort_direction,filter_key,filter_value,created_by) VALUES($1::bigint,$2,$3,$4,$5,$6,$7) RETURNING id::text`, databaseID, view.Name, view.SortKey, view.SortDirection, view.FilterKey, view.FilterValue, actor).Scan(&view.ID)
	if err != nil {
		return nil, err
	}
	if err := finishWikiContentMutation(ctx, tx, ws, actor, database); err != nil {
		return nil, err
	}
	return &view, nil
}

func (s *Store) DeleteWikiDatabaseView(ctx context.Context, ws, actor, databaseID, viewID string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	database, err := lockWikiDatabase(ctx, tx, ws, actor, databaseID)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM wiki_database_views WHERE database_id::text=$1 AND id::text=$2`, databaseID, viewID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	if err := finishWikiContentMutation(ctx, tx, ws, actor, database); err != nil {
		return fmt.Errorf("finish database view deletion: %w", err)
	}
	return nil
}
