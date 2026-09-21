package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// ProjectConfigurationEntry names one piece of a project's configuration and
// where it is changed, so a project's administrators can read the whole
// configuration in one place instead of opening each directory in turn.
type ProjectConfigurationEntry struct {
	Label       string
	Name        string
	Description string
	// Href is the page that changes this setting. It is empty when the
	// setting has no page of its own.
	Href string
	// SiteDefault reports that no scheme is assigned and the site default
	// answers for the project.
	SiteDefault bool
}

// ProjectConfiguration schemes a project has no association row for fall back
// to the site default, which is what each subsystem does when it resolves one.
const projectConfigurationSchemesQuery = `
SELECT
  COALESCE((SELECT s.name FROM issue_type_schemes s WHERE s.id=(SELECT scheme_id FROM project_issue_type_schemes WHERE project_id=$2)),''),
  COALESCE((SELECT s.name FROM issue_type_schemes s WHERE s.workspace_id=$1 AND s.is_default ORDER BY s.id LIMIT 1),''),
  COALESCE((SELECT s.name FROM issue_type_screen_schemes s WHERE s.id=(SELECT scheme_id FROM project_issue_type_screen_schemes WHERE project_id=$2)),''),
  COALESCE((SELECT s.name FROM issue_type_screen_schemes s WHERE s.workspace_id=$1 AND s.is_default ORDER BY s.id LIMIT 1),''),
  COALESCE((SELECT s.name FROM field_configuration_schemes s WHERE s.id=(SELECT scheme_id FROM project_field_configuration_schemes WHERE project_id=$2)),''),
  COALESCE((SELECT s.name FROM field_configuration_schemes s WHERE s.workspace_id=$1 AND s.is_default ORDER BY s.id LIMIT 1),''),
  COALESCE((SELECT s.name FROM priority_schemes s WHERE s.id=(SELECT scheme_id FROM project_priority_schemes WHERE project_id=$2)),''),
  COALESCE((SELECT s.name FROM priority_schemes s WHERE s.workspace_id=$1 AND s.is_default ORDER BY s.id LIMIT 1),'')`

// ProjectConfigurationSummary reads every scheme a project routes through,
// naming the site default where the project has no scheme of its own.
func (s *Store) ProjectConfigurationSummary(ctx context.Context, workspaceID, projectID string) ([]ProjectConfigurationEntry, error) {
	entries := make([]ProjectConfigurationEntry, 0, 8)
	project, err := s.ProjectByIDOrKey(ctx, workspaceID, projectID)
	if err != nil {
		return nil, err
	}
	settings := "/projects/" + project.Key + "/settings"

	permission, _, err := s.AssignedPermissionScheme(ctx, workspaceID, project.ID, false)
	if err != nil {
		return nil, err
	}
	if permission != nil {
		entries = append(entries, ProjectConfigurationEntry{Label: "Permission scheme", Name: permission.Name, Description: permission.Description, Href: settings + "/permissions"})
	}
	notification, _, err := s.AssignedNotificationScheme(ctx, workspaceID, project.ID, false)
	if err != nil {
		return nil, err
	}
	if notification != nil {
		entries = append(entries, ProjectConfigurationEntry{Label: "Notification scheme", Name: notification.Name, Description: notification.Description, Href: settings + "/notifications"})
	}
	security, _, err := s.AssignedIssueSecurityScheme(ctx, workspaceID, project.ID, false)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	securityEntry := ProjectConfigurationEntry{Label: "Issue security scheme", Name: "None", Description: "Every work item in the project is visible to anyone who can browse it.", Href: settings + "/issue-security"}
	if security != nil {
		securityEntry.Name, securityEntry.Description = security.Name, ""
	}
	entries = append(entries, securityEntry)

	// A project without a workflow scheme routes every work type through its
	// own workflow, so the column is null rather than missing.
	var schemeID *string
	if err := s.Pool.QueryRow(ctx, `SELECT workflow_scheme_id FROM projects WHERE id=$1 AND workspace_id=$2`, project.ID, workspaceID).Scan(&schemeID); err != nil {
		return nil, err
	}
	if schemeID != nil && *schemeID != "" {
		scheme, err := s.WorkflowSchemeByID(ctx, workspaceID, *schemeID, false)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		if err == nil {
			entries = append(entries, ProjectConfigurationEntry{Label: "Workflow scheme", Name: scheme.Name, Description: scheme.Description, Href: "/settings/workflow-schemes/" + scheme.ID})
		}
	}
	workflow, err := s.WorkflowForProject(ctx, project.ID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if err == nil {
		entries = append(entries, ProjectConfigurationEntry{Label: "Workflow", Name: workflow.Name, Href: "/settings/workflows/" + workflow.ID})
	}

	var names [8]string
	if err := s.Pool.QueryRow(ctx, projectConfigurationSchemesQuery, workspaceID, project.ID).Scan(
		&names[0], &names[1], &names[2], &names[3], &names[4], &names[5], &names[6], &names[7]); err != nil {
		return nil, err
	}
	for index, entry := range []ProjectConfigurationEntry{
		{Label: "Work type scheme", Href: "/settings/work-types"},
		{Label: "Work type screen scheme", Href: "/settings/screen-schemes"},
		{Label: "Field configuration scheme", Href: "/settings/field-configurations"},
		{Label: "Priority scheme", Href: "/settings/priorities"},
	} {
		entry.Name = names[index*2]
		if entry.Name == "" {
			entry.Name, entry.SiteDefault = names[index*2+1], true
		}
		if entry.Name == "" {
			continue
		}
		entries = append(entries, entry)
	}
	return entries, nil
}
