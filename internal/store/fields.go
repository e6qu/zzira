package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

// ErrFieldValidation is returned when a field request is refused on its own
// terms rather than because something is missing.
var ErrFieldValidation = errors.New("invalid field")

// FieldPage is one page of Jira's paginated field searches.
type FieldPage struct {
	Fields []*models.CustomField
	Total  int
}

// FieldSearch narrows one of Jira's two paginated field searches.
type FieldSearch struct {
	Query      string
	Types      []string
	IDs        []string
	ProjectIDs []string
	OrderBy    string
	Trashed    bool
	StartAt    int
	MaxResults int
}

// SearchCustomFields serves both `GET /field/search` and its trashed
// counterpart; which one is chosen by Trashed, because the two differ only in
// the state they look at.
func (s *Store) SearchCustomFields(ctx context.Context, workspaceID string, search FieldSearch) (*FieldPage, error) {
	where := []string{"cf.active", "(cf.workspace_id IS NULL OR cf.workspace_id=$1)",
		"(cf.app_installation_id IS NULL OR ai.status='active')"}
	args := []any{workspaceID}
	if search.Trashed {
		where = append(where, "cf.trashed_at IS NOT NULL")
	} else {
		where = append(where, "cf.trashed_at IS NULL")
	}
	add := func(clause string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf(clause, len(args)))
	}
	if search.Query != "" {
		add("(cf.name ILIKE '%%' || $%d || '%%' OR cf.description ILIKE '%%' || $%[1]d || '%%')", search.Query)
	}
	if len(search.Types) > 0 {
		add("cf.type = ANY($%d)", search.Types)
	}
	if len(search.IDs) > 0 {
		add("cf.id = ANY($%d)", search.IDs)
	}
	if len(search.ProjectIDs) > 0 {
		add(`EXISTS (SELECT 1 FROM custom_field_contexts ctx
			LEFT JOIN custom_field_context_projects ctxp ON ctxp.context_id=ctx.id
			WHERE ctx.field_id=cf.id AND (ctx.all_projects OR ctxp.project_id = ANY($%d)))`, search.ProjectIDs)
	}
	// Jira's orderBy accepts a leading - for descending; only the documented
	// sortable columns are honored, and anything else falls back to the id.
	direction := "ASC"
	column := strings.TrimPrefix(search.OrderBy, "-")
	if strings.HasPrefix(search.OrderBy, "-") {
		direction = "DESC"
	}
	order := "cf.id"
	switch column {
	case "name":
		order = "lower(cf.name)"
	case "description":
		order = "lower(cf.description)"
	}
	predicate := strings.Join(where, " AND ")
	page := &FieldPage{Fields: []*models.CustomField{}}
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM custom_fields cf
		LEFT JOIN app_installations ai ON ai.id=cf.app_installation_id WHERE `+predicate, args...).Scan(&page.Total); err != nil {
		return nil, err
	}
	args = append(args, search.MaxResults, search.StartAt)
	rows, err := s.Pool.Query(ctx, customFieldSelect+"WHERE "+predicate+
		fmt.Sprintf(" ORDER BY %s %s, cf.id LIMIT $%d OFFSET $%d", order, direction, len(args)-1, len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	fields, err := scanCustomFields(rows)
	if err != nil {
		return nil, err
	}
	if fields != nil {
		page.Fields = fields
	}
	return page, nil
}

// CustomFieldByID reads one field in any state, so the trash operations can
// tell "already trashed" from "does not exist".
func (s *Store) CustomFieldByID(ctx context.Context, workspaceID, fieldID string) (*models.CustomField, error) {
	rows, err := s.Pool.Query(ctx, customFieldSelect+
		`WHERE cf.id=$1 AND (cf.workspace_id IS NULL OR cf.workspace_id=$2)`, fieldID, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	fields, err := scanCustomFields(rows)
	if err != nil {
		return nil, err
	}
	if len(fields) == 0 {
		return nil, pgx.ErrNoRows
	}
	return fields[0], nil
}

// UpdateCustomField renames or re-describes a field. An app's field belongs to
// its module descriptor, so it is not editable here.
func (s *Store) UpdateCustomField(ctx context.Context, workspaceID, fieldID string, name, description, searcherKey *string) (*models.CustomField, error) {
	field, err := s.CustomFieldByID(ctx, workspaceID, fieldID)
	if err != nil {
		return nil, err
	}
	if field.AppKey != "" {
		return nil, fmt.Errorf("%w: an app's field is defined by its module", ErrFieldValidation)
	}
	if name != nil {
		trimmed := strings.TrimSpace(*name)
		if trimmed == "" || len(trimmed) > 255 {
			return nil, fmt.Errorf("%w: a field name of 1 to 255 characters is required", ErrFieldValidation)
		}
		field.Name = trimmed
	}
	if description != nil {
		if len(*description) > 40000 {
			return nil, fmt.Errorf("%w: the description is limited to 40000 characters", ErrFieldValidation)
		}
		field.Description = *description
	}
	if searcherKey != nil {
		field.SearcherKey = *searcherKey
	}
	if _, err = s.Pool.Exec(ctx, `UPDATE custom_fields SET name=$2,description=$3,searcher_key=$4 WHERE id=$1`,
		fieldID, field.Name, field.Description, field.SearcherKey); err != nil {
		return nil, err
	}
	return field, nil
}

// SetCustomFieldTrashed moves a field into or out of the trash. A trashed field
// keeps its contexts, options and the values already on work items; it simply
// stops reaching forms, screens and metadata until it is restored.
func (s *Store) SetCustomFieldTrashed(ctx context.Context, workspaceID, fieldID string, trashed bool) error {
	field, err := s.CustomFieldByID(ctx, workspaceID, fieldID)
	if err != nil {
		return err
	}
	if field.AppKey != "" {
		return fmt.Errorf("%w: an app's field is removed with the app", ErrFieldValidation)
	}
	if field.Trashed == trashed {
		if trashed {
			return fmt.Errorf("%w: the field is already in the trash", ErrFieldValidation)
		}
		return fmt.Errorf("%w: the field is not in the trash", ErrFieldValidation)
	}
	if trashed {
		_, err = s.Pool.Exec(ctx, `UPDATE custom_fields SET trashed_at=now() WHERE id=$1`, fieldID)
	} else {
		_, err = s.Pool.Exec(ctx, `UPDATE custom_fields SET trashed_at=NULL WHERE id=$1`, fieldID)
	}
	return err
}

// DeleteCustomField removes a field permanently. Jira requires it to be in the
// trash first, which is what keeps a single mistaken call from destroying the
// values recorded against it.
func (s *Store) DeleteCustomField(ctx context.Context, workspaceID, fieldID string) error {
	field, err := s.CustomFieldByID(ctx, workspaceID, fieldID)
	if err != nil {
		return err
	}
	if field.AppKey != "" {
		return fmt.Errorf("%w: an app's field is removed with the app", ErrFieldValidation)
	}
	if !field.Trashed {
		return fmt.Errorf("%w: the field must be in the trash before it is deleted", ErrFieldValidation)
	}
	_, err = s.Pool.Exec(ctx, `DELETE FROM custom_fields WHERE id=$1`, fieldID)
	return err
}

// FieldProjectIDs reports the projects a field reaches through its contexts. A
// context covering all projects reaches every project in the workspace, which
// is why the global case is answered from the project table.
func (s *Store) FieldProjectIDs(ctx context.Context, workspaceID, fieldID string) ([]string, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT p.id FROM projects p
		WHERE p.workspace_id=$1 AND EXISTS (
			SELECT 1 FROM custom_field_contexts ctx
			LEFT JOIN custom_field_context_projects ctxp ON ctxp.context_id=ctx.id
			WHERE ctx.field_id=$2 AND (ctx.all_projects OR ctxp.project_id=p.id))
		ORDER BY p.id`, workspaceID, fieldID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ProjectFieldAssociation is one row of `GET /projects/fields`: a field that
// applies to a project and work type, and whether the form requires it.
type ProjectFieldAssociation struct {
	FieldID     string
	Description string
	ProjectID   string
	WorkTypeID  string
	Required    bool
}

// ProjectFieldAssociations reports which fields apply to which project and work
// type. It resolves through the same context and field-configuration rules the
// create dialog uses, so the answer cannot drift from the form.
func (s *Store) ProjectFieldAssociations(ctx context.Context, workspaceID string, projectIDs, workTypeIDs, fieldIDs []string) ([]ProjectFieldAssociation, error) {
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
				AND item.issue_type_id IN (work_type.id, $5)
			ORDER BY assigned.project_id, work_type.id, (item.issue_type_id = work_type.id) DESC
		)
		SELECT f.id, COALESCE(f.description,''), p.id, work_type.id,
		       COALESCE(rule.is_required, FALSE)
		FROM projects p
		CROSS JOIN issue_types work_type
		JOIN custom_fields f
			ON f.active AND f.trashed_at IS NULL
			AND (f.workspace_id IS NULL OR f.workspace_id=p.workspace_id)
			AND jira_custom_field_context(f.id,p.id,work_type.id) IS NOT NULL
		LEFT JOIN chosen ON chosen.project_id=p.id AND chosen.issue_type_id=work_type.id
		LEFT JOIN field_configuration_items rule
			ON rule.configuration_id=chosen.configuration_id AND rule.field_id=f.id
		WHERE p.workspace_id=$1 AND p.lifecycle_state='ACTIVE'
		  AND COALESCE(rule.is_hidden, FALSE) = FALSE
		  AND ($2::text[] IS NULL OR p.id = ANY($2))
		  AND ($3::text[] IS NULL OR work_type.id = ANY($3))
		  AND ($4::text[] IS NULL OR f.id = ANY($4))
		ORDER BY p.id, work_type.id, f.id`,
		workspaceID, nullableIDs(projectIDs), nullableIDs(workTypeIDs), nullableIDs(fieldIDs),
		DefaultIssueTypeMapping)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProjectFieldAssociation{}
	for rows.Next() {
		var row ProjectFieldAssociation
		if err = rows.Scan(&row.FieldID, &row.Description, &row.ProjectID, &row.WorkTypeID, &row.Required); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// nullableIDs turns an absent filter into SQL NULL, so one query serves both
// the filtered and the unfiltered read.
func nullableIDs(ids []string) any {
	if len(ids) == 0 {
		return nil
	}
	return ids
}

// FieldUsage is what Jira's field search reports about where a field is used.
type FieldUsage struct {
	Contexts int
	Projects int
	Screens  int
}

// FieldUsageCounts answers the counts for a page of fields in one query, rather
// than per field.
func (s *Store) FieldUsageCounts(ctx context.Context, workspaceID string, fieldIDs []string) (map[string]FieldUsage, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT f.id,
			(SELECT count(*) FROM custom_field_contexts c WHERE c.field_id=f.id),
			(SELECT count(*) FROM projects p WHERE p.workspace_id=$1 AND EXISTS (
				SELECT 1 FROM custom_field_contexts c
				LEFT JOIN custom_field_context_projects cp ON cp.context_id=c.id
				WHERE c.field_id=f.id AND (c.all_projects OR cp.project_id=p.id))),
			(SELECT count(*) FROM screen_tab_fields sf WHERE sf.workspace_id=$1 AND sf.field_id=f.id)
		FROM custom_fields f WHERE f.id = ANY($2)`, workspaceID, fieldIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	usage := map[string]FieldUsage{}
	for rows.Next() {
		var id string
		var counts FieldUsage
		if err = rows.Scan(&id, &counts.Contexts, &counts.Projects, &counts.Screens); err != nil {
			return nil, err
		}
		usage[id] = counts
	}
	return usage, rows.Err()
}
