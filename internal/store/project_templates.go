package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/e6qu/zzira/internal/models"
)

var (
	ErrProjectTemplateValidation = errors.New("invalid project template")
	ErrProjectTemplateNotFound   = errors.New("project template not found")
)

// ProjectTemplateConfiguration is the configuration a template carries: the
// project type, board and the schemes a project created from it uses. Scheme
// ids are Jira's ids.
type ProjectTemplateConfiguration struct {
	ProjectTypeKey             string `json:"projectTypeKey"`
	BoardType                  string `json:"boardType,omitempty"`
	AssigneeType               string `json:"assigneeType,omitempty"`
	PermissionSchemeID         *int64 `json:"permissionSchemeId,omitempty"`
	NotificationSchemeID       *int64 `json:"notificationSchemeId,omitempty"`
	IssueTypeSchemeID          *int64 `json:"issueTypeSchemeId,omitempty"`
	IssueTypeScreenSchemeID    *int64 `json:"issueTypeScreenSchemeId,omitempty"`
	FieldConfigurationSchemeID *int64 `json:"fieldLayoutSchemeId,omitempty"`
	PrioritySchemeID           *int64 `json:"prioritySchemeId,omitempty"`
	WorkflowSchemeID           *int64 `json:"workflowSchemeId,omitempty"`
	IssueSecuritySchemeID      *int64 `json:"issueSecuritySchemeId,omitempty"`
}

type ProjectTemplate struct {
	UUID, Key, Name, Description, Type, ProjectID string
	Snapshot                                      ProjectTemplateConfiguration
	GenerationOptions                             json.RawMessage
	UpdatedAt                                     time.Time
}

func nullableID(value *int64, raw *string) *int64 {
	if raw == nil || *raw == "" {
		return value
	}
	parsed, err := strconv.ParseInt(*raw, 10, 64)
	if err != nil {
		return nil
	}
	return &parsed
}

// ProjectConfiguration reads the configuration a template records for a
// project.
func (s *Store) ProjectConfiguration(ctx context.Context, workspaceID, projectID string) (ProjectTemplateConfiguration, error) {
	var configuration ProjectTemplateConfiguration
	var workflowScheme, securityScheme *string
	err := s.Pool.QueryRow(ctx, `SELECT p.project_type_key,COALESCE((SELECT b.type FROM boards b WHERE b.project_id=p.id ORDER BY b.created_at,b.id LIMIT 1),''),p.assignee_type,
		(SELECT scheme_id FROM project_permission_schemes WHERE project_id=p.id),
		(SELECT scheme_id FROM project_notification_schemes WHERE project_id=p.id),
		(SELECT scheme_id FROM project_issue_type_schemes WHERE project_id=p.id),
		(SELECT scheme_id FROM project_issue_type_screen_schemes WHERE project_id=p.id),
		(SELECT scheme_id FROM project_field_configuration_schemes WHERE project_id=p.id),
		(SELECT scheme_id FROM project_priority_schemes WHERE project_id=p.id),
		(SELECT ws.jira_id::text FROM workflow_schemes ws WHERE ws.id=p.workflow_scheme_id),
		p.security_scheme_id
		FROM projects p WHERE p.workspace_id=$1 AND p.id=$2 AND p.trashed_at IS NULL`, workspaceID, projectID).Scan(
		&configuration.ProjectTypeKey, &configuration.BoardType, &configuration.AssigneeType, &configuration.PermissionSchemeID, &configuration.NotificationSchemeID,
		&configuration.IssueTypeSchemeID, &configuration.IssueTypeScreenSchemeID, &configuration.FieldConfigurationSchemeID, &configuration.PrioritySchemeID,
		&workflowScheme, &securityScheme)
	if errors.Is(err, pgx.ErrNoRows) {
		return configuration, ErrProjectTemplateNotFound
	}
	configuration.WorkflowSchemeID = nullableID(nil, workflowScheme)
	configuration.IssueSecuritySchemeID = nullableID(nil, securityScheme)
	return configuration, err
}

func validProjectTemplateText(name, description string) (string, string, error) {
	name, description = strings.TrimSpace(name), strings.TrimSpace(description)
	if name == "" || len([]rune(name)) > 50 {
		return "", "", fmt.Errorf("%w: The template name must be between 1 and 50 characters.", ErrProjectTemplateValidation)
	}
	if len([]rune(description)) > 150 {
		return "", "", fmt.Errorf("%w: The template description must be at most 150 characters.", ErrProjectTemplateValidation)
	}
	return name, description, nil
}

func uniqueTemplateError(err error) error {
	if err != nil && strings.Contains(err.Error(), "project_templates_name") {
		return fmt.Errorf("%w: A template with this name already exists.", ErrProjectTemplateValidation)
	}
	return err
}

// SaveProjectTemplate saves a LIVE or SNAPSHOT template from a project.
func (s *Store) SaveProjectTemplate(ctx context.Context, workspaceID, actorID, projectID, templateType, name, description string, options json.RawMessage) (ProjectTemplate, error) {
	name, description, err := validProjectTemplateText(name, description)
	if err != nil {
		return ProjectTemplate{}, err
	}
	if templateType != "LIVE" && templateType != "SNAPSHOT" {
		return ProjectTemplate{}, fmt.Errorf("%w: The template type must be LIVE or SNAPSHOT.", ErrProjectTemplateValidation)
	}
	configuration, err := s.ProjectConfiguration(ctx, workspaceID, projectID)
	if errors.Is(err, ErrProjectTemplateNotFound) {
		return ProjectTemplate{}, fmt.Errorf("%w: The project %s does not exist.", ErrProjectTemplateValidation, projectID)
	}
	if err != nil {
		return ProjectTemplate{}, err
	}
	snapshot, err := json.Marshal(configuration)
	if err != nil {
		return ProjectTemplate{}, err
	}
	if len(options) == 0 || string(options) == "null" {
		options = json.RawMessage(`{}`)
	}
	var linked any
	if templateType == "LIVE" {
		linked = projectID
	}
	var template ProjectTemplate
	err = s.Pool.QueryRow(ctx, `INSERT INTO project_templates(workspace_id,template_key,name,description,type,project_id,snapshot,generation_options,created_by)
		VALUES($1,'',$2,$3,$4,$5,$6,$7,$8) RETURNING uuid::text`, workspaceID, name, description, templateType, linked, snapshot, []byte(options), actorID).Scan(&template.UUID)
	if err != nil {
		return ProjectTemplate{}, uniqueTemplateError(err)
	}
	key := "com.atlassian.jira.custom-template:" + template.UUID
	if _, err := s.Pool.Exec(ctx, `UPDATE project_templates SET template_key=$2 WHERE uuid::text=$1`, template.UUID, key); err != nil {
		return ProjectTemplate{}, err
	}
	return s.ProjectTemplate(ctx, workspaceID, key, "")
}

// ProjectTemplate finds a template by its key or, failing that, the LIVE
// template of a project. A LIVE template reports its project's current
// configuration.
func (s *Store) ProjectTemplate(ctx context.Context, workspaceID, templateKey, projectID string) (ProjectTemplate, error) {
	var template ProjectTemplate
	var snapshot []byte
	var project *string
	query := `SELECT uuid::text,template_key,name,description,type,project_id,snapshot,generation_options,updated_at FROM project_templates WHERE workspace_id=$1 AND template_key=$2`
	arg := templateKey
	if templateKey == "" {
		query = `SELECT uuid::text,template_key,name,description,type,project_id,snapshot,generation_options,updated_at FROM project_templates WHERE workspace_id=$1 AND project_id=$2 AND type='LIVE' ORDER BY created_at DESC LIMIT 1`
		arg = projectID
	}
	err := s.Pool.QueryRow(ctx, query, workspaceID, arg).Scan(&template.UUID, &template.Key, &template.Name, &template.Description, &template.Type, &project, &snapshot, &template.GenerationOptions, &template.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProjectTemplate{}, ErrProjectTemplateNotFound
	}
	if err != nil {
		return ProjectTemplate{}, err
	}
	if project != nil {
		template.ProjectID = *project
	}
	if err := json.Unmarshal(snapshot, &template.Snapshot); err != nil {
		return ProjectTemplate{}, err
	}
	if template.Type == "LIVE" && template.ProjectID != "" {
		if live, err := s.ProjectConfiguration(ctx, workspaceID, template.ProjectID); err == nil {
			template.Snapshot = live
		}
	}
	return template, nil
}

func (s *Store) EditProjectTemplate(ctx context.Context, workspaceID, templateKey string, name, description *string, options json.RawMessage) error {
	current, err := s.ProjectTemplate(ctx, workspaceID, templateKey, "")
	if err != nil {
		return err
	}
	if name == nil {
		name = &current.Name
	}
	if description == nil {
		description = &current.Description
	}
	cleanName, cleanDescription, err := validProjectTemplateText(*name, *description)
	if err != nil {
		return err
	}
	if len(options) == 0 || string(options) == "null" {
		options = current.GenerationOptions
	}
	_, err = s.Pool.Exec(ctx, `UPDATE project_templates SET name=$3,description=$4,generation_options=$5,updated_at=now() WHERE workspace_id=$1 AND template_key=$2`,
		workspaceID, templateKey, cleanName, cleanDescription, []byte(options))
	return uniqueTemplateError(err)
}

func (s *Store) RemoveProjectTemplate(ctx context.Context, workspaceID, templateKey string) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM project_templates WHERE workspace_id=$1 AND template_key=$2`, workspaceID, templateKey)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrProjectTemplateNotFound
	}
	return err
}

// ValidateProjectConfiguration checks that every scheme a configuration names
// exists on the site.
func (s *Store) ValidateProjectConfiguration(ctx context.Context, workspaceID string, configuration ProjectTemplateConfiguration) error {
	checks := []struct {
		id    *int64
		name  string
		query string
	}{
		{configuration.PermissionSchemeID, "permission scheme", `SELECT 1 FROM permission_schemes WHERE workspace_id=$1 AND id=$2`},
		{configuration.NotificationSchemeID, "notification scheme", `SELECT 1 FROM notification_schemes WHERE workspace_id=$1 AND id=$2`},
		{configuration.IssueTypeSchemeID, "issue type scheme", `SELECT 1 FROM issue_type_schemes WHERE workspace_id=$1 AND id=$2`},
		{configuration.IssueTypeScreenSchemeID, "issue type screen scheme", `SELECT 1 FROM issue_type_screen_schemes WHERE workspace_id=$1 AND id=$2`},
		{configuration.FieldConfigurationSchemeID, "field layout scheme", `SELECT 1 FROM field_configuration_schemes WHERE workspace_id=$1 AND id=$2`},
		{configuration.WorkflowSchemeID, "workflow scheme", `SELECT 1 FROM workflow_schemes WHERE workspace_id=$1 AND jira_id=$2`},
		{configuration.IssueSecuritySchemeID, "issue security scheme", `SELECT 1 FROM security_schemes WHERE workspace_id=$1 AND id=$2::bigint::text`},
	}
	for _, check := range checks {
		if check.id == nil {
			continue
		}
		var found bool
		if err := s.Pool.QueryRow(ctx, `SELECT EXISTS(`+check.query+`)`, workspaceID, *check.id).Scan(&found); err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("%w: The %s %d does not exist.", ErrProjectTemplateValidation, check.name, *check.id)
		}
	}
	return nil
}

// ---- creating a project from a custom template ----

const apiTaskCreateProjectFromTemplate = "create-project-from-template"

type createProjectFromTemplatePayload struct {
	Project       models.Project               `json:"project"`
	BoardType     string                       `json:"boardType"`
	Configuration ProjectTemplateConfiguration `json:"configuration"`
}

// EnqueueProjectFromTemplate queues the creation of a validated project and
// the application of its template configuration.
func (s *Store) EnqueueProjectFromTemplate(ctx context.Context, workspaceID, actorID string, project models.Project, boardType string, configuration ProjectTemplateConfiguration) (APITask, error) {
	task, err := queuedAPITask(workspaceID, actorID, "Creating project "+project.Key+" from a custom template", apiTaskCreateProjectFromTemplate, createProjectFromTemplatePayload{Project: project, BoardType: boardType, Configuration: configuration})
	if err != nil {
		return APITask{}, err
	}
	return task, s.enqueueAPITask(ctx, &task)
}

func (s *Store) executeProjectFromTemplate(ctx context.Context, task APITask) error {
	var payload createProjectFromTemplatePayload
	if err := json.Unmarshal(task.Payload, &payload); err != nil {
		return fmt.Errorf("decode project creation: %w", err)
	}
	// A project's workspace is not part of its JSON; the task carries it.
	payload.Project.WorkspaceID = task.WorkspaceID
	project, err := s.CreateProject(ctx, task.SubmittedBy, payload.Project, payload.BoardType)
	if err != nil {
		return err
	}
	configuration := payload.Configuration
	if err := s.UpdateAPITaskProgress(ctx, task, 50, "Created project "+project.Key+"; applying the template configuration."); err != nil {
		return err
	}
	if configuration.PermissionSchemeID != nil {
		if _, _, err := s.AssignPermissionScheme(ctx, task.WorkspaceID, task.SubmittedBy, project.ID, *configuration.PermissionSchemeID); err != nil {
			return fmt.Errorf("assign permission scheme: %w", err)
		}
	}
	if configuration.NotificationSchemeID != nil {
		if err := s.AssignNotificationScheme(ctx, task.WorkspaceID, task.SubmittedBy, project.ID, *configuration.NotificationSchemeID); err != nil {
			return fmt.Errorf("assign notification scheme: %w", err)
		}
	}
	if configuration.IssueTypeSchemeID != nil {
		if err := s.AssignIssueTypeScheme(ctx, task.WorkspaceID, strconv.FormatInt(*configuration.IssueTypeSchemeID, 10), project.ID); err != nil {
			return fmt.Errorf("assign issue type scheme: %w", err)
		}
	}
	if configuration.IssueTypeScreenSchemeID != nil {
		if err := s.AssignIssueTypeScreenScheme(ctx, task.WorkspaceID, task.SubmittedBy, project.ID, strconv.FormatInt(*configuration.IssueTypeScreenSchemeID, 10)); err != nil {
			return fmt.Errorf("assign issue type screen scheme: %w", err)
		}
	}
	if configuration.FieldConfigurationSchemeID != nil {
		if err := s.AssignFieldConfigurationScheme(ctx, task.WorkspaceID, task.SubmittedBy, project.ID, strconv.FormatInt(*configuration.FieldConfigurationSchemeID, 10)); err != nil {
			return fmt.Errorf("assign field configuration scheme: %w", err)
		}
	}
	if configuration.WorkflowSchemeID != nil {
		var schemeID string
		if err := s.Pool.QueryRow(ctx, `SELECT id FROM workflow_schemes WHERE workspace_id=$1 AND jira_id=$2`, task.WorkspaceID, *configuration.WorkflowSchemeID).Scan(&schemeID); err != nil {
			return fmt.Errorf("find workflow scheme: %w", err)
		}
		if err := s.AssignWorkflowScheme(ctx, task.WorkspaceID, task.SubmittedBy, project.ID, schemeID); err != nil {
			return fmt.Errorf("assign workflow scheme: %w", err)
		}
	}
	if configuration.IssueSecuritySchemeID != nil {
		if err := s.AssignSecurityScheme(ctx, project.ID, strconv.FormatInt(*configuration.IssueSecuritySchemeID, 10)); err != nil {
			return fmt.Errorf("assign issue security scheme: %w", err)
		}
	}
	projectID, _ := strconv.ParseInt(project.ID, 10, 64)
	return s.CompleteAPITask(ctx, task, "Created project "+project.Key+".", map[string]any{"projectId": projectID, "projectKey": project.Key})
}
