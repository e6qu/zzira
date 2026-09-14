package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// ---- app custom fields ----

// AppField is a custom field an installed app provides.
type AppField struct {
	ID, Type, InstallationID, AppKey, ModuleKey string
}

// AppCustomField finds an app-provided custom field by its id or its
// appKey__moduleKey key.
func (s *Store) AppCustomField(ctx context.Context, workspaceID, idOrKey string) (AppField, error) {
	var field AppField
	err := s.Pool.QueryRow(ctx, `SELECT cf.id,cf.type,cf.app_installation_id,ai.app_key,cf.app_module_key
		FROM custom_fields cf JOIN app_installations ai ON ai.id=cf.app_installation_id
		WHERE ai.workspace_id=$1 AND cf.active AND (cf.id=$2 OR ai.app_key||'__'||cf.app_module_key=$2)`, workspaceID, strings.TrimSpace(idOrKey)).
		Scan(&field.ID, &field.Type, &field.InstallationID, &field.AppKey, &field.ModuleKey)
	return field, err
}

// AppFieldConfiguration is a field's configuration in one of its contexts.
type AppFieldConfiguration struct {
	ID            int64
	FieldID       string
	ContextID     int64
	Configuration json.RawMessage
	Schema        json.RawMessage
}

var ErrAppFieldConfigurationValidation = errors.New("invalid app field configuration")

// AppFieldConfigurations returns one configuration for each context of a
// field, creating the empty configuration of a new context on first read so
// its id is stable.
func (s *Store) AppFieldConfigurations(ctx context.Context, workspaceID, fieldID string) ([]AppFieldConfiguration, error) {
	if _, err := s.Pool.Exec(ctx, `INSERT INTO app_field_configurations(field_id,context_id)
		SELECT c.field_id,c.id FROM custom_field_contexts c WHERE c.workspace_id=$1 AND c.field_id=$2
		ON CONFLICT (field_id,context_id) DO NOTHING`, workspaceID, fieldID); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT a.id,a.field_id,a.context_id,a.configuration,a.schema FROM app_field_configurations a
		JOIN custom_field_contexts c ON c.id=a.context_id WHERE c.workspace_id=$1 AND a.field_id=$2 ORDER BY a.context_id`, workspaceID, fieldID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	configurations := []AppFieldConfiguration{}
	for rows.Next() {
		var configuration AppFieldConfiguration
		if err := rows.Scan(&configuration.ID, &configuration.FieldID, &configuration.ContextID, &configuration.Configuration, &configuration.Schema); err != nil {
			return nil, err
		}
		configurations = append(configurations, configuration)
	}
	return configurations, rows.Err()
}

// SetAppFieldConfigurations replaces the configuration and schema of the
// given configurations of one field.
func (s *Store) SetAppFieldConfigurations(ctx context.Context, workspaceID, fieldID string, configurations []AppFieldConfiguration) error {
	if _, err := s.AppFieldConfigurations(ctx, workspaceID, fieldID); err != nil {
		return err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, configuration := range configurations {
		for _, raw := range []json.RawMessage{configuration.Configuration, configuration.Schema} {
			if len(raw) > 100000 {
				return fmt.Errorf("%w: a configuration and its schema must each be at most 100000 characters", ErrAppFieldConfigurationValidation)
			}
		}
		tag, err := tx.Exec(ctx, `UPDATE app_field_configurations SET configuration=$4,schema=$5,updated_at=now()
			WHERE field_id=$1 AND id=$2 AND context_id=$3`, fieldID, configuration.ID, configuration.ContextID, appFieldJSON(configuration.Configuration), appFieldJSON(configuration.Schema))
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("%w: the configuration %d does not belong to the field context %d", ErrAppFieldConfigurationValidation, configuration.ID, configuration.ContextID)
		}
	}
	return tx.Commit(ctx)
}

func appFieldJSON(raw json.RawMessage) any {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return []byte(raw)
}

// ---- Connect app migration ----

// CreateAppMigrationTransfer opens a data transfer for an installed app.
func (s *Store) CreateAppMigrationTransfer(ctx context.Context, workspaceID, actorID, installationID string) (string, error) {
	var id string
	err := s.Pool.QueryRow(ctx, `INSERT INTO app_migration_transfers(workspace_id,installation_id,created_by)
		SELECT workspace_id,id,$3 FROM app_installations WHERE workspace_id=$1 AND id=$2 RETURNING id::text`, workspaceID, installationID, actorID).Scan(&id)
	return id, err
}

type AppMigrationTransfer struct {
	ID, CreatedBy string
	CreatedAt     string
}

func (s *Store) AppMigrationTransfers(ctx context.Context, installationID string) ([]AppMigrationTransfer, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id::text,created_by,to_char(created_at AT TIME ZONE 'UTC','YYYY-MM-DD HH24:MI') FROM app_migration_transfers WHERE installation_id=$1 ORDER BY created_at DESC`, installationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	transfers := []AppMigrationTransfer{}
	for rows.Next() {
		var transfer AppMigrationTransfer
		if err := rows.Scan(&transfer.ID, &transfer.CreatedBy, &transfer.CreatedAt); err != nil {
			return nil, err
		}
		transfers = append(transfers, transfer)
	}
	return transfers, rows.Err()
}

// ValidAppMigrationTransfer reports whether a transfer id belongs to the app.
func (s *Store) ValidAppMigrationTransfer(ctx context.Context, installationID, transferID string) (bool, error) {
	var found bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM app_migration_transfers WHERE installation_id=$1 AND id::text=$2)`, installationID, strings.ToLower(strings.TrimSpace(transferID))).Scan(&found)
	return found, err
}

// MigrationEntityProperty is one property update from an app migration.
type MigrationEntityProperty struct {
	EntityID int64
	Key      string
	Value    json.RawMessage
}

var ErrMigrationValidation = errors.New("invalid migration request")

// SetMigrationEntityProperties stores entity properties by Jira's numeric
// entity ids, all or none.
func (s *Store) SetMigrationEntityProperties(ctx context.Context, workspaceID, actorID, entityType string, properties []MigrationEntityProperty) error {
	type target struct{ lookup, upsert string }
	targets := map[string]target{
		"IssueProperty":         {`SELECT id FROM issues WHERE workspace_id=$1 AND jira_id=$2`, `INSERT INTO issue_properties(issue_id,key,value) VALUES($1,$2,$3) ON CONFLICT(issue_id,key) DO UPDATE SET value=EXCLUDED.value,updated_at=now()`},
		"CommentProperty":       {`SELECT id FROM comments WHERE workspace_id=$1 AND jira_id=$2`, `INSERT INTO jira_comment_properties(comment_id,key,value) VALUES($1,$2,$3) ON CONFLICT(comment_id,key) DO UPDATE SET value=EXCLUDED.value,updated_at=now()`},
		"WorklogProperty":       {`SELECT id FROM worklogs WHERE workspace_id=$1 AND jira_id=$2`, `INSERT INTO worklog_properties(worklog_id,key,value) VALUES($1,$2,$3) ON CONFLICT(worklog_id,key) DO UPDATE SET value=EXCLUDED.value,updated_at=now()`},
		"IssueTypeProperty":     {`SELECT id FROM issue_types WHERE (workspace_id IS NULL OR workspace_id=$1) AND jira_id=$2`, `INSERT INTO issue_type_properties(workspace_id,issue_type_id,key,value) VALUES($4,$1,$2,$3) ON CONFLICT(workspace_id,issue_type_id,key) DO UPDATE SET value=EXCLUDED.value,updated_at=now()`},
		"ProjectProperty":       {`SELECT id FROM projects WHERE workspace_id=$1 AND id=$2::bigint::text`, `INSERT INTO project_properties(project_id,property_key,value) VALUES($1,$2,$3) ON CONFLICT(project_id,property_key) DO UPDATE SET value=EXCLUDED.value,updated_at=now()`},
		"BoardProperty":         {`SELECT b.id FROM boards b JOIN projects p ON p.id=b.project_id WHERE p.workspace_id=$1 AND b.jira_id=$2`, `INSERT INTO board_properties(board_id,key,value) VALUES($1,$2,$3) ON CONFLICT(board_id,key) DO UPDATE SET value=EXCLUDED.value,updated_at=now()`},
		"SprintProperty":        {`SELECT s.id FROM sprints s JOIN boards b ON b.id=s.board_id JOIN projects p ON p.id=b.project_id WHERE p.workspace_id=$1 AND s.jira_id=$2`, `INSERT INTO sprint_properties(sprint_id,key,value) VALUES($1,$2,$3) ON CONFLICT(sprint_id,key) DO UPDATE SET value=EXCLUDED.value,updated_at=now()`},
		"DashboardItemProperty": {`SELECT g.id::text FROM dashboard_gadgets g JOIN dashboards d ON d.id=g.dashboard_id WHERE d.workspace_id=$1 AND g.id=$2`, `INSERT INTO dashboard_gadget_properties(gadget_id,key,value) VALUES($1::bigint,$2,$3) ON CONFLICT(gadget_id,key) DO UPDATE SET value=EXCLUDED.value`},
	}
	destination, ok := targets[entityType]
	if !ok {
		return fmt.Errorf("%w: the entity type %s is not supported", ErrMigrationValidation, entityType)
	}
	if len(properties) == 0 || len(properties) > 50 {
		return fmt.Errorf("%w: give between 1 and 50 entity properties", ErrMigrationValidation)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, property := range properties {
		if property.Key == "" || len([]rune(property.Key)) > 255 || len(property.Value) > 32768 || !json.Valid(property.Value) {
			return fmt.Errorf("%w: the property %q on entity %d needs a key of 1 to 255 characters and a JSON value of at most 32768 characters", ErrMigrationValidation, property.Key, property.EntityID)
		}
		var id string
		if err := tx.QueryRow(ctx, destination.lookup, workspaceID, property.EntityID).Scan(&id); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("%w: the entity %d does not exist", ErrMigrationValidation, property.EntityID)
			}
			return err
		}
		args := []any{id, property.Key, []byte(property.Value)}
		if entityType == "IssueTypeProperty" {
			args = append(args, workspaceID)
		}
		tag, err := tx.Exec(ctx, destination.upsert, args...)
		if err != nil {
			return err
		}
		// Issue property changes are recorded like any other property write.
		if entityType == "IssueProperty" && tag.RowsAffected() == 1 {
			if err := issuePropertyAction(ctx, tx, actorID, id, property.Key, property.Value, "upsert"); err != nil {
				return err
			}
		}
	}
	return tx.Commit(ctx)
}

// ---- Connect to Forge issue field migration ----

const apiTaskConnectFieldMigration = "connect-forge-field-migration"

var ErrConnectFieldMigrationRunning = errors.New("a migration task is already in progress for the field")

type connectFieldMigrationPayload struct {
	FieldID   string `json:"fieldId"`
	ModuleKey string `json:"moduleKey"`
}

// ConnectFieldMigrationTask returns the latest migration task of an app's
// issue field.
func (s *Store) ConnectFieldMigrationTask(ctx context.Context, workspaceID, installationID, moduleKey string) (APITask, error) {
	var taskID string
	if err := s.Pool.QueryRow(ctx, `SELECT task_id FROM connect_field_migrations WHERE installation_id=$1 AND module_key=$2 ORDER BY created_at DESC LIMIT 1`, installationID, moduleKey).Scan(&taskID); err != nil {
		return APITask{}, err
	}
	return s.APITaskByID(ctx, workspaceID, taskID)
}

// SubmitConnectFieldMigration queues the migration of an app issue field's
// values. A completed migration is only run again when asked. It reports
// whether a task was queued.
func (s *Store) SubmitConnectFieldMigration(ctx context.Context, workspaceID, actorID string, field AppField, retrigger bool) (bool, error) {
	previous, err := s.ConnectFieldMigrationTask(ctx, workspaceID, field.InstallationID, field.ModuleKey)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	if err == nil {
		switch previous.Status {
		case "ENQUEUED", "RUNNING", "CANCEL_REQUESTED":
			return false, ErrConnectFieldMigrationRunning
		case "COMPLETE":
			if !retrigger {
				return false, nil
			}
		}
	}
	task, err := queuedAPITask(workspaceID, actorID, "Migrating the values of "+field.AppKey+" issue field "+field.ModuleKey+" to Forge", apiTaskConnectFieldMigration, connectFieldMigrationPayload{FieldID: field.ID, ModuleKey: field.ModuleKey})
	if err != nil {
		return false, err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := insertAPITask(ctx, tx, &task); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO connect_field_migrations(installation_id,module_key,task_id) VALUES($1,$2,$3)`, field.InstallationID, field.ModuleKey, task.ID); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

// executeConnectFieldMigration moves a field's stored values. Connect and
// Forge fields share one custom field record here, so the values stay where
// they are; the task verifies the field still exists and reports the count.
func (s *Store) executeConnectFieldMigration(ctx context.Context, task APITask) error {
	var payload connectFieldMigrationPayload
	if err := json.Unmarshal(task.Payload, &payload); err != nil {
		return fmt.Errorf("decode field migration: %w", err)
	}
	var migrated int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM issues WHERE workspace_id=$1 AND fields ? $2`, task.WorkspaceID, payload.FieldID).Scan(&migrated); err != nil {
		return err
	}
	return s.CompleteAPITask(ctx, task, fmt.Sprintf("Migrated %d values.", migrated), map[string]any{"fieldId": payload.FieldID, "migratedValues": migrated})
}
