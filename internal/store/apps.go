package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

func scanAppInstallation(row interface{ Scan(...any) error }) (*models.AppInstallation, error) {
	value := &models.AppInstallation{}
	err := row.Scan(&value.ID, &value.WorkspaceID, &value.Key, &value.Name, &value.BaseURL, &value.Version, &value.Status, &value.SecretCiphertext, &value.Descriptor, &value.InstalledBy, &value.InstalledAt, &value.UpdatedAt)
	if err != nil {
		return nil, err
	}
	value.InstalledAt = value.InstalledAt.UTC()
	value.UpdatedAt = value.UpdatedAt.UTC()
	return value, nil
}

const appInstallationSelect = `SELECT id,workspace_id,app_key,name,base_url,version,status,secret_ciphertext,descriptor,COALESCE(installed_by,''),installed_at,updated_at FROM app_installations `

func (s *Store) loadAppChildren(ctx context.Context, value *models.AppInstallation) error {
	rows, err := s.Pool.Query(ctx, `SELECT scope FROM app_scopes WHERE installation_id=$1 ORDER BY scope`, value.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var scope string
		if err := rows.Scan(&scope); err != nil {
			rows.Close()
			return err
		}
		value.Scopes = append(value.Scopes, scope)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	moduleRows, err := s.Pool.Query(ctx, `SELECT id,installation_id,module_key,module_type,location,title,body,position FROM app_modules WHERE installation_id=$1 ORDER BY position,id::bigint`, value.ID)
	if err != nil {
		return err
	}
	defer moduleRows.Close()
	for moduleRows.Next() {
		var module models.AppModule
		module.AppKey, module.AppName = value.Key, value.Name
		if err := moduleRows.Scan(&module.ID, &module.InstallationID, &module.Key, &module.Type, &module.Location, &module.Title, &module.Body, &module.Position); err != nil {
			return err
		}
		value.Modules = append(value.Modules, module)
	}
	return moduleRows.Err()
}

func (s *Store) AppInstallations(ctx context.Context, workspaceID string) ([]*models.AppInstallation, error) {
	rows, err := s.Pool.Query(ctx, appInstallationSelect+`WHERE workspace_id=$1 ORDER BY lower(name),app_key`, workspaceID)
	if err != nil {
		return nil, err
	}
	values := []*models.AppInstallation{}
	for rows.Next() {
		value, err := scanAppInstallation(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for _, value := range values {
		if err := s.loadAppChildren(ctx, value); err != nil {
			return nil, err
		}
	}
	return values, nil
}

func (s *Store) AppInstallation(ctx context.Context, workspaceID, appKey string) (*models.AppInstallation, error) {
	value, err := scanAppInstallation(s.Pool.QueryRow(ctx, appInstallationSelect+`WHERE workspace_id=$1 AND app_key=$2`, workspaceID, appKey))
	if err != nil {
		return nil, err
	}
	if err := s.loadAppChildren(ctx, value); err != nil {
		return nil, err
	}
	return value, nil
}

func writeAppChildren(ctx context.Context, tx pgx.Tx, installationID string, descriptor models.AppDescriptor) error {
	for _, scope := range descriptor.Scopes {
		if _, err := tx.Exec(ctx, `INSERT INTO app_scopes(installation_id,scope) VALUES($1,$2)`, installationID, scope); err != nil {
			return err
		}
	}
	for position, module := range descriptor.Modules {
		if _, err := tx.Exec(ctx, `INSERT INTO app_modules(installation_id,module_key,module_type,location,title,body,position) VALUES($1,$2,$3,$4,$5,$6,$7)`, installationID, module.Key, module.Type, module.Location, module.Title, module.Body, position); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) InstallApp(ctx context.Context, workspaceID, actorID string, descriptor models.AppDescriptor, rawDescriptor, secretCiphertext []byte) (*models.AppInstallation, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return nil, err
	}
	var installationID string
	err = tx.QueryRow(ctx, `INSERT INTO app_installations(workspace_id,app_key,name,base_url,version,status,secret_ciphertext,descriptor,installed_by) VALUES($1,$2,$3,$4,$5,'active',$6,$7,$8)
		ON CONFLICT(workspace_id,app_key) DO UPDATE SET name=EXCLUDED.name,base_url=EXCLUDED.base_url,version=EXCLUDED.version,status='active',secret_ciphertext=EXCLUDED.secret_ciphertext,descriptor=EXCLUDED.descriptor,installed_by=EXCLUDED.installed_by,installed_at=now(),updated_at=now()
		WHERE app_installations.status='uninstalled' RETURNING id`, workspaceID, descriptor.Key, descriptor.Name, descriptor.BaseURL, descriptor.Version, secretCiphertext, json.RawMessage(rawDescriptor), actorID).Scan(&installationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("app %s is already installed", descriptor.Key)
	}
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM app_scopes WHERE installation_id=$1`, installationID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM app_modules WHERE installation_id=$1`, installationID); err != nil {
		return nil, err
	}
	if err := writeAppChildren(ctx, tx, installationID, descriptor); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO app_lifecycle_events(installation_id,event,payload) VALUES($1,'installed',$2)`, installationID, json.RawMessage(rawDescriptor)); err != nil {
		return nil, err
	}
	if err := auditApp(ctx, tx, workspaceID, actorID, "app.installed", descriptor.Key, map[string]any{"version": descriptor.Version, "scopes": descriptor.Scopes}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.AppInstallation(ctx, workspaceID, descriptor.Key)
}

func auditApp(ctx context.Context, tx pgx.Tx, workspaceID, actorID, action, appKey string, detail any) error {
	raw, _ := json.Marshal(detail)
	_, err := tx.Exec(ctx, `INSERT INTO organization_audit_events(organization_id,actor_id,action,target_type,target_id,detail) SELECT organization_id,NULLIF($2,''),$3,'app',$4,$5::jsonb FROM sites WHERE workspace_id=$1`, workspaceID, actorID, action, appKey, raw)
	return err
}

func (s *Store) UpdateAppState(ctx context.Context, workspaceID, actorID, appKey, status string, requireAdmin bool) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if requireAdmin {
		if err := projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
			return err
		}
	}
	var installationID, current string
	if err := tx.QueryRow(ctx, `SELECT id,status FROM app_installations WHERE workspace_id=$1 AND app_key=$2 FOR UPDATE`, workspaceID, appKey).Scan(&installationID, &current); err != nil {
		return err
	}
	if current == "uninstalled" && status != "uninstalled" {
		return fmt.Errorf("uninstalled apps must be installed again")
	}
	if current != status {
		if _, err := tx.Exec(ctx, `UPDATE app_installations SET status=$3,updated_at=now() WHERE workspace_id=$1 AND app_key=$2`, workspaceID, appKey, status); err != nil {
			return err
		}
		if status == "uninstalled" {
			if _, err := tx.Exec(ctx, `DELETE FROM app_storage WHERE installation_id=$1`, installationID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `DELETE FROM app_modules WHERE installation_id=$1`, installationID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `DELETE FROM app_scopes WHERE installation_id=$1`, installationID); err != nil {
				return err
			}
		}
	}
	payload, _ := json.Marshal(map[string]string{"from": current, "to": status})
	if _, err := tx.Exec(ctx, `INSERT INTO app_lifecycle_events(installation_id,event,payload) VALUES($1,$2,$3)`, installationID, status, payload); err != nil {
		return err
	}
	if err := auditApp(ctx, tx, workspaceID, actorID, "app."+status, appKey, map[string]string{"from": current, "to": status}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) UpgradeApp(ctx context.Context, workspaceID, appKey string, descriptor models.AppDescriptor, rawDescriptor []byte) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var installationID, status string
	if err := tx.QueryRow(ctx, `SELECT id,status FROM app_installations WHERE workspace_id=$1 AND app_key=$2 FOR UPDATE`, workspaceID, appKey).Scan(&installationID, &status); err != nil {
		return err
	}
	if status == "uninstalled" {
		return fmt.Errorf("uninstalled apps cannot be upgraded")
	}
	if _, err := tx.Exec(ctx, `UPDATE app_installations SET name=$3,base_url=$4,version=$5,descriptor=$6,updated_at=now() WHERE workspace_id=$1 AND app_key=$2`, workspaceID, appKey, descriptor.Name, descriptor.BaseURL, descriptor.Version, json.RawMessage(rawDescriptor)); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM app_scopes WHERE installation_id=$1`, installationID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM app_modules WHERE installation_id=$1`, installationID); err != nil {
		return err
	}
	if err := writeAppChildren(ctx, tx, installationID, descriptor); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO app_lifecycle_events(installation_id,event,payload) VALUES($1,'upgraded',$2)`, installationID, json.RawMessage(rawDescriptor)); err != nil {
		return err
	}
	if err := auditApp(ctx, tx, workspaceID, "", "app.upgraded", appKey, map[string]string{"version": descriptor.Version}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) AppNavigationModules(ctx context.Context, workspaceID string) ([]models.AppModule, error) {
	rows, err := s.Pool.Query(ctx, `SELECT m.id,m.installation_id,i.app_key,i.name,m.module_key,m.module_type,m.location,m.title,m.body,m.position FROM app_modules m JOIN app_installations i ON i.id=m.installation_id WHERE i.workspace_id=$1 AND i.status='active' AND m.location IN ('jira.navigation','confluence.navigation') ORDER BY m.position,m.id::bigint`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []models.AppModule{}
	for rows.Next() {
		var value models.AppModule
		if err := rows.Scan(&value.ID, &value.InstallationID, &value.AppKey, &value.AppName, &value.Key, &value.Type, &value.Location, &value.Title, &value.Body, &value.Position); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *Store) ActiveAppModule(ctx context.Context, workspaceID, moduleID string) (*models.AppModule, error) {
	var value models.AppModule
	err := s.Pool.QueryRow(ctx, `SELECT m.id,m.installation_id,i.app_key,i.name,m.module_key,m.module_type,m.location,m.title,m.body,m.position FROM app_modules m JOIN app_installations i ON i.id=m.installation_id WHERE i.workspace_id=$1 AND i.status='active' AND m.id=$2`, workspaceID, moduleID).Scan(&value.ID, &value.InstallationID, &value.AppKey, &value.AppName, &value.Key, &value.Type, &value.Location, &value.Title, &value.Body, &value.Position)
	return &value, err
}

func (s *Store) AppStorage(ctx context.Context, installationID, key string) (*models.AppStorageValue, error) {
	value := &models.AppStorageValue{Key: key}
	err := s.Pool.QueryRow(ctx, `SELECT value,version,updated_at FROM app_storage WHERE installation_id=$1 AND key=$2`, installationID, key).Scan(&value.Value, &value.Version, &value.UpdatedAt)
	if err != nil {
		return nil, err
	}
	value.UpdatedAt = value.UpdatedAt.UTC()
	return value, nil
}

func (s *Store) PutAppStorage(ctx context.Context, installationID, key string, value json.RawMessage) (*models.AppStorageValue, error) {
	stored := &models.AppStorageValue{Key: key, Value: value}
	err := s.Pool.QueryRow(ctx, `INSERT INTO app_storage(installation_id,key,value) VALUES($1,$2,$3) ON CONFLICT(installation_id,key) DO UPDATE SET value=EXCLUDED.value,version=app_storage.version+1,updated_at=now() RETURNING version,updated_at`, installationID, key, value).Scan(&stored.Version, &stored.UpdatedAt)
	if err != nil {
		return nil, err
	}
	stored.UpdatedAt = stored.UpdatedAt.UTC()
	return stored, nil
}

func (s *Store) DeleteAppStorage(ctx context.Context, installationID, key string) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM app_storage WHERE installation_id=$1 AND key=$2`, installationID, key)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func AppHasScope(installation *models.AppInstallation, scope string) bool {
	for _, candidate := range installation.Scopes {
		if candidate == scope {
			return true
		}
	}
	return false
}

func (s *Store) ClaimAppSignedRequest(ctx context.Context, installationID, requestID string) (bool, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM app_signed_requests WHERE created_at<now()-interval '24 hours'`); err != nil {
		return false, err
	}
	tag, err := tx.Exec(ctx, `INSERT INTO app_signed_requests(installation_id,request_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, installationID, requestID)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}
