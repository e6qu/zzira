package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

func (s *Store) RegisterDynamicAppModules(ctx context.Context, installation *models.AppInstallation, modules []models.AppDynamicModule) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM app_dynamic_modules WHERE installation_id=$1`, installation.ID).Scan(&count); err != nil {
		return err
	}
	if count+len(modules) > 100 {
		return fmt.Errorf("an app installation may register at most 100 dynamic modules")
	}
	for position, module := range modules {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM app_modules WHERE installation_id=$1 AND module_key=$2)`, installation.ID, module.Key).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return fmt.Errorf("module key %q is already registered", module.Key)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO app_dynamic_modules(installation_id,module_type,module_key,descriptor) VALUES($1,$2,$3,$4)`, installation.ID, module.Type, module.Key, module.Descriptor); err != nil {
			return err
		}
		value := module.Module
		if _, err := tx.Exec(ctx, `INSERT INTO app_modules(installation_id,module_key,module_type,location,title,body,remote_url,position,dynamic) VALUES($1,$2,$3,$4,$5,$6,$7,$8,true)`, installation.ID, value.Key, value.Type, value.Location, value.Title, value.Body, value.RemoteURL, 10000+count+position); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) DynamicAppModules(ctx context.Context, installationID string) ([]models.AppDynamicModule, error) {
	rows, err := s.Pool.Query(ctx, `SELECT module_type,module_key,descriptor FROM app_dynamic_modules WHERE installation_id=$1 ORDER BY created_at,module_key`, installationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []models.AppDynamicModule{}
	for rows.Next() {
		var value models.AppDynamicModule
		if err := rows.Scan(&value.Type, &value.Key, &value.Descriptor); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *Store) DeleteDynamicAppModules(ctx context.Context, installationID string, keys []string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if len(keys) == 0 {
		if _, err := tx.Exec(ctx, `DELETE FROM app_modules WHERE installation_id=$1 AND dynamic`, installationID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM app_dynamic_modules WHERE installation_id=$1`, installationID); err != nil {
			return err
		}
	} else {
		if _, err := tx.Exec(ctx, `DELETE FROM app_modules WHERE installation_id=$1 AND dynamic AND module_key=ANY($2)`, installationID, keys); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM app_dynamic_modules WHERE installation_id=$1 AND module_key=ANY($2)`, installationID, keys); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func restoreDynamicAppModules(ctx context.Context, tx pgx.Tx, installationID string, staticKeys []string) error {
	if len(staticKeys) > 0 {
		if _, err := tx.Exec(ctx, `DELETE FROM app_dynamic_modules WHERE installation_id=$1 AND module_key=ANY($2)`, installationID, staticKeys); err != nil {
			return err
		}
	}
	rows, err := tx.Query(ctx, `SELECT module_type,module_key,descriptor FROM app_dynamic_modules WHERE installation_id=$1 ORDER BY created_at,module_key`, installationID)
	if err != nil {
		return err
	}
	type storedDynamicModule struct {
		moduleType, key string
		raw             json.RawMessage
	}
	stored := []storedDynamicModule{}
	for rows.Next() {
		var module storedDynamicModule
		if err := rows.Scan(&module.moduleType, &module.key, &module.raw); err != nil {
			rows.Close()
			return err
		}
		stored = append(stored, module)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for position, module := range stored {
		var input struct {
			URL      string `json:"url"`
			Location string `json:"location"`
			Name     struct {
				Value string `json:"value"`
			} `json:"name"`
		}
		if err := json.Unmarshal(module.raw, &input); err != nil {
			return err
		}
		if module.moduleType != "webPanels" {
			return fmt.Errorf("stored dynamic module type %q is unsupported", module.moduleType)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO app_modules(installation_id,module_key,module_type,location,title,body,remote_url,position,dynamic) VALUES($1,$2,'jira:issuePanel','jira.issue.view',$3,'',$4,$5,true)`, installationID, module.key, input.Name.Value, input.URL, 10000+position); err != nil {
			return err
		}
	}
	return nil
}
