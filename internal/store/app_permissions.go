package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// appPermissionSelect reads the permissions active apps declare, keyed by the
// app key and module key joined by two underscores.
const appPermissionSelect = `SELECT i.app_key || '__' || m.module_key, m.name, m.description, m.permission_type
	FROM app_permission_modules m JOIN app_installations i ON i.id=m.installation_id
	WHERE i.workspace_id=$1 AND i.status='active'`

// PermissionCatalog lists Jira's built-in permissions followed by those the
// site's active apps declare.
func (s *Store) PermissionCatalog(ctx context.Context, workspaceID string) ([]PermissionDefinition, error) {
	definitions := PermissionDefinitions()
	rows, err := s.Pool.Query(ctx, appPermissionSelect+` ORDER BY i.app_key, m.module_key`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var definition PermissionDefinition
		if err = rows.Scan(&definition.Key, &definition.Name, &definition.Description, &definition.Type); err != nil {
			return nil, err
		}
		definitions = append(definitions, definition)
	}
	return definitions, rows.Err()
}

// appPermission finds one permission an active app of the site declares.
func appPermission(ctx context.Context, q rowQuerier, workspaceID, key string) (PermissionDefinition, []string, bool, error) {
	var definition PermissionDefinition
	var grants []string
	err := q.QueryRow(ctx, `SELECT i.app_key || '__' || m.module_key, m.name, m.description, m.permission_type, m.default_grants
		FROM app_permission_modules m JOIN app_installations i ON i.id=m.installation_id
		WHERE i.workspace_id=$1 AND i.status='active' AND i.app_key || '__' || m.module_key = $2`, workspaceID, key).
		Scan(&definition.Key, &definition.Name, &definition.Description, &definition.Type, &grants)
	if errors.Is(err, pgx.ErrNoRows) {
		return PermissionDefinition{}, nil, false, nil
	}
	return definition, grants, err == nil, err
}
