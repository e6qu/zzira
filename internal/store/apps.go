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

func scanAppInstallation(row interface{ Scan(...any) error }) (*models.AppInstallation, error) {
	value := &models.AppInstallation{}
	err := row.Scan(&value.ID, &value.WorkspaceID, &value.PrincipalID, &value.Key, &value.Name, &value.BaseURL, &value.Version, &value.Format, &value.Status, &value.SecretCiphertext, &value.Descriptor, &value.InstalledBy, &value.InstalledAt, &value.UpdatedAt)
	if err != nil {
		return nil, err
	}
	value.InstalledAt = value.InstalledAt.UTC()
	value.UpdatedAt = value.UpdatedAt.UTC()
	return value, nil
}

const appInstallationSelect = `SELECT id,workspace_id,principal_id,app_key,name,base_url,version,descriptor_format,status,secret_ciphertext,descriptor,COALESCE(installed_by,''),installed_at,updated_at FROM app_installations `

func (s *Store) loadAppChildren(ctx context.Context, value *models.AppInstallation) error {
	value.Lifecycle = map[string]string{}
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
	moduleRows, err := s.Pool.Query(ctx, `SELECT id,installation_id,module_key,module_type,location,title,body,remote_url,position,dynamic FROM app_modules WHERE installation_id=$1 ORDER BY position,id::bigint`, value.ID)
	if err != nil {
		return err
	}
	for moduleRows.Next() {
		var module models.AppModule
		module.AppKey, module.AppName = value.Key, value.Name
		if err := moduleRows.Scan(&module.ID, &module.InstallationID, &module.Key, &module.Type, &module.Location, &module.Title, &module.Body, &module.RemoteURL, &module.Position, &module.Dynamic); err != nil {
			return err
		}
		value.Modules = append(value.Modules, module)
	}
	if err := moduleRows.Err(); err != nil {
		moduleRows.Close()
		return err
	}
	moduleRows.Close()
	callbackRows, err := s.Pool.Query(ctx, `SELECT event,path FROM app_lifecycle_callbacks WHERE installation_id=$1 ORDER BY event`, value.ID)
	if err != nil {
		return err
	}
	for callbackRows.Next() {
		var event, path string
		if err := callbackRows.Scan(&event, &path); err != nil {
			callbackRows.Close()
			return err
		}
		value.Lifecycle[event] = path
	}
	if err := callbackRows.Err(); err != nil {
		callbackRows.Close()
		return err
	}
	callbackRows.Close()
	webhookRows, err := s.Pool.Query(ctx, `SELECT id::text,module_key,path,events,jql,last_seq,dynamic,exclude_body FROM app_webhook_modules WHERE installation_id=$1 ORDER BY module_key`, value.ID)
	if err != nil {
		return err
	}
	for webhookRows.Next() {
		var webhook models.AppWebhook
		if err := webhookRows.Scan(&webhook.ID, &webhook.Key, &webhook.Path, &webhook.Events, &webhook.JQL, &webhook.LastSeq, &webhook.Dynamic, &webhook.ExcludeBody); err != nil {
			webhookRows.Close()
			return err
		}
		value.Webhooks = append(value.Webhooks, webhook)
	}
	if err := webhookRows.Err(); err != nil {
		webhookRows.Close()
		return err
	}
	webhookRows.Close()
	fieldRows, err := s.Pool.Query(ctx, `SELECT cf.id,cf.app_installation_id,i.app_key,cf.app_module_key,cf.name,cf.type,cf.description,cf.dynamic,cf.active FROM custom_fields cf JOIN app_installations i ON i.id=cf.app_installation_id WHERE cf.app_installation_id=$1 ORDER BY cf.created_at,cf.id`, value.ID)
	if err != nil {
		return err
	}
	for fieldRows.Next() {
		var field models.AppIssueField
		if err := fieldRows.Scan(&field.ID, &field.InstallationID, &field.AppKey, &field.Key, &field.Name, &field.Type, &field.Description, &field.Dynamic, &field.Active); err != nil {
			fieldRows.Close()
			return err
		}
		value.IssueFields = append(value.IssueFields, field)
	}
	if err := fieldRows.Err(); err != nil {
		fieldRows.Close()
		return err
	}
	fieldRows.Close()
	scheduleRows, err := s.Pool.Query(ctx, `SELECT id::text,module_key,path,interval_name,next_run_at FROM app_scheduled_triggers WHERE installation_id=$1 ORDER BY module_key`, value.ID)
	if err != nil {
		return err
	}
	defer scheduleRows.Close()
	for scheduleRows.Next() {
		var trigger models.AppScheduledTrigger
		if err := scheduleRows.Scan(&trigger.ID, &trigger.Key, &trigger.Path, &trigger.Interval, &trigger.NextRunAt); err != nil {
			return err
		}
		trigger.NextRunAt = trigger.NextRunAt.UTC()
		value.ScheduledTriggers = append(value.ScheduledTriggers, trigger)
	}
	return scheduleRows.Err()
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
		if _, err := tx.Exec(ctx, `INSERT INTO app_modules(installation_id,module_key,module_type,location,title,body,remote_url,position,dynamic) VALUES($1,$2,$3,$4,$5,$6,$7,$8,false) ON CONFLICT(installation_id,module_key) DO UPDATE SET module_type=EXCLUDED.module_type,location=EXCLUDED.location,title=EXCLUDED.title,body=EXCLUDED.body,remote_url=EXCLUDED.remote_url,position=EXCLUDED.position,dynamic=false`, installationID, module.Key, module.Type, module.Location, module.Title, module.Body, module.RemoteURL, position); err != nil {
			return err
		}
	}
	for _, field := range descriptor.IssueFields {
		if err := writeAppIssueField(ctx, tx, installationID, field, false); err != nil {
			return err
		}
	}
	for event, path := range descriptor.Lifecycle {
		if _, err := tx.Exec(ctx, `INSERT INTO app_lifecycle_callbacks(installation_id,event,path) VALUES($1,$2,$3)`, installationID, event, path); err != nil {
			return err
		}
	}
	for _, webhook := range descriptor.Webhooks {
		if _, err := tx.Exec(ctx, `INSERT INTO app_webhook_modules(installation_id,module_key,path,events,jql,last_seq,dynamic,exclude_body) SELECT $1,$2,$3,$4,$5,w.seq,false,$6 FROM app_installations i JOIN workspaces w ON w.id=i.workspace_id WHERE i.id=$1`, installationID, webhook.Key, webhook.Path, webhook.Events, webhook.JQL, webhook.ExcludeBody); err != nil {
			return err
		}
	}
	intervals := map[string]int{"fiveMinute": 300, "hour": 3600, "day": 86400, "week": 604800}
	for _, trigger := range descriptor.ScheduledTriggers {
		if _, err := tx.Exec(ctx, `INSERT INTO app_scheduled_triggers(installation_id,module_key,path,interval_name,interval_seconds) VALUES($1,$2,$3,$4,$5)`, installationID, trigger.Key, trigger.Path, trigger.Interval, intervals[trigger.Interval]); err != nil {
			return err
		}
	}
	return nil
}

func writeAppIssueField(ctx context.Context, tx pgx.Tx, installationID string, field models.AppIssueField, dynamic bool) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO custom_fields(id,name,type,description,workspace_id,app_installation_id,app_module_key,dynamic,active)
		SELECT 'customfield_' || nextval('jira_app_custom_field_id'),$2,$3,$4,i.workspace_id,i.id,$5,$6,true
		FROM app_installations i WHERE i.id=$1
		ON CONFLICT(app_installation_id,app_module_key) DO UPDATE SET
		  name=EXCLUDED.name,type=EXCLUDED.type,description=EXCLUDED.description,
		  workspace_id=EXCLUDED.workspace_id,dynamic=EXCLUDED.dynamic,active=true`,
		installationID, field.Name, field.Type, field.Description, field.Key, dynamic)
	return err
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
	var installationID, principalID, currentStatus string
	err = tx.QueryRow(ctx, `SELECT id,principal_id,status FROM app_installations WHERE workspace_id=$1 AND app_key=$2 FOR UPDATE`, workspaceID, descriptor.Key).Scan(&installationID, &principalID, &currentStatus)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		if err := tx.QueryRow(ctx, `SELECT nextval('jira_app_installation_id')::text`).Scan(&installationID); err != nil {
			return nil, err
		}
		principalID = "app_principal_" + installationID
		if _, err := tx.Exec(ctx, `INSERT INTO users(id,email,password_hash,display_name,active) VALUES($1,$2,'!app-principal!',$3,true)`, principalID, "app+"+installationID+"@apps.zzira.invalid", descriptor.Name); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO app_installations(id,workspace_id,principal_id,app_key,name,base_url,version,descriptor_format,status,secret_ciphertext,descriptor,installed_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,'active',$9,$10,$11)`, installationID, workspaceID, principalID, descriptor.Key, descriptor.Name, descriptor.BaseURL, descriptor.Version, descriptor.Format, secretCiphertext, json.RawMessage(rawDescriptor), actorID); err != nil {
			return nil, err
		}
	case err != nil:
		return nil, err
	case currentStatus != "uninstalled":
		return nil, fmt.Errorf("app %s is already installed", descriptor.Key)
	default:
		if _, err := tx.Exec(ctx, `UPDATE users SET display_name=$2,active=true WHERE id=$1`, principalID, descriptor.Name); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `UPDATE app_installations SET name=$3,base_url=$4,version=$5,descriptor_format=$6,status='active',secret_ciphertext=$7,descriptor=$8,installed_by=$9,installed_at=now(),updated_at=now() WHERE workspace_id=$1 AND app_key=$2`, workspaceID, descriptor.Key, descriptor.Name, descriptor.BaseURL, descriptor.Version, descriptor.Format, secretCiphertext, json.RawMessage(rawDescriptor), actorID); err != nil {
			return nil, err
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'member') ON CONFLICT DO NOTHING`, workspaceID, principalID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM app_scopes WHERE installation_id=$1`, installationID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM app_modules WHERE installation_id=$1`, installationID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE custom_fields SET active=false WHERE app_installation_id=$1`, installationID); err != nil {
		return nil, err
	}
	if currentStatus == "uninstalled" {
		// A reinstallation establishes a new credential generation. Do not send
		// an old generation's pending callbacks with its replacement secret.
		if _, err := tx.Exec(ctx, `DELETE FROM app_outbound_deliveries WHERE installation_id=$1`, installationID); err != nil {
			return nil, err
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM app_webhook_modules WHERE installation_id=$1`, installationID); err != nil {
		return nil, err
	}
	if err := deleteAppOutboundConfig(ctx, tx, installationID); err != nil {
		return nil, err
	}
	if err := writeAppChildren(ctx, tx, installationID, descriptor); err != nil {
		return nil, err
	}
	staticModuleKeys := make([]string, 0, len(descriptor.Modules))
	for _, module := range descriptor.Modules {
		staticModuleKeys = append(staticModuleKeys, module.Key)
	}
	for _, webhook := range descriptor.Webhooks {
		staticModuleKeys = append(staticModuleKeys, webhook.Key)
	}
	for _, field := range descriptor.IssueFields {
		staticModuleKeys = append(staticModuleKeys, field.Key)
	}
	if err := restoreDynamicAppModules(ctx, tx, installationID, staticModuleKeys); err != nil {
		return nil, err
	}
	if err := enqueueAppLifecycle(ctx, tx, installationID, descriptor.Key, descriptor.Version, "installed"); err != nil {
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
		event := map[string]string{"active": "enabled", "suspended": "disabled", "uninstalled": "uninstalled"}[status]
		if err := enqueueAppLifecycle(ctx, tx, installationID, appKey, "", event); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE app_installations SET status=$3,updated_at=now() WHERE workspace_id=$1 AND app_key=$2`, workspaceID, appKey, status); err != nil {
			return err
		}
		if status == "uninstalled" {
			if _, err := tx.Exec(ctx, `DELETE FROM dashboard_gadgets WHERE module_key IN (SELECT 'app:' || id FROM app_modules WHERE installation_id=$1)`, installationID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `DELETE FROM app_storage WHERE installation_id=$1`, installationID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `DELETE FROM app_modules WHERE installation_id=$1`, installationID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `DELETE FROM app_scopes WHERE installation_id=$1`, installationID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `DELETE FROM memberships WHERE user_id=(SELECT principal_id FROM app_installations WHERE id=$1) AND workspace_id=$2`, installationID, workspaceID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE users SET active=false WHERE id=(SELECT principal_id FROM app_installations WHERE id=$1)`, installationID); err != nil {
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
	if _, err := tx.Exec(ctx, `UPDATE app_installations SET name=$3,base_url=$4,version=$5,descriptor_format=$6,descriptor=$7,updated_at=now() WHERE workspace_id=$1 AND app_key=$2`, workspaceID, appKey, descriptor.Name, descriptor.BaseURL, descriptor.Version, descriptor.Format, json.RawMessage(rawDescriptor)); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET display_name=$2 WHERE id=(SELECT principal_id FROM app_installations WHERE id=$1)`, installationID, descriptor.Name); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM app_scopes WHERE installation_id=$1`, installationID); err != nil {
		return err
	}
	if err := deleteAppOutboundConfig(ctx, tx, installationID); err != nil {
		return err
	}
	moduleKeys := make([]string, 0, len(descriptor.Modules))
	for _, module := range descriptor.Modules {
		moduleKeys = append(moduleKeys, module.Key)
	}
	conflictKeys := append([]string{}, moduleKeys...)
	for _, webhook := range descriptor.Webhooks {
		conflictKeys = append(conflictKeys, webhook.Key)
	}
	for _, field := range descriptor.IssueFields {
		conflictKeys = append(conflictKeys, field.Key)
	}
	if len(conflictKeys) > 0 {
		if _, err := tx.Exec(ctx, `DELETE FROM app_modules WHERE installation_id=$1 AND dynamic AND module_key=ANY($2)`, installationID, conflictKeys); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM app_webhook_modules WHERE installation_id=$1 AND dynamic AND module_key=ANY($2)`, installationID, conflictKeys); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM app_dynamic_modules WHERE installation_id=$1 AND module_key=ANY($2)`, installationID, conflictKeys); err != nil {
			return err
		}
	}
	if err := writeAppChildren(ctx, tx, installationID, descriptor); err != nil {
		return err
	}
	staticFieldKeys := make([]string, 0, len(descriptor.IssueFields))
	for _, field := range descriptor.IssueFields {
		staticFieldKeys = append(staticFieldKeys, field.Key)
	}
	if _, err := tx.Exec(ctx, `UPDATE custom_fields SET active=false WHERE app_installation_id=$1 AND NOT dynamic AND NOT(app_module_key=ANY($2))`, installationID, staticFieldKeys); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM dashboard_gadgets WHERE module_key IN (SELECT 'app:' || id FROM app_modules WHERE installation_id=$1 AND NOT dynamic AND NOT(module_key=ANY($2)))`, installationID, moduleKeys); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM app_modules WHERE installation_id=$1 AND NOT dynamic AND NOT(module_key=ANY($2))`, installationID, moduleKeys); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO app_lifecycle_events(installation_id,event,payload) VALUES($1,'upgraded',$2)`, installationID, json.RawMessage(rawDescriptor)); err != nil {
		return err
	}
	if err := enqueueAppLifecycle(ctx, tx, installationID, appKey, descriptor.Version, "upgraded"); err != nil {
		return err
	}
	if err := enqueueAppLifecycle(ctx, tx, installationID, appKey, descriptor.Version, "enabled"); err != nil {
		return err
	}
	if err := auditApp(ctx, tx, workspaceID, "", "app.upgraded", appKey, map[string]string{"version": descriptor.Version}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) AppNavigationModules(ctx context.Context, workspaceID string) ([]models.AppModule, error) {
	rows, err := s.Pool.Query(ctx, `SELECT m.id,m.installation_id,i.app_key,i.name,m.module_key,m.module_type,m.location,m.title,m.body,m.remote_url,m.position,m.dynamic FROM app_modules m JOIN app_installations i ON i.id=m.installation_id WHERE i.workspace_id=$1 AND i.status='active' AND m.location IN ('jira.navigation','confluence.navigation') ORDER BY m.position,m.id::bigint`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []models.AppModule{}
	for rows.Next() {
		var value models.AppModule
		if err := rows.Scan(&value.ID, &value.InstallationID, &value.AppKey, &value.AppName, &value.Key, &value.Type, &value.Location, &value.Title, &value.Body, &value.RemoteURL, &value.Position, &value.Dynamic); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *Store) AppModulesByLocation(ctx context.Context, workspaceID, location string) ([]models.AppModule, error) {
	rows, err := s.Pool.Query(ctx, `SELECT m.id,m.installation_id,i.app_key,i.name,m.module_key,m.module_type,m.location,m.title,m.body,m.remote_url,m.position,m.dynamic FROM app_modules m JOIN app_installations i ON i.id=m.installation_id WHERE i.workspace_id=$1 AND i.status='active' AND m.location=$2 ORDER BY m.position,m.id::bigint`, workspaceID, location)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []models.AppModule{}
	for rows.Next() {
		var value models.AppModule
		if err := rows.Scan(&value.ID, &value.InstallationID, &value.AppKey, &value.AppName, &value.Key, &value.Type, &value.Location, &value.Title, &value.Body, &value.RemoteURL, &value.Position, &value.Dynamic); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *Store) AppIssueContentForIssue(ctx context.Context, workspaceID, issueID string) ([]models.AppIssueContent, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT m.id,m.installation_id,i.app_key,i.name,m.module_key,m.module_type,m.location,m.title,m.body,m.remote_url,m.position,m.dynamic,
		       EXISTS(SELECT 1 FROM app_issue_content_instances c WHERE c.issue_id=$2 AND c.installation_id=m.installation_id AND c.module_key=m.module_key)
		FROM app_modules m
		JOIN app_installations i ON i.id=m.installation_id
		JOIN issues issue ON issue.id=$2 AND issue.workspace_id=$1
		WHERE i.workspace_id=$1 AND i.status='active' AND m.location='jira.issue.content'
		ORDER BY m.position,m.id::bigint`, workspaceID, issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []models.AppIssueContent{}
	for rows.Next() {
		var value models.AppIssueContent
		if err := rows.Scan(&value.Module.ID, &value.Module.InstallationID, &value.Module.AppKey, &value.Module.AppName, &value.Module.Key, &value.Module.Type, &value.Module.Location, &value.Module.Title, &value.Module.Body, &value.Module.RemoteURL, &value.Module.Position, &value.Module.Dynamic, &value.Added); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *Store) SetAppIssueContent(ctx context.Context, workspaceID, issueID, actorID, moduleID string, added bool) error {
	if added {
		tag, err := s.Pool.Exec(ctx, `
			INSERT INTO app_issue_content_instances(workspace_id,issue_id,installation_id,module_key,created_by)
			SELECT $1,issue.id,m.installation_id,m.module_key,$4
			FROM issues issue,app_modules m
			JOIN app_installations i ON i.id=m.installation_id
			WHERE issue.id=$2 AND issue.workspace_id=$1 AND m.id=$3 AND m.location='jira.issue.content' AND i.workspace_id=$1 AND i.status='active'
			ON CONFLICT DO NOTHING`, workspaceID, issueID, moduleID, actorID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			var exists bool
			if err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM app_issue_content_instances c JOIN app_modules m ON m.installation_id=c.installation_id AND m.module_key=c.module_key JOIN app_installations i ON i.id=m.installation_id WHERE c.workspace_id=$1 AND c.issue_id=$2 AND m.id=$3 AND i.status='active')`, workspaceID, issueID, moduleID).Scan(&exists); err != nil || !exists {
				return pgx.ErrNoRows
			}
		}
		return nil
	}
	tag, err := s.Pool.Exec(ctx, `DELETE FROM app_issue_content_instances c USING app_modules m,app_installations i WHERE c.workspace_id=$1 AND c.issue_id=$2 AND m.id=$3 AND m.installation_id=c.installation_id AND m.module_key=c.module_key AND i.id=m.installation_id AND i.workspace_id=$1`, workspaceID, issueID, moduleID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func (s *Store) ActiveAppModule(ctx context.Context, workspaceID, moduleID string) (*models.AppModule, error) {
	var value models.AppModule
	err := s.Pool.QueryRow(ctx, `SELECT m.id,m.installation_id,i.app_key,i.name,i.base_url,i.secret_ciphertext,m.module_key,m.module_type,m.location,m.title,m.body,m.remote_url,m.position,m.dynamic FROM app_modules m JOIN app_installations i ON i.id=m.installation_id WHERE i.workspace_id=$1 AND i.status='active' AND m.id=$2`, workspaceID, moduleID).Scan(&value.ID, &value.InstallationID, &value.AppKey, &value.AppName, &value.BaseURL, &value.SecretCiphertext, &value.Key, &value.Type, &value.Location, &value.Title, &value.Body, &value.RemoteURL, &value.Position, &value.Dynamic)
	return &value, err
}

func (s *Store) ActiveDashboardAppModule(ctx context.Context, workspaceID, moduleKey string) (*models.AppModule, error) {
	if !strings.HasPrefix(moduleKey, "app:") {
		return nil, pgx.ErrNoRows
	}
	module, err := s.ActiveAppModule(ctx, workspaceID, strings.TrimPrefix(moduleKey, "app:"))
	if err != nil {
		return nil, err
	}
	if module.Location != "jira.dashboard" {
		return nil, pgx.ErrNoRows
	}
	return module, nil
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
