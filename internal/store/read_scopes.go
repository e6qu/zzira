package store

import (
	"context"
	"strconv"

	"github.com/e6qu/zzira/internal/models"
)

// activeProjectWorkflowIDs lists, for the project aliased p, the published
// workflows its work uses: the workflow scheme's default and type mappings,
// or the project's own workflow. Drafts are not active.
const activeProjectWorkflowIDs = `(SELECT COALESCE(p.workflow_id,'wf_default') UNION
	SELECT ws.default_workflow_id FROM workflow_schemes ws WHERE ws.id=p.workflow_scheme_id UNION
	SELECT mapping.value FROM workflow_schemes ws, jsonb_each_text(ws.issue_type_mappings) mapping WHERE ws.id=p.workflow_scheme_id)`

// StatusesInProjectWorkflows returns the statuses of the active workflows the
// projects use: every status a published workflow lists or transitions
// through. Jira's status reads return only statuses of active workflows.
func (s *Store) StatusesInProjectWorkflows(ctx context.Context, workspaceID string, projectIDs []string) ([]models.Status, error) {
	if len(projectIDs) == 0 {
		return nil, nil
	}
	rows, err := s.Pool.Query(ctx, `
		WITH active AS (
		  SELECT DISTINCT w.id, w.def FROM projects p
		  JOIN LATERAL `+activeProjectWorkflowIDs+` used(id) ON TRUE
		  JOIN workflows w ON w.id=used.id AND (w.workspace_id=p.workspace_id OR w.workspace_id IS NULL)
		  WHERE p.workspace_id=$1 AND p.id=ANY($2)
		), referenced AS (
		  SELECT t->>'to' AS id FROM active, jsonb_array_elements(COALESCE(active.def->'transitions','[]'::jsonb)) t
		  UNION SELECT source FROM active, jsonb_array_elements(COALESCE(active.def->'transitions','[]'::jsonb)) t,
		    jsonb_array_elements_text(COALESCE(t->'from','[]'::jsonb)) source
		  UNION SELECT layout->>'statusReference' FROM active, jsonb_array_elements(COALESCE(active.def->'statuses','[]'::jsonb)) layout
		)
		SELECT st.id,st.name,st.description,st.category,COALESCE(st.project_id,''),st.workspace_id IS NULL,st.jira_id
		FROM statuses st
		WHERE (st.workspace_id IS NULL OR st.workspace_id=$1) AND st.id IN (SELECT id FROM referenced)
		  AND (st.project_id IS NULL OR st.project_id=ANY($2))
		ORDER BY CASE st.category WHEN 'new' THEN 1 WHEN 'indeterminate' THEN 2 ELSE 3 END,lower(st.name),st.id`, workspaceID, projectIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.Status
	for rows.Next() {
		var status models.Status
		if err := rows.Scan(&status.ID, &status.Name, &status.Description, &status.Category, &status.ProjectID, &status.Protected, &status.JiraID); err != nil {
			return nil, err
		}
		out = append(out, status)
	}
	return out, rows.Err()
}

// ScreenUsedByProject reports whether the project's issue type screen scheme,
// or the default one when the project has none, maps a screen scheme that
// uses the screen for any operation.
func (s *Store) ScreenUsedByProject(ctx context.Context, workspaceID, projectID, screenID string) (bool, error) {
	id, err := strconv.ParseInt(screenID, 10, 64)
	if err != nil {
		return false, nil
	}
	var used bool
	err = s.Pool.QueryRow(ctx, `
		SELECT EXISTS(
		  SELECT 1 FROM projects p
		  JOIN issue_type_screen_scheme_items item ON item.scheme_id=COALESCE(
		    (SELECT scheme_id FROM project_issue_type_screen_schemes WHERE project_id=p.id),
		    (SELECT id FROM issue_type_screen_schemes WHERE workspace_id=p.workspace_id AND is_default))
		  JOIN screen_scheme_items screen ON screen.scheme_id=item.screen_scheme_id
		  WHERE p.workspace_id=$1 AND p.id=$2 AND screen.screen_id=$3)`, workspaceID, projectID, id).Scan(&used)
	return used, err
}

// fieldShownInProject is the SQL deciding whether the custom field aliased f
// is shown in the project aliased p: the project's field configuration scheme,
// or the default one, maps no configuration or one that does not hide it.
const fieldShownInProject = `(NOT EXISTS(` + projectFieldConfigurations + `)
	OR EXISTS(SELECT 1 FROM (` + projectFieldConfigurations + `) layout(configuration_id)
	  WHERE NOT EXISTS(SELECT 1 FROM field_configuration_items item
	    WHERE item.configuration_id=layout.configuration_id AND item.field_id=f.id AND item.is_hidden)))`

const projectFieldConfigurations = `SELECT mapping.configuration_id FROM field_configuration_scheme_items mapping
	WHERE mapping.scheme_id=COALESCE(
	  (SELECT scheme_id FROM project_field_configuration_schemes WHERE project_id=p.id),
	  (SELECT id FROM field_configuration_schemes WHERE workspace_id=p.workspace_id AND is_default))`

// CustomFieldOptionVisibleInProjects reports whether an option's field is
// used in one of the projects, through the context holding the option, and
// is not hidden by every field configuration that project applies.
func (s *Store) CustomFieldOptionVisibleInProjects(ctx context.Context, workspaceID, optionID string, projectIDs []string) (bool, error) {
	id, err := strconv.ParseInt(optionID, 10, 64)
	if err != nil || len(projectIDs) == 0 {
		return false, nil
	}
	var visible bool
	err = s.Pool.QueryRow(ctx, `
		SELECT EXISTS(
		  SELECT 1 FROM custom_field_options o
		  JOIN custom_field_contexts c ON c.id=o.context_id
		  JOIN custom_fields f ON f.id=c.field_id AND (f.workspace_id IS NULL OR f.workspace_id=$1)
		  JOIN projects p ON p.workspace_id=$1 AND p.id=ANY($3)
		  WHERE o.id=$2
		    AND (c.all_projects OR EXISTS(SELECT 1 FROM custom_field_context_projects cp WHERE cp.context_id=c.id AND cp.project_id=p.id))
		    AND `+fieldShownInProject+`)`, workspaceID, id, projectIDs).Scan(&visible)
	return visible, err
}

// CustomFieldVisibleInProjects reports whether one of the field's contexts
// applies to one of the projects and a field configuration there shows it.
func (s *Store) CustomFieldVisibleInProjects(ctx context.Context, workspaceID, fieldID string, projectIDs []string) (bool, error) {
	if len(projectIDs) == 0 {
		return false, nil
	}
	var visible bool
	err := s.Pool.QueryRow(ctx, `
		SELECT EXISTS(
		  SELECT 1 FROM custom_field_contexts c
		  JOIN custom_fields f ON f.id=c.field_id AND (f.workspace_id IS NULL OR f.workspace_id=$1)
		  JOIN projects p ON p.workspace_id=$1 AND p.id=ANY($3)
		  WHERE f.id=$2
		    AND (c.all_projects OR EXISTS(SELECT 1 FROM custom_field_context_projects cp WHERE cp.context_id=c.id AND cp.project_id=p.id))
		    AND `+fieldShownInProject+`)`, workspaceID, fieldID, projectIDs).Scan(&visible)
	return visible, err
}

// NamedProjectEntity is a component or version a JQL value names, with the
// project it belongs to.
type NamedProjectEntity struct {
	ID, ProjectID string
}

// ComponentsNamed lists the site's components with a name, any case.
func (s *Store) ComponentsNamed(ctx context.Context, workspaceID, name string) ([]NamedProjectEntity, error) {
	return s.namedProjectEntities(ctx, `SELECT c.id, c.project_id FROM project_components c JOIN projects p ON p.id=c.project_id
		WHERE p.workspace_id=$1 AND lower(c.name)=lower($2) ORDER BY length(c.id), c.id`, workspaceID, name)
}

// VersionsNamed lists the site's versions with a name, any case.
func (s *Store) VersionsNamed(ctx context.Context, workspaceID, name string) ([]NamedProjectEntity, error) {
	return s.namedProjectEntities(ctx, `SELECT v.id, v.project_id FROM project_versions v JOIN projects p ON p.id=v.project_id
		WHERE p.workspace_id=$1 AND lower(v.name)=lower($2) ORDER BY length(v.id), v.id`, workspaceID, name)
}

func (s *Store) namedProjectEntities(ctx context.Context, query, workspaceID, name string) ([]NamedProjectEntity, error) {
	rows, err := s.Pool.Query(ctx, query, workspaceID, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NamedProjectEntity
	for rows.Next() {
		var entity NamedProjectEntity
		if err = rows.Scan(&entity.ID, &entity.ProjectID); err != nil {
			return nil, err
		}
		out = append(out, entity)
	}
	return out, rows.Err()
}
