package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/e6qu/zzira/internal/models"
)

// IssueTypes lists the issue type registry used by create metadata.
func (s *Store) IssueTypes(ctx context.Context) ([]models.IssueType, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id, name, COALESCE(icon,'') FROM issue_types ORDER BY name, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.IssueType
	for rows.Next() {
		var issueType models.IssueType
		if err := rows.Scan(&issueType.ID, &issueType.Name, &issueType.Icon); err != nil {
			return nil, err
		}
		out = append(out, issueType)
	}
	return out, rows.Err()
}

// ProjectByIDOrKey resolves a project without allowing it to escape the
// caller's workspace.
func (s *Store) ProjectByIDOrKey(ctx context.Context, workspaceID, idOrKey string) (*models.Project, error) {
	project := &models.Project{}
	err := s.Pool.QueryRow(ctx, `
		SELECT id, workspace_id, key, name, COALESCE(workflow_id,''), COALESCE(security_scheme_id,'')
		FROM projects
		WHERE workspace_id=$1 AND (id=$2 OR upper(key)=upper($2))`, workspaceID, idOrKey).
		Scan(&project.ID, &project.WorkspaceID, &project.Key, &project.Name, &project.WorkflowID, &project.SecuritySchemeID)
	return project, err
}

// IssueCreateMetadata builds the sole schema used by create UI and API
// metadata responses. Keeping this at the store boundary prevents either edge
// from inventing a field the command path cannot persist.
func (s *Store) IssueCreateMetadata(ctx context.Context, workspaceID, userID string) (*models.IssueCreateMetadata, error) {
	projects, err := s.ProjectsByWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	issueTypes, err := s.IssueTypes(ctx)
	if err != nil {
		return nil, err
	}
	priorities, err := s.Priorities(ctx)
	if err != nil {
		return nil, err
	}
	members, err := s.MembersByWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	admin, err := s.IsAdmin(ctx, workspaceID, userID)
	if err != nil {
		return nil, err
	}

	projectOptions := make([]models.CreateFieldOption, 0, len(projects))
	for _, project := range projects {
		projectOptions = append(projectOptions, models.CreateFieldOption{ID: project.ID, Key: project.Key, Name: project.Name})
	}
	typeOptions := make([]models.CreateFieldOption, 0, len(issueTypes))
	for _, issueType := range issueTypes {
		typeOptions = append(typeOptions, models.CreateFieldOption{ID: issueType.ID, Name: issueType.Name})
	}
	priorityOptions := make([]models.CreateFieldOption, 0, len(priorities))
	for _, priority := range priorities {
		priorityOptions = append(priorityOptions, models.CreateFieldOption{ID: priority.ID, Name: priority.Name})
	}
	memberOptions := make([]models.CreateFieldOption, 0, len(members))
	for _, member := range members {
		memberOptions = append(memberOptions, models.CreateFieldOption{ID: member.ID, Name: member.DisplayName})
	}

	meta := &models.IssueCreateMetadata{Projects: make([]models.CreateProjectMeta, 0, len(projects))}
	for _, project := range projects {
		fields := []models.CreateFieldMeta{
			{ID: "project", Name: "Project", Type: "project", Required: true, Section: "context", Options: projectOptions},
			{ID: "issuetype", Name: "Issue type", Type: "issuetype", Required: true, Section: "context", Options: typeOptions},
			{ID: "summary", Name: "Summary", Type: "string", Required: true, Section: "primary"},
			{ID: "description", Name: "Description", Type: "doc", Section: "primary"},
			{ID: "assignee", Name: "Assignee", Type: "user", Section: "details", Options: memberOptions},
			{ID: "priority", Name: "Priority", Type: "priority", Section: "details", Options: priorityOptions},
			{ID: "labels", Name: "Labels", Type: "array", Description: "Separate labels with commas. Spaces are not allowed inside a label.", Section: "details"},
		}

		scheme, err := s.SecuritySchemeForProject(ctx, project.ID)
		if err != nil {
			return nil, err
		}
		if scheme != nil {
			options := make([]models.CreateFieldOption, 0, len(scheme.Levels))
			for _, level := range scheme.Levels {
				if admin || containsString(level.Members, userID) {
					options = append(options, models.CreateFieldOption{ID: level.ID, Name: level.Name})
				}
			}
			if len(options) > 0 {
				fields = append(fields, models.CreateFieldMeta{ID: "security", Name: "Restrict to", Type: "securitylevel", Section: "details", Options: options})
			}
		}

		customFields, err := s.CustomFieldsForProject(ctx, project.ID)
		if err != nil {
			return nil, err
		}
		for _, field := range customFields {
			fieldType, err := createFieldType(field.Type)
			if err != nil {
				return nil, fmt.Errorf("custom field %q: %w", field.ID, err)
			}
			fields = append(fields, models.CreateFieldMeta{
				ID: field.ID, Name: field.Name, Type: fieldType, Description: field.Description,
				Custom: true, Section: "details",
			})
		}
		meta.Projects = append(meta.Projects, models.CreateProjectMeta{Project: *project, IssueTypes: issueTypes, Fields: fields})
	}
	return meta, nil
}

func createFieldType(fieldType string) (string, error) {
	switch strings.ToLower(fieldType) {
	case models.CustomFieldText:
		return "string", nil
	case models.CustomFieldNumber:
		return "number", nil
	case models.CustomFieldDatetime:
		return "datetime", nil
	default:
		return "", fmt.Errorf("unsupported type %q", fieldType)
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
