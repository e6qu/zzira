package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

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
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM app_modules WHERE installation_id=$1 AND module_key=$2 UNION ALL SELECT 1 FROM app_webhook_modules WHERE installation_id=$1 AND module_key=$2 UNION ALL SELECT 1 FROM custom_fields WHERE app_installation_id=$1 AND app_module_key=$2 AND active UNION ALL SELECT 1 FROM app_dynamic_modules WHERE installation_id=$1 AND module_key=$2)`, installation.ID, module.Key).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return fmt.Errorf("module key %q is already registered", module.Key)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO app_dynamic_modules(installation_id,module_type,module_key,descriptor) VALUES($1,$2,$3,$4)`, installation.ID, module.Type, module.Key, module.Descriptor); err != nil {
			return err
		}
		switch module.Type {
		case "webPanels":
			value := module.Module
			if _, err := tx.Exec(ctx, `INSERT INTO app_modules(installation_id,module_key,module_type,location,title,body,remote_url,position,dynamic) VALUES($1,$2,$3,$4,$5,$6,$7,$8,true)`, installation.ID, value.Key, value.Type, value.Location, value.Title, value.Body, value.RemoteURL, 10000+count+position); err != nil {
				return err
			}
		case "webhooks":
			value := module.Webhook
			if _, err := tx.Exec(ctx, `INSERT INTO app_webhook_modules(installation_id,module_key,path,events,jql,last_seq,dynamic,exclude_body) SELECT $1,$2,$3,$4,$5,w.seq,true,$6 FROM workspaces w WHERE w.id=$7`, installation.ID, value.Key, value.Path, value.Events, value.JQL, value.ExcludeBody, installation.WorkspaceID); err != nil {
				return err
			}
		case "jiraIssueFields":
			if err := writeAppIssueField(ctx, tx, installation.ID, module.IssueField, true); err != nil {
				return err
			}
		default:
			return fmt.Errorf("dynamic module type %q is unsupported", module.Type)
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
		if _, err := tx.Exec(ctx, `DELETE FROM app_webhook_modules WHERE installation_id=$1 AND dynamic`, installationID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE custom_fields SET active=false WHERE app_installation_id=$1 AND dynamic`, installationID); err != nil {
			return err
		}
	} else {
		if _, err := tx.Exec(ctx, `DELETE FROM app_modules WHERE installation_id=$1 AND dynamic AND module_key=ANY($2)`, installationID, keys); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM app_dynamic_modules WHERE installation_id=$1 AND module_key=ANY($2)`, installationID, keys); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM app_webhook_modules WHERE installation_id=$1 AND dynamic AND module_key=ANY($2)`, installationID, keys); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE custom_fields SET active=false WHERE app_installation_id=$1 AND dynamic AND app_module_key=ANY($2)`, installationID, keys); err != nil {
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
		switch module.moduleType {
		case "webPanels":
			if _, err := tx.Exec(ctx, `INSERT INTO app_modules(installation_id,module_key,module_type,location,title,body,remote_url,position,dynamic) VALUES($1,$2,'jira:issuePanel','jira.issue.view',$3,'',$4,$5,true)`, installationID, module.key, input.Name.Value, input.URL, 10000+position); err != nil {
				return err
			}
		case "webhooks":
			var webhook struct {
				Event       string `json:"event"`
				URL         string `json:"url"`
				Filter      string `json:"filter"`
				ExcludeBody bool   `json:"excludeBody"`
			}
			if err := json.Unmarshal(module.raw, &webhook); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO app_webhook_modules(installation_id,module_key,path,events,jql,last_seq,dynamic,exclude_body) SELECT $1,$2,$3,$4,$5,w.seq,true,$6 FROM app_installations i JOIN workspaces w ON w.id=i.workspace_id WHERE i.id=$1`, installationID, module.key, webhook.URL, []string{webhook.Event}, webhook.Filter, webhook.ExcludeBody); err != nil {
				return err
			}
		case "jiraIssueFields":
			var field struct {
				Key  string `json:"key"`
				Name struct {
					Value string `json:"value"`
				} `json:"name"`
				Description struct {
					Value string `json:"value"`
				} `json:"description"`
				Type string `json:"type"`
			}
			if err := json.Unmarshal(module.raw, &field); err != nil {
				return err
			}
			fieldType := map[string]string{"string": models.CustomFieldText, "text": models.CustomFieldText, "rich_text": models.CustomFieldText, "number": models.CustomFieldNumber, "date": models.CustomFieldDatetime, "datetime": models.CustomFieldDatetime}[strings.ToLower(strings.TrimSpace(field.Type))]
			if err := writeAppIssueField(ctx, tx, installationID, models.AppIssueField{Key: module.key, Name: field.Name.Value, Description: field.Description.Value, Type: fieldType}, true); err != nil {
				return err
			}
		default:
			return fmt.Errorf("stored dynamic module type %q is unsupported", module.moduleType)
		}
	}
	return nil
}
