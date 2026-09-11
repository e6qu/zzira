package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

var (
	ErrFieldConfigValidation = errors.New("field configuration request is invalid")
	ErrFieldConfigNotFound   = errors.New("field configuration or scheme does not exist")
	ErrFieldConfigConflict   = errors.New("field configuration is in use")
)

const fieldConfigurationColumns = `id::text,name,description,is_default`

func validateFieldConfigName(name, kind string) error {
	if name == "" || name != strings.TrimSpace(name) || len(name) > 255 {
		return fmt.Errorf("%w: %s name must contain 1 to 255 characters and cannot begin or end with whitespace", ErrFieldConfigValidation, kind)
	}
	return nil
}

func scanFieldConfiguration(row pgx.Row) (*models.FieldConfiguration, error) {
	configuration := &models.FieldConfiguration{}
	if err := row.Scan(&configuration.ID, &configuration.Name, &configuration.Description, &configuration.IsDefault); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrFieldConfigNotFound
		}
		return nil, err
	}
	return configuration, nil
}

func fieldConfigurationTx(ctx context.Context, tx pgx.Tx, workspaceID, configurationID string) (*models.FieldConfiguration, error) {
	id, err := strconv.ParseInt(configurationID, 10, 64)
	if err != nil {
		return nil, ErrFieldConfigNotFound
	}
	return scanFieldConfiguration(tx.QueryRow(ctx, `SELECT `+fieldConfigurationColumns+`
		FROM field_configurations WHERE workspace_id=$1 AND id=$2`, workspaceID, id))
}

func (s *Store) FieldConfigurations(ctx context.Context, workspaceID string, ids []string) ([]*models.FieldConfiguration, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+fieldConfigurationColumns+`
		FROM field_configurations WHERE workspace_id=$1 ORDER BY id`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	allowed := map[string]bool{}
	for _, id := range ids {
		allowed[id] = true
	}
	configurations := []*models.FieldConfiguration{}
	for rows.Next() {
		configuration, scanErr := scanFieldConfiguration(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		if len(allowed) == 0 || allowed[configuration.ID] {
			configurations = append(configurations, configuration)
		}
	}
	return configurations, rows.Err()
}

// FieldConfigurationItems returns one configuration's explicit field rules,
// named from the same catalog screens validate against. Fields without a row
// are optional and visible, so they are not listed.
func (s *Store) FieldConfigurationItems(ctx context.Context, workspaceID, configurationID string) ([]models.FieldConfigurationItem, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	configuration, err := fieldConfigurationTx(ctx, tx, workspaceID, configurationID)
	if err != nil {
		return nil, err
	}
	catalog, err := screenFieldCatalogTx(ctx, tx, workspaceID)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT field_id,is_required,is_hidden,description
		FROM field_configuration_items WHERE workspace_id=$1 AND configuration_id=$2 ORDER BY field_id`,
		workspaceID, configuration.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []models.FieldConfigurationItem{}
	for rows.Next() {
		var item models.FieldConfigurationItem
		if err = rows.Scan(&item.FieldID, &item.IsRequired, &item.IsHidden, &item.Description); err != nil {
			return nil, err
		}
		if known, ok := catalog[item.FieldID]; ok {
			item.Name = known.Name
		} else {
			item.Name = item.FieldID
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) CreateFieldConfiguration(ctx context.Context, workspaceID, actorID, name, description string) (*models.FieldConfiguration, error) {
	if err := validateFieldConfigName(name, "field configuration"); err != nil {
		return nil, err
	}
	if len(description) > 255 {
		return nil, fmt.Errorf("%w: description accepts at most 255 characters", ErrFieldConfigValidation)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return nil, err
	}
	configuration, err := scanFieldConfiguration(tx.QueryRow(ctx, `INSERT INTO field_configurations(workspace_id,name,description)
		VALUES($1,$2,$3) RETURNING `+fieldConfigurationColumns, workspaceID, name, description))
	if isUniqueViolation(err) {
		return nil, fmt.Errorf("%w: a field configuration with this name already exists", ErrFieldConfigConflict)
	}
	if err != nil {
		return nil, err
	}
	// A new configuration starts from Jira's baseline: summary is required.
	if _, err = tx.Exec(ctx, `INSERT INTO field_configuration_items(workspace_id,configuration_id,field_id,is_required)
		VALUES($1,$2,'summary',TRUE)`, workspaceID, configuration.ID); err != nil {
		return nil, err
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "field_configuration", configuration.ID, models.OpUpsert, configuration); err != nil {
		return nil, err
	}
	return configuration, tx.Commit(ctx)
}

func (s *Store) UpdateFieldConfiguration(ctx context.Context, workspaceID, actorID, configurationID string, name, description *string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return err
	}
	configuration, err := fieldConfigurationTx(ctx, tx, workspaceID, configurationID)
	if err != nil {
		return err
	}
	if name != nil {
		if err = validateFieldConfigName(*name, "field configuration"); err != nil {
			return err
		}
		configuration.Name = *name
	}
	if description != nil {
		if len(*description) > 255 {
			return fmt.Errorf("%w: description accepts at most 255 characters", ErrFieldConfigValidation)
		}
		configuration.Description = *description
	}
	_, err = tx.Exec(ctx, `UPDATE field_configurations SET name=$3,description=$4,updated_at=now()
		WHERE workspace_id=$1 AND id=$2`, workspaceID, configuration.ID, configuration.Name, configuration.Description)
	if isUniqueViolation(err) {
		return fmt.Errorf("%w: a field configuration with this name already exists", ErrFieldConfigConflict)
	}
	if err != nil {
		return err
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "field_configuration", configuration.ID, models.OpUpsert, configuration); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// SetFieldConfigurationItems applies Jira's partial item update: only the
// supplied fields change, and a field cannot be required and hidden at once.
func (s *Store) SetFieldConfigurationItems(ctx context.Context, workspaceID, actorID, configurationID string, items []models.FieldConfigurationItem) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return err
	}
	configuration, err := fieldConfigurationTx(ctx, tx, workspaceID, configurationID)
	if err != nil {
		return err
	}
	catalog, err := screenFieldCatalogTx(ctx, tx, workspaceID)
	if err != nil {
		return err
	}
	for _, item := range items {
		fieldID := strings.TrimSpace(item.FieldID)
		if _, known := catalog[fieldID]; !known {
			return fmt.Errorf("%w: field %q does not exist", ErrFieldConfigValidation, fieldID)
		}
		if item.IsRequired && item.IsHidden {
			return fmt.Errorf("%w: field %q cannot be required and hidden", ErrFieldConfigValidation, fieldID)
		}
		if fieldID == "summary" && (item.IsHidden || !item.IsRequired) {
			return fmt.Errorf("%w: summary stays required on every work item", ErrFieldConfigValidation)
		}
		if len(item.Description) > 255 {
			return fmt.Errorf("%w: field description accepts at most 255 characters", ErrFieldConfigValidation)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO field_configuration_items(workspace_id,configuration_id,field_id,is_required,is_hidden,description)
			VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT (configuration_id,field_id)
			DO UPDATE SET is_required=EXCLUDED.is_required,is_hidden=EXCLUDED.is_hidden,description=EXCLUDED.description`,
			workspaceID, configuration.ID, fieldID, item.IsRequired, item.IsHidden, item.Description); err != nil {
			return err
		}
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "field_configuration_item", configuration.ID, models.OpUpsert,
		map[string]any{"fieldConfigurationId": configuration.ID, "items": items}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) DeleteFieldConfiguration(ctx context.Context, workspaceID, actorID, configurationID string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return err
	}
	configuration, err := fieldConfigurationTx(ctx, tx, workspaceID, configurationID)
	if err != nil {
		return err
	}
	if configuration.IsDefault {
		return fmt.Errorf("%w: the default field configuration cannot be deleted", ErrFieldConfigConflict)
	}
	var used int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM field_configuration_scheme_items WHERE configuration_id=$1`, configuration.ID).Scan(&used); err != nil {
		return err
	}
	if used > 0 {
		return fmt.Errorf("%w: remove this configuration from every work type mapping first", ErrFieldConfigConflict)
	}
	if _, err = tx.Exec(ctx, `DELETE FROM field_configurations WHERE workspace_id=$1 AND id=$2`, workspaceID, configuration.ID); err != nil {
		return err
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "field_configuration", configuration.ID, models.OpDelete, configuration); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
