package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

var (
	ErrFieldContextValidation = errors.New("custom field context request is invalid")
	ErrFieldContextNotFound   = errors.New("custom field context does not exist")
	ErrFieldContextConflict   = errors.New("custom field context conflicts with another")
)

const customFieldContextColumns = `id::text,field_id,name,description,all_projects,all_issue_types`

func scanCustomFieldContext(row pgx.Row) (*models.CustomFieldContext, error) {
	found := &models.CustomFieldContext{}
	if err := row.Scan(&found.ID, &found.FieldID, &found.Name, &found.Description,
		&found.AllProjects, &found.AllIssueTypes); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrFieldContextNotFound
		}
		return nil, err
	}
	return found, nil
}

func customFieldExistsTx(ctx context.Context, tx pgx.Tx, workspaceID, fieldID string) error {
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM custom_fields
		WHERE id=$1 AND (workspace_id IS NULL OR workspace_id=$2))`, fieldID, workspaceID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrFieldContextNotFound
	}
	return nil
}

func customFieldContextTx(ctx context.Context, tx pgx.Tx, fieldID, contextID string) (*models.CustomFieldContext, error) {
	id, err := strconv.ParseInt(contextID, 10, 64)
	if err != nil {
		return nil, ErrFieldContextNotFound
	}
	return scanCustomFieldContext(tx.QueryRow(ctx, `SELECT `+customFieldContextColumns+`
		FROM custom_field_contexts WHERE field_id=$1 AND id=$2`, fieldID, id))
}

func loadContextScopeTx(ctx context.Context, tx pgx.Tx, contexts []*models.CustomFieldContext) error {
	if len(contexts) == 0 {
		return nil
	}
	byID := map[string]*models.CustomFieldContext{}
	ids := make([]int64, 0, len(contexts))
	for _, found := range contexts {
		byID[found.ID] = found
		ids = append(ids, mustParseScreenID(found.ID))
	}
	projects, err := tx.Query(ctx, `SELECT context_id::text,project_id FROM custom_field_context_projects
		WHERE context_id = ANY($1) ORDER BY context_id,project_id`, ids)
	if err != nil {
		return err
	}
	for projects.Next() {
		var contextID, projectID string
		if err = projects.Scan(&contextID, &projectID); err != nil {
			projects.Close()
			return err
		}
		if found, ok := byID[contextID]; ok {
			found.ProjectIDs = append(found.ProjectIDs, projectID)
		}
	}
	projects.Close()
	if err = projects.Err(); err != nil {
		return err
	}
	issueTypes, err := tx.Query(ctx, `SELECT context_id::text,issue_type_id FROM custom_field_context_issue_types
		WHERE context_id = ANY($1) ORDER BY context_id,issue_type_id`, ids)
	if err != nil {
		return err
	}
	for issueTypes.Next() {
		var contextID, issueTypeID string
		if err = issueTypes.Scan(&contextID, &issueTypeID); err != nil {
			issueTypes.Close()
			return err
		}
		if found, ok := byID[contextID]; ok {
			found.IssueTypeIDs = append(found.IssueTypeIDs, issueTypeID)
		}
	}
	issueTypes.Close()
	if err = issueTypes.Err(); err != nil {
		return err
	}
	defaults, err := tx.Query(ctx, `SELECT context_id::text,value FROM custom_field_context_defaults
		WHERE context_id = ANY($1)`, ids)
	if err != nil {
		return err
	}
	defer defaults.Close()
	for defaults.Next() {
		var contextID string
		var value []byte
		if err = defaults.Scan(&contextID, &value); err != nil {
			return err
		}
		if found, ok := byID[contextID]; ok {
			found.DefaultValue = string(value)
		}
	}
	return defaults.Err()
}

// CustomFieldContexts lists one field's contexts with their scope and defaults.
func (s *Store) CustomFieldContexts(ctx context.Context, workspaceID, fieldID string, ids []string) ([]*models.CustomFieldContext, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = customFieldExistsTx(ctx, tx, workspaceID, fieldID); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT `+customFieldContextColumns+`
		FROM custom_field_contexts WHERE field_id=$1 ORDER BY id`, fieldID)
	if err != nil {
		return nil, err
	}
	allowed := map[string]bool{}
	for _, id := range ids {
		allowed[id] = true
	}
	contexts := []*models.CustomFieldContext{}
	for rows.Next() {
		found, scanErr := scanCustomFieldContext(rows)
		if scanErr != nil {
			rows.Close()
			return nil, scanErr
		}
		if len(allowed) == 0 || allowed[found.ID] {
			contexts = append(contexts, found)
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return contexts, loadContextScopeTx(ctx, tx, contexts)
}

// ApplicableCustomFieldContext answers which context governs a field for one
// project and work type, or nil when the field does not apply there.
func (s *Store) ApplicableCustomFieldContext(ctx context.Context, fieldID, projectID, issueTypeID string) (*models.CustomFieldContext, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var contextID *string
	if err = tx.QueryRow(ctx, `SELECT jira_custom_field_context($1,$2,NULLIF($3,''))::text`,
		fieldID, projectID, issueTypeID).Scan(&contextID); err != nil {
		return nil, err
	}
	if contextID == nil {
		return nil, nil
	}
	found, err := customFieldContextTx(ctx, tx, fieldID, *contextID)
	if err != nil {
		return nil, err
	}
	return found, loadContextScopeTx(ctx, tx, []*models.CustomFieldContext{found})
}

func validateContextName(name string) error {
	if name == "" || name != strings.TrimSpace(name) || len(name) > 255 {
		return fmt.Errorf("%w: context name must contain 1 to 255 characters and cannot begin or end with whitespace", ErrFieldContextValidation)
	}
	return nil
}

// assertNoContextOverlapTx keeps Jira's rule that at most one context governs a
// given project and work type, so resolution is never ambiguous.
func assertNoContextOverlapTx(ctx context.Context, tx pgx.Tx, fieldID, contextID string) error {
	var overlapping bool
	err := tx.QueryRow(ctx, `
		WITH subject AS (
			SELECT c.id, c.all_projects, c.all_issue_types,
				COALESCE(array_agg(DISTINCT p.project_id) FILTER (WHERE p.project_id IS NOT NULL), '{}') AS projects,
				COALESCE(array_agg(DISTINCT t.issue_type_id) FILTER (WHERE t.issue_type_id IS NOT NULL), '{}') AS issue_types
			FROM custom_field_contexts c
			LEFT JOIN custom_field_context_projects p ON p.context_id=c.id
			LEFT JOIN custom_field_context_issue_types t ON t.context_id=c.id
			WHERE c.id=$2 GROUP BY c.id
		), others AS (
			SELECT c.id, c.all_projects, c.all_issue_types,
				COALESCE(array_agg(DISTINCT p.project_id) FILTER (WHERE p.project_id IS NOT NULL), '{}') AS projects,
				COALESCE(array_agg(DISTINCT t.issue_type_id) FILTER (WHERE t.issue_type_id IS NOT NULL), '{}') AS issue_types
			FROM custom_field_contexts c
			LEFT JOIN custom_field_context_projects p ON p.context_id=c.id
			LEFT JOIN custom_field_context_issue_types t ON t.context_id=c.id
			WHERE c.field_id=$1 AND c.id<>$2 GROUP BY c.id
		)
		SELECT EXISTS(
			SELECT 1 FROM subject, others
			WHERE (subject.all_projects OR others.all_projects OR subject.projects && others.projects)
			  AND (subject.all_issue_types OR others.all_issue_types OR subject.issue_types && others.issue_types)
		)`, fieldID, contextID).Scan(&overlapping)
	if err != nil {
		return err
	}
	if overlapping {
		return fmt.Errorf("%w: another context already covers these projects and work types", ErrFieldContextConflict)
	}
	return nil
}

func (s *Store) CreateCustomFieldContext(ctx context.Context, workspaceID, actorID, fieldID, name, description string, projectIDs, issueTypeIDs []string) (*models.CustomFieldContext, error) {
	if err := validateContextName(name); err != nil {
		return nil, err
	}
	if len(description) > 255 {
		return nil, fmt.Errorf("%w: description accepts at most 255 characters", ErrFieldContextValidation)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return nil, err
	}
	if err = customFieldExistsTx(ctx, tx, workspaceID, fieldID); err != nil {
		return nil, err
	}
	found, err := scanCustomFieldContext(tx.QueryRow(ctx, `INSERT INTO custom_field_contexts(
		workspace_id,field_id,name,description,all_projects,all_issue_types)
		VALUES($1,$2,$3,$4,$5,$6) RETURNING `+customFieldContextColumns,
		workspaceID, fieldID, name, description, len(projectIDs) == 0, len(issueTypeIDs) == 0))
	if isUniqueViolation(err) {
		return nil, fmt.Errorf("%w: a context with this name already exists for the field", ErrFieldContextConflict)
	}
	if err != nil {
		return nil, err
	}
	if err = addContextProjectsTx(ctx, tx, workspaceID, found.ID, projectIDs); err != nil {
		return nil, err
	}
	if err = addContextIssueTypesTx(ctx, tx, found.ID, issueTypeIDs); err != nil {
		return nil, err
	}
	if err = assertNoContextOverlapTx(ctx, tx, fieldID, found.ID); err != nil {
		return nil, err
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "custom_field_context", found.ID, models.OpUpsert, found); err != nil {
		return nil, err
	}
	return found, tx.Commit(ctx)
}

func addContextProjectsTx(ctx context.Context, tx pgx.Tx, workspaceID, contextID string, projectIDs []string) error {
	for _, projectID := range projectIDs {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM projects WHERE id=$1 AND workspace_id=$2)`,
			projectID, workspaceID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("%w: project %q does not exist", ErrFieldContextValidation, projectID)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO custom_field_context_projects(context_id,project_id)
			VALUES($1,$2) ON CONFLICT DO NOTHING`, contextID, projectID); err != nil {
			return err
		}
	}
	if len(projectIDs) > 0 {
		if _, err := tx.Exec(ctx, `UPDATE custom_field_contexts SET all_projects=FALSE,updated_at=now() WHERE id=$1`, contextID); err != nil {
			return err
		}
	}
	return nil
}

func addContextIssueTypesTx(ctx context.Context, tx pgx.Tx, contextID string, issueTypeIDs []string) error {
	for _, issueTypeID := range issueTypeIDs {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM issue_types WHERE id=$1)`, issueTypeID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("%w: work type %q does not exist", ErrFieldContextValidation, issueTypeID)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO custom_field_context_issue_types(context_id,issue_type_id)
			VALUES($1,$2) ON CONFLICT DO NOTHING`, contextID, issueTypeID); err != nil {
			return err
		}
	}
	if len(issueTypeIDs) > 0 {
		if _, err := tx.Exec(ctx, `UPDATE custom_field_contexts SET all_issue_types=FALSE,updated_at=now() WHERE id=$1`, contextID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) UpdateCustomFieldContext(ctx context.Context, workspaceID, actorID, fieldID, contextID string, name, description *string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return err
	}
	found, err := customFieldContextTx(ctx, tx, fieldID, contextID)
	if err != nil {
		return err
	}
	if name != nil {
		if err = validateContextName(*name); err != nil {
			return err
		}
		found.Name = *name
	}
	if description != nil {
		if len(*description) > 255 {
			return fmt.Errorf("%w: description accepts at most 255 characters", ErrFieldContextValidation)
		}
		found.Description = *description
	}
	_, err = tx.Exec(ctx, `UPDATE custom_field_contexts SET name=$2,description=$3,updated_at=now() WHERE id=$1`,
		found.ID, found.Name, found.Description)
	if isUniqueViolation(err) {
		return fmt.Errorf("%w: a context with this name already exists for the field", ErrFieldContextConflict)
	}
	if err != nil {
		return err
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "custom_field_context", found.ID, models.OpUpsert, found); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) DeleteCustomFieldContext(ctx context.Context, workspaceID, actorID, fieldID, contextID string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return err
	}
	found, err := customFieldContextTx(ctx, tx, fieldID, contextID)
	if err != nil {
		return err
	}
	var remaining int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM custom_field_contexts WHERE field_id=$1`, fieldID).Scan(&remaining); err != nil {
		return err
	}
	if remaining <= 1 {
		return fmt.Errorf("%w: a custom field keeps at least one context", ErrFieldContextConflict)
	}
	if _, err = tx.Exec(ctx, `DELETE FROM custom_field_contexts WHERE id=$1`, found.ID); err != nil {
		return err
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "custom_field_context", found.ID, models.OpDelete, found); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) ChangeCustomFieldContextScope(ctx context.Context, workspaceID, actorID, fieldID, contextID string, projectIDs, issueTypeIDs []string, remove bool) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return err
	}
	found, err := customFieldContextTx(ctx, tx, fieldID, contextID)
	if err != nil {
		return err
	}
	if remove {
		for _, projectID := range projectIDs {
			if _, err = tx.Exec(ctx, `DELETE FROM custom_field_context_projects WHERE context_id=$1 AND project_id=$2`,
				found.ID, projectID); err != nil {
				return err
			}
		}
		for _, issueTypeID := range issueTypeIDs {
			if _, err = tx.Exec(ctx, `DELETE FROM custom_field_context_issue_types WHERE context_id=$1 AND issue_type_id=$2`,
				found.ID, issueTypeID); err != nil {
				return err
			}
		}
		// A context that lists nothing again applies everywhere, which is what
		// Jira means by removing every project or work type from it.
		if _, err = tx.Exec(ctx, `UPDATE custom_field_contexts SET
			all_projects = NOT EXISTS(SELECT 1 FROM custom_field_context_projects WHERE context_id=$1),
			all_issue_types = NOT EXISTS(SELECT 1 FROM custom_field_context_issue_types WHERE context_id=$1),
			updated_at=now() WHERE id=$1`, found.ID); err != nil {
			return err
		}
	} else {
		if err = addContextProjectsTx(ctx, tx, workspaceID, found.ID, projectIDs); err != nil {
			return err
		}
		if err = addContextIssueTypesTx(ctx, tx, found.ID, issueTypeIDs); err != nil {
			return err
		}
	}
	if err = assertNoContextOverlapTx(ctx, tx, fieldID, found.ID); err != nil {
		return err
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "custom_field_context", found.ID, models.OpUpsert,
		map[string]any{"contextId": found.ID, "projectIds": projectIDs, "issueTypeIds": issueTypeIDs, "removed": remove}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// SetCustomFieldContextDefault stores the default value a context applies. An
// empty raw value clears it.
func (s *Store) SetCustomFieldContextDefault(ctx context.Context, workspaceID, actorID, fieldID, contextID, value string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return err
	}
	found, err := customFieldContextTx(ctx, tx, fieldID, contextID)
	if err != nil {
		return err
	}
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || trimmed == "null" {
		if _, err = tx.Exec(ctx, `DELETE FROM custom_field_context_defaults WHERE context_id=$1`, found.ID); err != nil {
			return err
		}
	} else {
		if !json.Valid([]byte(trimmed)) {
			return fmt.Errorf("%w: the default value must be valid JSON", ErrFieldContextValidation)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO custom_field_context_defaults(context_id,value) VALUES($1,$2)
			ON CONFLICT (context_id) DO UPDATE SET value=EXCLUDED.value`, found.ID, trimmed); err != nil {
			return err
		}
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "custom_field_context_default", found.ID, models.OpUpsert,
		map[string]any{"contextId": found.ID, "value": trimmed}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// CustomFieldDefaultsByProject resolves every custom field's applicable context
// and its default value for each project and work type, in one query, for
// IssueCreateMetadata.
func (s *Store) CustomFieldContextsByProject(ctx context.Context, workspaceID string) (map[string]map[string]map[string]models.CustomFieldContextInfo, error) {
	// Every project and work type gets an entry, even when no custom field
	// applies: an empty set means "this form shows no custom fields", which is
	// not the same as "no context governs this form".
	rows, err := s.Pool.Query(ctx, `
		SELECT p.id, work_type.id, f.id,
			COALESCE((SELECT d.value::text FROM custom_field_context_defaults d
				WHERE d.context_id = jira_custom_field_context(f.id,p.id,work_type.id)), '')
		FROM projects p
		CROSS JOIN issue_types work_type
		LEFT JOIN custom_fields f
			ON (f.workspace_id IS NULL OR f.workspace_id=$1) AND f.active AND f.trashed_at IS NULL
			AND jira_custom_field_context(f.id,p.id,work_type.id) IS NOT NULL
		WHERE p.workspace_id=$1`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	resolved := map[string]map[string]map[string]models.CustomFieldContextInfo{}
	for rows.Next() {
		var projectID, issueTypeID, value string
		var fieldID *string
		if err = rows.Scan(&projectID, &issueTypeID, &fieldID, &value); err != nil {
			return nil, err
		}
		if resolved[projectID] == nil {
			resolved[projectID] = map[string]map[string]models.CustomFieldContextInfo{}
		}
		if resolved[projectID][issueTypeID] == nil {
			resolved[projectID][issueTypeID] = map[string]models.CustomFieldContextInfo{}
		}
		if fieldID != nil {
			resolved[projectID][issueTypeID][*fieldID] = models.CustomFieldContextInfo{Default: value}
		}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return resolved, s.attachContextOptions(ctx, workspaceID, resolved)
}

// attachContextOptions fills in each select field's choices from the context
// that governs it, in the order an administrator arranged them.
func (s *Store) attachContextOptions(ctx context.Context, workspaceID string, resolved map[string]map[string]map[string]models.CustomFieldContextInfo) error {
	rows, err := s.Pool.Query(ctx, `
		SELECT p.id, work_type.id, f.id, o.id::text, o.value
		FROM projects p
		CROSS JOIN issue_types work_type
		JOIN custom_fields f ON f.active AND f.trashed_at IS NULL AND f.type=$2 AND (f.workspace_id IS NULL OR f.workspace_id=$1)
		JOIN custom_field_options o
			ON o.context_id = jira_custom_field_context(f.id,p.id,work_type.id) AND NOT o.disabled
		WHERE p.workspace_id=$1
		ORDER BY p.id, work_type.id, f.id, o.position, o.id`, workspaceID, models.CustomFieldSelect)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var projectID, issueTypeID, fieldID, optionID, value string
		if err = rows.Scan(&projectID, &issueTypeID, &fieldID, &optionID, &value); err != nil {
			return err
		}
		types := resolved[projectID]
		if types == nil {
			continue
		}
		fields := types[issueTypeID]
		if fields == nil {
			continue
		}
		info, governed := fields[fieldID]
		if !governed {
			continue
		}
		info.Options = append(info.Options, models.CreateFieldOption{ID: optionID, Name: value})
		fields[fieldID] = info
	}
	return rows.Err()
}

// CustomFieldWriteScope reports which of the workspace's custom fields exist and
// which of them a context reaches for one project and work type, so a write path
// can tell "unknown field" from "out of context" in one round trip.
// CustomFieldOptionScope lists the option IDs each select field offers for one
// project and work type, so a write path can reject a value the applicable
// context does not contain.
func (s *Store) CustomFieldOptionScope(ctx context.Context, workspaceID, projectID, issueTypeID string) (map[string]map[string]bool, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT f.id, o.id::text, o.disabled
		FROM custom_fields f
		JOIN custom_field_options o ON o.context_id = jira_custom_field_context(f.id,$2,NULLIF($3,''))
		WHERE f.active AND f.trashed_at IS NULL AND f.type=$4 AND (f.workspace_id IS NULL OR f.workspace_id=$1)`,
		workspaceID, projectID, issueTypeID, models.CustomFieldSelect)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	scope := map[string]map[string]bool{}
	for rows.Next() {
		var fieldID, optionID string
		var disabled bool
		if err = rows.Scan(&fieldID, &optionID, &disabled); err != nil {
			return nil, err
		}
		if scope[fieldID] == nil {
			scope[fieldID] = map[string]bool{}
		}
		// A disabled option keeps existing values valid but cannot be chosen.
		scope[fieldID][optionID] = !disabled
	}
	return scope, rows.Err()
}

func (s *Store) CustomFieldWriteScope(ctx context.Context, workspaceID, projectID, issueTypeID string) (known, applicable map[string]bool, err error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT f.id, jira_custom_field_context(f.id,$2,NULLIF($3,'')) IS NOT NULL
		FROM custom_fields f
		WHERE f.active AND f.trashed_at IS NULL AND (f.workspace_id IS NULL OR f.workspace_id=$1)`,
		workspaceID, projectID, issueTypeID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	known, applicable = map[string]bool{}, map[string]bool{}
	for rows.Next() {
		var fieldID string
		var applies bool
		if err = rows.Scan(&fieldID, &applies); err != nil {
			return nil, nil, err
		}
		known[fieldID] = true
		if applies {
			applicable[fieldID] = true
		}
	}
	return known, applicable, rows.Err()
}
