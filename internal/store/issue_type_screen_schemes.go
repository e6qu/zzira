package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

const issueTypeScreenSchemeColumns = `id::text,name,description,is_default`

func scanIssueTypeScreenScheme(row pgx.Row) (*models.IssueTypeScreenScheme, error) {
	scheme := &models.IssueTypeScreenScheme{}
	if err := row.Scan(&scheme.ID, &scheme.Name, &scheme.Description, &scheme.IsDefault); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrScreenNotFound
		}
		return nil, err
	}
	return scheme, nil
}

func loadIssueTypeScreenSchemeItemsTx(ctx context.Context, tx pgx.Tx, workspaceID string, schemes []*models.IssueTypeScreenScheme) error {
	if len(schemes) == 0 {
		return nil
	}
	byID := map[string]*models.IssueTypeScreenScheme{}
	ids := make([]int64, 0, len(schemes))
	for _, scheme := range schemes {
		byID[scheme.ID] = scheme
		ids = append(ids, mustParseScreenID(scheme.ID))
	}
	rows, err := tx.Query(ctx, `SELECT scheme_id::text,issue_type_id,screen_scheme_id::text
		FROM issue_type_screen_scheme_items WHERE workspace_id=$1 AND scheme_id = ANY($2)
		ORDER BY scheme_id,issue_type_id`, workspaceID, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var schemeID string
		var item models.IssueTypeScreenSchemeItem
		if err = rows.Scan(&schemeID, &item.IssueTypeID, &item.ScreenSchemeID); err != nil {
			return err
		}
		if scheme, ok := byID[schemeID]; ok {
			scheme.Mappings = append(scheme.Mappings, item)
		}
	}
	return rows.Err()
}

func issueTypeScreenSchemeTx(ctx context.Context, tx pgx.Tx, workspaceID, schemeID string) (*models.IssueTypeScreenScheme, error) {
	id, err := strconv.ParseInt(schemeID, 10, 64)
	if err != nil {
		return nil, ErrScreenNotFound
	}
	scheme, err := scanIssueTypeScreenScheme(tx.QueryRow(ctx, `SELECT `+issueTypeScreenSchemeColumns+`
		FROM issue_type_screen_schemes WHERE workspace_id=$1 AND id=$2`, workspaceID, id))
	if err != nil {
		return nil, err
	}
	return scheme, loadIssueTypeScreenSchemeItemsTx(ctx, tx, workspaceID, []*models.IssueTypeScreenScheme{scheme})
}

func (s *Store) IssueTypeScreenSchemes(ctx context.Context, workspaceID string, ids []string) ([]*models.IssueTypeScreenScheme, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `SELECT `+issueTypeScreenSchemeColumns+`
		FROM issue_type_screen_schemes WHERE workspace_id=$1 ORDER BY id`, workspaceID)
	if err != nil {
		return nil, err
	}
	allowed := map[string]bool{}
	for _, id := range ids {
		allowed[id] = true
	}
	schemes := []*models.IssueTypeScreenScheme{}
	for rows.Next() {
		scheme, scanErr := scanIssueTypeScreenScheme(rows)
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
	return schemes, loadIssueTypeScreenSchemeItemsTx(ctx, tx, workspaceID, schemes)
}

func appendIssueTypeScreenSchemeMappingsTx(ctx context.Context, tx pgx.Tx, workspaceID, schemeID string, mappings []models.IssueTypeScreenSchemeItem) error {
	for _, mapping := range mappings {
		if mapping.IssueTypeID == "" || mapping.ScreenSchemeID == "" {
			return fmt.Errorf("%w: each mapping needs a work type and a screen scheme", ErrScreenValidation)
		}
		if mapping.IssueTypeID != DefaultIssueTypeMapping {
			var known bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM issue_types WHERE id=$1)`, mapping.IssueTypeID).Scan(&known); err != nil {
				return err
			}
			if !known {
				return fmt.Errorf("%w: work type %q does not exist", ErrScreenValidation, mapping.IssueTypeID)
			}
		}
		if _, err := screenSchemeTx(ctx, tx, workspaceID, mapping.ScreenSchemeID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO issue_type_screen_scheme_items(workspace_id,scheme_id,issue_type_id,screen_scheme_id)
			VALUES($1,$2,$3,$4) ON CONFLICT (scheme_id,issue_type_id) DO UPDATE SET screen_scheme_id=EXCLUDED.screen_scheme_id`,
			workspaceID, schemeID, mapping.IssueTypeID, mapping.ScreenSchemeID); err != nil {
			return err
		}
	}
	return nil
}

func requireDefaultMappingTx(ctx context.Context, tx pgx.Tx, schemeID string) error {
	var hasDefault bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM issue_type_screen_scheme_items
		WHERE scheme_id=$1 AND issue_type_id=$2)`, schemeID, DefaultIssueTypeMapping).Scan(&hasDefault); err != nil {
		return err
	}
	if !hasDefault {
		return fmt.Errorf("%w: a work type screen scheme requires a default mapping", ErrScreenValidation)
	}
	return nil
}

func (s *Store) CreateIssueTypeScreenScheme(ctx context.Context, workspaceID, actorID, name, description string, mappings []models.IssueTypeScreenSchemeItem) (*models.IssueTypeScreenScheme, error) {
	if err := validateScreenName(name, "work type screen scheme"); err != nil {
		return nil, err
	}
	if len(description) > 255 {
		return nil, fmt.Errorf("%w: description accepts at most 255 characters", ErrScreenValidation)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return nil, err
	}
	scheme, err := scanIssueTypeScreenScheme(tx.QueryRow(ctx, `INSERT INTO issue_type_screen_schemes(workspace_id,name,description)
		VALUES($1,$2,$3) RETURNING `+issueTypeScreenSchemeColumns, workspaceID, name, description))
	if isUniqueViolation(err) {
		return nil, fmt.Errorf("%w: a work type screen scheme with this name already exists", ErrScreenConflict)
	}
	if err != nil {
		return nil, err
	}
	if err = appendIssueTypeScreenSchemeMappingsTx(ctx, tx, workspaceID, scheme.ID, mappings); err != nil {
		return nil, err
	}
	if err = requireDefaultMappingTx(ctx, tx, scheme.ID); err != nil {
		return nil, err
	}
	if scheme, err = issueTypeScreenSchemeTx(ctx, tx, workspaceID, scheme.ID); err != nil {
		return nil, err
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "issue_type_screen_scheme", scheme.ID, models.OpUpsert, scheme); err != nil {
		return nil, err
	}
	return scheme, tx.Commit(ctx)
}

func (s *Store) UpdateIssueTypeScreenScheme(ctx context.Context, workspaceID, actorID, schemeID string, name, description *string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return err
	}
	scheme, err := issueTypeScreenSchemeTx(ctx, tx, workspaceID, schemeID)
	if err != nil {
		return err
	}
	if name != nil {
		if err = validateScreenName(*name, "work type screen scheme"); err != nil {
			return err
		}
		scheme.Name = *name
	}
	if description != nil {
		if len(*description) > 255 {
			return fmt.Errorf("%w: description accepts at most 255 characters", ErrScreenValidation)
		}
		scheme.Description = *description
	}
	_, err = tx.Exec(ctx, `UPDATE issue_type_screen_schemes SET name=$3,description=$4,updated_at=now()
		WHERE workspace_id=$1 AND id=$2`, workspaceID, scheme.ID, scheme.Name, scheme.Description)
	if isUniqueViolation(err) {
		return fmt.Errorf("%w: a work type screen scheme with this name already exists", ErrScreenConflict)
	}
	if err != nil {
		return err
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "issue_type_screen_scheme", scheme.ID, models.OpUpsert, scheme); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) AppendIssueTypeScreenSchemeMappings(ctx context.Context, workspaceID, actorID, schemeID string, mappings []models.IssueTypeScreenSchemeItem) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return err
	}
	scheme, err := issueTypeScreenSchemeTx(ctx, tx, workspaceID, schemeID)
	if err != nil {
		return err
	}
	if err = appendIssueTypeScreenSchemeMappingsTx(ctx, tx, workspaceID, scheme.ID, mappings); err != nil {
		return err
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "issue_type_screen_scheme", scheme.ID, models.OpUpsert,
		map[string]any{"schemeId": scheme.ID, "mappings": mappings}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// SetIssueTypeScreenSchemeDefault replaces the scheme's fallback mapping.
func (s *Store) SetIssueTypeScreenSchemeDefault(ctx context.Context, workspaceID, actorID, schemeID, screenSchemeID string) error {
	return s.AppendIssueTypeScreenSchemeMappings(ctx, workspaceID, actorID, schemeID,
		[]models.IssueTypeScreenSchemeItem{{IssueTypeID: DefaultIssueTypeMapping, ScreenSchemeID: screenSchemeID}})
}

func (s *Store) RemoveIssueTypeScreenSchemeMappings(ctx context.Context, workspaceID, actorID, schemeID string, issueTypeIDs []string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return err
	}
	scheme, err := issueTypeScreenSchemeTx(ctx, tx, workspaceID, schemeID)
	if err != nil {
		return err
	}
	for _, issueTypeID := range issueTypeIDs {
		if issueTypeID == DefaultIssueTypeMapping {
			return fmt.Errorf("%w: the default mapping cannot be removed", ErrScreenValidation)
		}
		command, execErr := tx.Exec(ctx, `DELETE FROM issue_type_screen_scheme_items
			WHERE workspace_id=$1 AND scheme_id=$2 AND issue_type_id=$3`, workspaceID, scheme.ID, issueTypeID)
		if execErr != nil {
			return execErr
		}
		if command.RowsAffected() == 0 {
			return ErrScreenNotFound
		}
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "issue_type_screen_scheme", scheme.ID, models.OpUpsert,
		map[string]any{"schemeId": scheme.ID, "removedIssueTypeIds": issueTypeIDs}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) DeleteIssueTypeScreenScheme(ctx context.Context, workspaceID, actorID, schemeID string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return err
	}
	scheme, err := issueTypeScreenSchemeTx(ctx, tx, workspaceID, schemeID)
	if err != nil {
		return err
	}
	if scheme.IsDefault {
		return fmt.Errorf("%w: the default work type screen scheme cannot be deleted", ErrScreenConflict)
	}
	var projects int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM project_issue_type_screen_schemes WHERE scheme_id=$1`, scheme.ID).Scan(&projects); err != nil {
		return err
	}
	if projects > 0 {
		return fmt.Errorf("%w: reassign every project before deleting this scheme", ErrScreenConflict)
	}
	if _, err = tx.Exec(ctx, `DELETE FROM issue_type_screen_schemes WHERE workspace_id=$1 AND id=$2`, workspaceID, scheme.ID); err != nil {
		return err
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "issue_type_screen_scheme", scheme.ID, models.OpDelete, scheme); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type IssueTypeScreenSchemeProject struct {
	SchemeID  string
	ProjectID string
}

func (s *Store) IssueTypeScreenSchemeProjects(ctx context.Context, workspaceID string) ([]IssueTypeScreenSchemeProject, error) {
	rows, err := s.Pool.Query(ctx, `SELECT scheme_id::text,project_id FROM project_issue_type_screen_schemes
		WHERE workspace_id=$1 ORDER BY scheme_id,project_id`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	assignments := []IssueTypeScreenSchemeProject{}
	for rows.Next() {
		var assignment IssueTypeScreenSchemeProject
		if err = rows.Scan(&assignment.SchemeID, &assignment.ProjectID); err != nil {
			return nil, err
		}
		assignments = append(assignments, assignment)
	}
	return assignments, rows.Err()
}

func (s *Store) AssignIssueTypeScreenScheme(ctx context.Context, workspaceID, actorID, projectID, schemeID string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return err
	}
	scheme, err := issueTypeScreenSchemeTx(ctx, tx, workspaceID, schemeID)
	if err != nil {
		return err
	}
	project, err := scanProject(tx.QueryRow(ctx, `SELECT `+projectSelectColumns+`
		FROM projects WHERE workspace_id=$1 AND lifecycle_state='ACTIVE' AND (id=$2 OR upper(key)=upper($2))`, workspaceID, projectID))
	if err != nil {
		return ErrScreenNotFound
	}
	if _, err = tx.Exec(ctx, `INSERT INTO project_issue_type_screen_schemes(project_id,workspace_id,scheme_id)
		VALUES($1,$2,$3) ON CONFLICT (project_id) DO UPDATE SET scheme_id=EXCLUDED.scheme_id`,
		project.ID, workspaceID, scheme.ID); err != nil {
		return err
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "project_issue_type_screen_scheme", project.ID, models.OpUpsert,
		map[string]any{"projectId": project.ID, "issueTypeScreenSchemeId": scheme.ID}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ResolveScreenFields returns the ordered field IDs the given form operation
// shows for one project and work type, following Jira's chain: project → work
// type screen scheme → screen scheme → screen. An unmapped work type falls back
// to the scheme's default mapping, and an unmapped operation to the screen
// scheme's default screen. A nil result means no screen governs the form, and
// callers keep their full field set.
func (s *Store) ResolveScreenFields(ctx context.Context, workspaceID, projectID, issueTypeID, operation string) ([]string, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	return resolveScreenFieldsTx(ctx, tx, workspaceID, projectID, issueTypeID, operation)
}

func resolveScreenFieldsTx(ctx context.Context, tx pgx.Tx, workspaceID, projectID, issueTypeID, operation string) ([]string, error) {
	var screenID string
	err := tx.QueryRow(ctx, `
		WITH assigned AS (
			SELECT scheme_id FROM project_issue_type_screen_schemes
			WHERE workspace_id=$1 AND project_id=$2
		), screen_scheme AS (
			SELECT item.screen_scheme_id FROM issue_type_screen_scheme_items item
			JOIN assigned ON assigned.scheme_id=item.scheme_id
			WHERE item.issue_type_id IN ($3, $5)
			ORDER BY (item.issue_type_id = $3) DESC LIMIT 1
		)
		SELECT item.screen_id::text FROM screen_scheme_items item
		JOIN screen_scheme ON screen_scheme.screen_scheme_id=item.scheme_id
		WHERE item.operation IN ($4, 'default')
		ORDER BY (item.operation = $4) DESC LIMIT 1`,
		workspaceID, projectID, issueTypeID, operation, DefaultIssueTypeMapping).Scan(&screenID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT f.field_id FROM screen_tab_fields f
		JOIN screen_tabs t ON t.id=f.tab_id
		WHERE f.screen_id=$1 ORDER BY t.position, t.id, f.position, f.field_id`, screenID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	fields := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		fields = append(fields, id)
	}
	return fields, rows.Err()
}

// ResolveScreenFieldsByProject answers ResolveScreenFields for every project and
// work type in the workspace with one query. IssueCreateMetadata builds the
// whole workspace at once, so resolving per project and work type would open a
// transaction per combination.
func (s *Store) ResolveScreenFieldsByProject(ctx context.Context, workspaceID, operation string) (map[string]map[string][]string, error) {
	rows, err := s.Pool.Query(ctx, `
		WITH assigned AS (
			SELECT p.id AS project_id, a.scheme_id
			FROM projects p
			JOIN project_issue_type_screen_schemes a ON a.project_id=p.id
			WHERE p.workspace_id=$1
		), pick_scheme AS (
			SELECT DISTINCT ON (assigned.project_id, work_type.id)
				assigned.project_id, work_type.id AS issue_type_id, item.screen_scheme_id
			FROM assigned
			CROSS JOIN issue_types work_type
			JOIN issue_type_screen_scheme_items item
				ON item.scheme_id=assigned.scheme_id
				AND item.issue_type_id IN (work_type.id, $3)
			ORDER BY assigned.project_id, work_type.id, (item.issue_type_id = work_type.id) DESC
		), pick_screen AS (
			SELECT DISTINCT ON (pick_scheme.project_id, pick_scheme.issue_type_id)
				pick_scheme.project_id, pick_scheme.issue_type_id, item.screen_id
			FROM pick_scheme
			JOIN screen_scheme_items item
				ON item.scheme_id=pick_scheme.screen_scheme_id
				AND item.operation IN ($2, 'default')
			ORDER BY pick_scheme.project_id, pick_scheme.issue_type_id, (item.operation = $2) DESC
		)
		SELECT chosen.project_id, chosen.issue_type_id, field.field_id
		FROM pick_screen chosen
		JOIN screen_tab_fields field ON field.screen_id=chosen.screen_id
		JOIN screen_tabs tab ON tab.id=field.tab_id
		ORDER BY chosen.project_id, chosen.issue_type_id, tab.position, tab.id, field.position, field.field_id`,
		workspaceID, operation, DefaultIssueTypeMapping)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	resolved := map[string]map[string][]string{}
	for rows.Next() {
		var projectID, issueTypeID, fieldID string
		if err = rows.Scan(&projectID, &issueTypeID, &fieldID); err != nil {
			return nil, err
		}
		if resolved[projectID] == nil {
			resolved[projectID] = map[string][]string{}
		}
		resolved[projectID][issueTypeID] = append(resolved[projectID][issueTypeID], fieldID)
	}
	return resolved, rows.Err()
}
