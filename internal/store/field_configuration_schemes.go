package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

const fieldConfigurationSchemeColumns = `id::text,name,description,is_default`

func scanFieldConfigurationScheme(row pgx.Row) (*models.FieldConfigurationScheme, error) {
	scheme := &models.FieldConfigurationScheme{}
	if err := row.Scan(&scheme.ID, &scheme.Name, &scheme.Description, &scheme.IsDefault); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrFieldConfigNotFound
		}
		return nil, err
	}
	return scheme, nil
}

func loadFieldConfigurationSchemeItemsTx(ctx context.Context, tx pgx.Tx, workspaceID string, schemes []*models.FieldConfigurationScheme) error {
	if len(schemes) == 0 {
		return nil
	}
	byID := map[string]*models.FieldConfigurationScheme{}
	ids := make([]int64, 0, len(schemes))
	for _, scheme := range schemes {
		byID[scheme.ID] = scheme
		ids = append(ids, mustParseScreenID(scheme.ID))
	}
	rows, err := tx.Query(ctx, `SELECT scheme_id::text,issue_type_id,configuration_id::text
		FROM field_configuration_scheme_items WHERE workspace_id=$1 AND scheme_id = ANY($2)
		ORDER BY scheme_id,issue_type_id`, workspaceID, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var schemeID string
		var item models.FieldConfigurationSchemeItem
		if err = rows.Scan(&schemeID, &item.IssueTypeID, &item.FieldConfigurationID); err != nil {
			return err
		}
		if scheme, ok := byID[schemeID]; ok {
			scheme.Mappings = append(scheme.Mappings, item)
		}
	}
	return rows.Err()
}

func fieldConfigurationSchemeTx(ctx context.Context, tx pgx.Tx, workspaceID, schemeID string) (*models.FieldConfigurationScheme, error) {
	id, err := strconv.ParseInt(schemeID, 10, 64)
	if err != nil {
		return nil, ErrFieldConfigNotFound
	}
	scheme, err := scanFieldConfigurationScheme(tx.QueryRow(ctx, `SELECT `+fieldConfigurationSchemeColumns+`
		FROM field_configuration_schemes WHERE workspace_id=$1 AND id=$2`, workspaceID, id))
	if err != nil {
		return nil, err
	}
	return scheme, loadFieldConfigurationSchemeItemsTx(ctx, tx, workspaceID, []*models.FieldConfigurationScheme{scheme})
}

func (s *Store) FieldConfigurationSchemes(ctx context.Context, workspaceID string, ids []string) ([]*models.FieldConfigurationScheme, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `SELECT `+fieldConfigurationSchemeColumns+`
		FROM field_configuration_schemes WHERE workspace_id=$1 ORDER BY id`, workspaceID)
	if err != nil {
		return nil, err
	}
	allowed := map[string]bool{}
	for _, id := range ids {
		allowed[id] = true
	}
	schemes := []*models.FieldConfigurationScheme{}
	for rows.Next() {
		scheme, scanErr := scanFieldConfigurationScheme(rows)
		if scanErr != nil {
			rows.Close()
			return nil, scanErr
		}
		if len(allowed) == 0 || allowed[scheme.ID] {
			schemes = append(schemes, scheme)
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return schemes, loadFieldConfigurationSchemeItemsTx(ctx, tx, workspaceID, schemes)
}

func (s *Store) CreateFieldConfigurationScheme(ctx context.Context, workspaceID, actorID, name, description string) (*models.FieldConfigurationScheme, error) {
	if err := validateFieldConfigName(name, "field configuration scheme"); err != nil {
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
	scheme, err := scanFieldConfigurationScheme(tx.QueryRow(ctx, `INSERT INTO field_configuration_schemes(workspace_id,name,description)
		VALUES($1,$2,$3) RETURNING `+fieldConfigurationSchemeColumns, workspaceID, name, description))
	if isUniqueViolation(err) {
		return nil, fmt.Errorf("%w: a field configuration scheme with this name already exists", ErrFieldConfigConflict)
	}
	if err != nil {
		return nil, err
	}
	// The scheme starts pointing every work type at the default configuration,
	// so it can be assigned to a project before anything else is mapped.
	if _, err = tx.Exec(ctx, `INSERT INTO field_configuration_scheme_items(workspace_id,scheme_id,issue_type_id,configuration_id)
		SELECT $1,$2,$3,id FROM field_configurations WHERE workspace_id=$1 AND is_default`,
		workspaceID, scheme.ID, DefaultIssueTypeMapping); err != nil {
		return nil, err
	}
	if scheme, err = fieldConfigurationSchemeTx(ctx, tx, workspaceID, scheme.ID); err != nil {
		return nil, err
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "field_configuration_scheme", scheme.ID, models.OpUpsert, scheme); err != nil {
		return nil, err
	}
	return scheme, tx.Commit(ctx)
}

func (s *Store) UpdateFieldConfigurationScheme(ctx context.Context, workspaceID, actorID, schemeID string, name, description *string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return err
	}
	scheme, err := fieldConfigurationSchemeTx(ctx, tx, workspaceID, schemeID)
	if err != nil {
		return err
	}
	if name != nil {
		if err = validateFieldConfigName(*name, "field configuration scheme"); err != nil {
			return err
		}
		scheme.Name = *name
	}
	if description != nil {
		if len(*description) > 255 {
			return fmt.Errorf("%w: description accepts at most 255 characters", ErrFieldConfigValidation)
		}
		scheme.Description = *description
	}
	_, err = tx.Exec(ctx, `UPDATE field_configuration_schemes SET name=$3,description=$4,updated_at=now()
		WHERE workspace_id=$1 AND id=$2`, workspaceID, scheme.ID, scheme.Name, scheme.Description)
	if isUniqueViolation(err) {
		return fmt.Errorf("%w: a field configuration scheme with this name already exists", ErrFieldConfigConflict)
	}
	if err != nil {
		return err
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "field_configuration_scheme", scheme.ID, models.OpUpsert, scheme); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) SetFieldConfigurationSchemeMappings(ctx context.Context, workspaceID, actorID, schemeID string, mappings []models.FieldConfigurationSchemeItem) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return err
	}
	scheme, err := fieldConfigurationSchemeTx(ctx, tx, workspaceID, schemeID)
	if err != nil {
		return err
	}
	for _, mapping := range mappings {
		if mapping.IssueTypeID == "" || mapping.FieldConfigurationID == "" {
			return fmt.Errorf("%w: each mapping needs a work type and a field configuration", ErrFieldConfigValidation)
		}
		if mapping.IssueTypeID != DefaultIssueTypeMapping {
			var known bool
			if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM issue_types WHERE id=$1)`, mapping.IssueTypeID).Scan(&known); err != nil {
				return err
			}
			if !known {
				return fmt.Errorf("%w: work type %q does not exist", ErrFieldConfigValidation, mapping.IssueTypeID)
			}
		}
		if _, err = fieldConfigurationTx(ctx, tx, workspaceID, mapping.FieldConfigurationID); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO field_configuration_scheme_items(workspace_id,scheme_id,issue_type_id,configuration_id)
			VALUES($1,$2,$3,$4) ON CONFLICT (scheme_id,issue_type_id) DO UPDATE SET configuration_id=EXCLUDED.configuration_id`,
			workspaceID, scheme.ID, mapping.IssueTypeID, mapping.FieldConfigurationID); err != nil {
			return err
		}
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "field_configuration_scheme", scheme.ID, models.OpUpsert,
		map[string]any{"schemeId": scheme.ID, "mappings": mappings}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) RemoveFieldConfigurationSchemeMappings(ctx context.Context, workspaceID, actorID, schemeID string, issueTypeIDs []string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return err
	}
	scheme, err := fieldConfigurationSchemeTx(ctx, tx, workspaceID, schemeID)
	if err != nil {
		return err
	}
	for _, issueTypeID := range issueTypeIDs {
		if issueTypeID == DefaultIssueTypeMapping {
			return fmt.Errorf("%w: the default mapping cannot be removed", ErrFieldConfigValidation)
		}
		command, execErr := tx.Exec(ctx, `DELETE FROM field_configuration_scheme_items
			WHERE workspace_id=$1 AND scheme_id=$2 AND issue_type_id=$3`, workspaceID, scheme.ID, issueTypeID)
		if execErr != nil {
			return execErr
		}
		if command.RowsAffected() == 0 {
			return ErrFieldConfigNotFound
		}
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "field_configuration_scheme", scheme.ID, models.OpUpsert,
		map[string]any{"schemeId": scheme.ID, "removedIssueTypeIds": issueTypeIDs}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) DeleteFieldConfigurationScheme(ctx context.Context, workspaceID, actorID, schemeID string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return err
	}
	scheme, err := fieldConfigurationSchemeTx(ctx, tx, workspaceID, schemeID)
	if err != nil {
		return err
	}
	if scheme.IsDefault {
		return fmt.Errorf("%w: the default field configuration scheme cannot be deleted", ErrFieldConfigConflict)
	}
	var projects int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM project_field_configuration_schemes WHERE scheme_id=$1`, scheme.ID).Scan(&projects); err != nil {
		return err
	}
	if projects > 0 {
		return fmt.Errorf("%w: reassign every project before deleting this scheme", ErrFieldConfigConflict)
	}
	if _, err = tx.Exec(ctx, `DELETE FROM field_configuration_schemes WHERE workspace_id=$1 AND id=$2`, workspaceID, scheme.ID); err != nil {
		return err
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "field_configuration_scheme", scheme.ID, models.OpDelete, scheme); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type FieldConfigurationSchemeProject struct {
	SchemeID  string
	ProjectID string
}

func (s *Store) FieldConfigurationSchemeProjects(ctx context.Context, workspaceID string) ([]FieldConfigurationSchemeProject, error) {
	rows, err := s.Pool.Query(ctx, `SELECT scheme_id::text,project_id FROM project_field_configuration_schemes
		WHERE workspace_id=$1 ORDER BY scheme_id,project_id`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	assignments := []FieldConfigurationSchemeProject{}
	for rows.Next() {
		var assignment FieldConfigurationSchemeProject
		if err = rows.Scan(&assignment.SchemeID, &assignment.ProjectID); err != nil {
			return nil, err
		}
		assignments = append(assignments, assignment)
	}
	return assignments, rows.Err()
}

func (s *Store) AssignFieldConfigurationScheme(ctx context.Context, workspaceID, actorID, projectID, schemeID string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return err
	}
	scheme, err := fieldConfigurationSchemeTx(ctx, tx, workspaceID, schemeID)
	if err != nil {
		return err
	}
	project, err := scanProject(tx.QueryRow(ctx, `SELECT `+projectSelectColumns+`
		FROM projects WHERE workspace_id=$1 AND lifecycle_state='ACTIVE' AND (id=$2 OR upper(key)=upper($2))`, workspaceID, projectID))
	if err != nil {
		return ErrFieldConfigNotFound
	}
	if _, err = tx.Exec(ctx, `INSERT INTO project_field_configuration_schemes(project_id,workspace_id,scheme_id)
		VALUES($1,$2,$3) ON CONFLICT (project_id) DO UPDATE SET scheme_id=EXCLUDED.scheme_id`,
		project.ID, workspaceID, scheme.ID); err != nil {
		return err
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "project_field_configuration_scheme", project.ID, models.OpUpsert,
		map[string]any{"projectId": project.ID, "fieldConfigurationSchemeId": scheme.ID}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ResolveFieldBehaviour returns the per-field rules the project's field
// configuration scheme applies to one work type, following project → scheme →
// work type mapping (falling back to default) → configuration. Fields without
// an explicit rule are optional and visible, so they are absent from the map.
func (s *Store) ResolveFieldBehaviour(ctx context.Context, workspaceID, projectID, issueTypeID string) (map[string]models.FieldBehaviour, error) {
	rows, err := s.Pool.Query(ctx, `
		WITH assigned AS (
			SELECT scheme_id FROM project_field_configuration_schemes
			WHERE workspace_id=$1 AND project_id=$2
		), chosen AS (
			SELECT item.configuration_id FROM field_configuration_scheme_items item
			JOIN assigned ON assigned.scheme_id=item.scheme_id
			WHERE item.issue_type_id IN ($3, $4)
			ORDER BY (item.issue_type_id = $3) DESC LIMIT 1
		)
		SELECT item.field_id,item.is_required,item.is_hidden,item.description
		FROM field_configuration_items item
		JOIN chosen ON chosen.configuration_id=item.configuration_id`,
		workspaceID, projectID, issueTypeID, DefaultIssueTypeMapping)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	behaviour := map[string]models.FieldBehaviour{}
	for rows.Next() {
		var fieldID string
		var rule models.FieldBehaviour
		if err = rows.Scan(&fieldID, &rule.IsRequired, &rule.IsHidden, &rule.Description); err != nil {
			return nil, err
		}
		behaviour[fieldID] = rule
	}
	return behaviour, rows.Err()
}

// ResolveFieldBehaviourByProject answers ResolveFieldBehaviour for every project
// and work type in one query, for IssueCreateMetadata.
func (s *Store) ResolveFieldBehaviourByProject(ctx context.Context, workspaceID string) (map[string]map[string]map[string]models.FieldBehaviour, error) {
	rows, err := s.Pool.Query(ctx, `
		WITH assigned AS (
			SELECT p.id AS project_id, a.scheme_id
			FROM projects p
			JOIN project_field_configuration_schemes a ON a.project_id=p.id
			WHERE p.workspace_id=$1
		), chosen AS (
			SELECT DISTINCT ON (assigned.project_id, work_type.id)
				assigned.project_id, work_type.id AS issue_type_id, item.configuration_id
			FROM assigned
			CROSS JOIN issue_types work_type
			JOIN field_configuration_scheme_items item
				ON item.scheme_id=assigned.scheme_id
				AND item.issue_type_id IN (work_type.id, $2)
			ORDER BY assigned.project_id, work_type.id, (item.issue_type_id = work_type.id) DESC
		)
		SELECT chosen.project_id,chosen.issue_type_id,item.field_id,item.is_required,item.is_hidden,item.description
		FROM chosen
		JOIN field_configuration_items item ON item.configuration_id=chosen.configuration_id`,
		workspaceID, DefaultIssueTypeMapping)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	resolved := map[string]map[string]map[string]models.FieldBehaviour{}
	for rows.Next() {
		var projectID, issueTypeID, fieldID string
		var rule models.FieldBehaviour
		if err = rows.Scan(&projectID, &issueTypeID, &fieldID, &rule.IsRequired, &rule.IsHidden, &rule.Description); err != nil {
			return nil, err
		}
		if resolved[projectID] == nil {
			resolved[projectID] = map[string]map[string]models.FieldBehaviour{}
		}
		if resolved[projectID][issueTypeID] == nil {
			resolved[projectID][issueTypeID] = map[string]models.FieldBehaviour{}
		}
		resolved[projectID][issueTypeID][fieldID] = rule
	}
	return resolved, rows.Err()
}
