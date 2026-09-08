package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

var ErrJQLFunctionPrecomputationNotFound = errors.New("JQL function precomputation not found")

func (s *Store) ActiveAppInstallationForPrincipal(ctx context.Context, workspaceID, principalID string) (installationID, appKey string, err error) {
	err = s.Pool.QueryRow(ctx, `SELECT id,app_key FROM app_installations WHERE workspace_id=$1 AND principal_id=$2 AND status='active'`, workspaceID, principalID).Scan(&installationID, &appKey)
	return
}

func scanJQLFunctionPrecomputation(row interface{ Scan(...any) error }) (models.JQLFunctionPrecomputation, error) {
	var value models.JQLFunctionPrecomputation
	err := row.Scan(&value.ID, &value.FunctionKey, &value.FunctionName, &value.Field, &value.Operator, &value.Arguments, &value.Value, &value.Error, &value.CreatedAt, &value.UpdatedAt, &value.UsedAt)
	value.CreatedAt = value.CreatedAt.UTC()
	value.UpdatedAt = value.UpdatedAt.UTC()
	value.UsedAt = value.UsedAt.UTC()
	return value, err
}

const jqlFunctionPrecomputationSelect = `SELECT id::text,function_key,function_name,field,operator,arguments,value,error,created_at,updated_at,used_at FROM jql_function_precomputations `

// EnsureJQLFunctionPrecomputation records an invocation and refreshes its last
// use time. The stored result survives repeated uses until the app updates it.
func (s *Store) EnsureJQLFunctionPrecomputation(ctx context.Context, installationID, functionKey, functionName, field, operator string, arguments []string, usedAt time.Time) (*models.JQLFunctionPrecomputation, error) {
	if usedAt.IsZero() {
		usedAt = time.Now().UTC()
	}
	if arguments == nil {
		arguments = []string{}
	}
	value, err := scanJQLFunctionPrecomputation(s.Pool.QueryRow(ctx, `
		INSERT INTO jql_function_precomputations(workspace_id,installation_id,function_key,function_name,field,operator,arguments,used_at)
		SELECT workspace_id,id,$2,$3,$4,$5,$6,$7 FROM app_installations WHERE id=$1 AND status='active'
		ON CONFLICT(installation_id,function_key,function_name,field,operator,arguments)
		DO UPDATE SET used_at=GREATEST(jql_function_precomputations.used_at,EXCLUDED.used_at)
		RETURNING id::text,function_key,function_name,field,operator,arguments,value,error,created_at,updated_at,used_at`,
		installationID, functionKey, functionName, field, strings.ToLower(operator), arguments, usedAt))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("active app installation does not exist: %w", err)
	}
	return &value, err
}

func jqlFunctionOrder(orderBy string) (string, error) {
	direction := "ASC"
	if strings.HasPrefix(orderBy, "-") {
		direction, orderBy = "DESC", strings.TrimPrefix(orderBy, "-")
	} else {
		orderBy = strings.TrimPrefix(orderBy, "+")
	}
	column := map[string]string{"": "function_key", "functionKey": "function_key", "used": "used_at", "created": "created_at", "updated": "updated_at"}[orderBy]
	if column == "" {
		return "", fmt.Errorf("unsupported orderBy %q", orderBy)
	}
	return column + " " + direction + ",id ASC", nil
}

func (s *Store) JQLFunctionPrecomputations(ctx context.Context, installationID string, functionKeys []string, startAt, maxResults int, orderBy string) ([]models.JQLFunctionPrecomputation, int64, error) {
	order, err := jqlFunctionOrder(orderBy)
	if err != nil {
		return nil, 0, err
	}
	var total int64
	if err = s.Pool.QueryRow(ctx, `SELECT count(*) FROM jql_function_precomputations WHERE installation_id=$1 AND (cardinality($2::text[])=0 OR function_key=ANY($2))`, installationID, functionKeys).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.Pool.Query(ctx, jqlFunctionPrecomputationSelect+`WHERE installation_id=$1 AND (cardinality($2::text[])=0 OR function_key=ANY($2)) ORDER BY `+order+` OFFSET $3 LIMIT $4`, installationID, functionKeys, startAt, maxResults)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	values := []models.JQLFunctionPrecomputation{}
	for rows.Next() {
		value, scanErr := scanJQLFunctionPrecomputation(rows)
		if scanErr != nil {
			return nil, 0, scanErr
		}
		values = append(values, value)
	}
	return values, total, rows.Err()
}

func (s *Store) JQLFunctionPrecomputationsByID(ctx context.Context, installationID string, ids []string, orderBy string) ([]models.JQLFunctionPrecomputation, []string, error) {
	order, err := jqlFunctionOrder(orderBy)
	if err != nil {
		return nil, nil, err
	}
	rows, err := s.Pool.Query(ctx, jqlFunctionPrecomputationSelect+`WHERE installation_id=$1 AND id::text=ANY($2) ORDER BY `+order, installationID, ids)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	values := []models.JQLFunctionPrecomputation{}
	found := map[string]bool{}
	for rows.Next() {
		value, scanErr := scanJQLFunctionPrecomputation(rows)
		if scanErr != nil {
			return nil, nil, scanErr
		}
		found[value.ID] = true
		values = append(values, value)
	}
	if err = rows.Err(); err != nil {
		return nil, nil, err
	}
	missing := []string{}
	for _, id := range ids {
		if !found[id] {
			missing = append(missing, id)
		}
	}
	return values, missing, nil
}

func (s *Store) UpdateJQLFunctionPrecomputations(ctx context.Context, installationID string, updates []models.JQLFunctionPrecomputationUpdate, skipNotFound bool) ([]string, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	ids := make([]string, 0, len(updates))
	for _, update := range updates {
		ids = append(ids, update.ID)
	}
	rows, err := tx.Query(ctx, `SELECT id::text FROM jql_function_precomputations WHERE installation_id=$1 AND id::text=ANY($2) FOR UPDATE`, installationID, ids)
	if err != nil {
		return nil, err
	}
	found := map[string]bool{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		found[id] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	missing := []string{}
	for _, update := range updates {
		if !found[update.ID] {
			missing = append(missing, update.ID)
		}
	}
	if len(missing) > 0 && !skipNotFound {
		return missing, ErrJQLFunctionPrecomputationNotFound
	}
	for _, update := range updates {
		if !found[update.ID] {
			continue
		}
		if update.Value != nil {
			_, err = tx.Exec(ctx, `UPDATE jql_function_precomputations SET value=$3,error=NULL,updated_at=now() WHERE installation_id=$1 AND id::text=$2`, installationID, update.ID, *update.Value)
		} else {
			_, err = tx.Exec(ctx, `UPDATE jql_function_precomputations SET value=NULL,error=$3,updated_at=now() WHERE installation_id=$1 AND id::text=$2`, installationID, update.ID, *update.Error)
		}
		if err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return missing, nil
}
