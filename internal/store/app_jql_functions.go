package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

var ErrAmbiguousAppJQLFunction = errors.New("multiple installed apps declare the same JQL function")

const appJQLFunctionSelect = `SELECT f.id::text,f.installation_id,i.app_key,i.base_url,i.descriptor_format,i.secret_ciphertext,f.module_key,f.function_name,f.path,f.arguments,f.types,f.operators FROM app_jql_function_modules f JOIN app_installations i ON i.id=f.installation_id `

func scanAppJQLFunction(row interface{ Scan(...any) error }) (models.AppJQLFunction, error) {
	var value models.AppJQLFunction
	var arguments []byte
	err := row.Scan(&value.ID, &value.InstallationID, &value.AppKey, &value.BaseURL, &value.Format, &value.SecretCiphertext, &value.Key, &value.Name, &value.Path, &arguments, &value.Types, &value.Operators)
	if err != nil {
		return value, err
	}
	if err := json.Unmarshal(arguments, &value.Arguments); err != nil {
		return value, fmt.Errorf("decode JQL function arguments: %w", err)
	}
	return value, nil
}

func (s *Store) ActiveAppJQLFunctions(ctx context.Context, workspaceID string) ([]models.AppJQLFunction, error) {
	rows, err := s.Pool.Query(ctx, appJQLFunctionSelect+`WHERE i.workspace_id=$1 AND i.status='active' ORDER BY lower(f.function_name),i.app_key,f.module_key`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []models.AppJQLFunction{}
	for rows.Next() {
		value, err := scanAppJQLFunction(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *Store) ActiveAppJQLFunction(ctx context.Context, workspaceID, name string) (*models.AppJQLFunction, error) {
	rows, err := s.Pool.Query(ctx, appJQLFunctionSelect+`WHERE i.workspace_id=$1 AND i.status='active' AND lower(f.function_name)=lower($2) ORDER BY i.app_key,f.module_key LIMIT 2`, workspaceID, strings.TrimSpace(name))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []models.AppJQLFunction{}
	for rows.Next() {
		value, err := scanAppJQLFunction(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(values) == 0 {
		return nil, pgx.ErrNoRows
	}
	if len(values) > 1 {
		return nil, ErrAmbiguousAppJQLFunction
	}
	return &values[0], nil
}
