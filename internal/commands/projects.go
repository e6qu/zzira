package commands

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type ProjectValidationError struct{ Fields map[string]string }

func (e *ProjectValidationError) Error() string { return "Check the project details and try again." }

type CreateProjectInput struct {
	Key                string `json:"key"`
	Name               string `json:"name"`
	Description        string `json:"description"`
	URL                string `json:"url"`
	LeadAccountID      string `json:"leadAccountId"`
	AssigneeType       string `json:"assigneeType"`
	ProjectTypeKey     string `json:"projectTypeKey"`
	ProjectTemplateKey string `json:"projectTemplateKey"`
	CategoryID         int64  `json:"categoryId"`
	// Lead is Jira's deprecated name for leadAccountId.
	Lead     string `json:"lead"`
	AvatarID int64  `json:"avatarId"`
	// The schemes a project starts with, by the ids clients see; zero keeps
	// the site default. fieldConfigurationScheme is Jira's deprecated name for
	// fieldScheme.
	FieldConfigurationScheme int64 `json:"fieldConfigurationScheme"`
	FieldScheme              int64 `json:"fieldScheme"`
	IssueSecurityScheme      int64 `json:"issueSecurityScheme"`
	IssueTypeScheme          int64 `json:"issueTypeScheme"`
	IssueTypeScreenScheme    int64 `json:"issueTypeScreenScheme"`
	NotificationScheme       int64 `json:"notificationScheme"`
	PermissionScheme         int64 `json:"permissionScheme"`
	WorkflowScheme           int64 `json:"workflowScheme"`
}

// Jira's project templates for each project type and the board each gives.
var (
	softwareProjectTemplates = map[string]string{
		"": "scrum", "com.pyxis.greenhopper.jira:gh-simplified-scrum-classic": "scrum", "com.pyxis.greenhopper.jira:gh-simplified-agility-scrum": "scrum",
		"com.pyxis.greenhopper.jira:gh-cross-team-template": "scrum", "com.pyxis.greenhopper.jira:gh-cross-team-planning-template": "scrum",
		"com.pyxis.greenhopper.jira:gh-simplified-kanban-classic": "kanban", "com.pyxis.greenhopper.jira:gh-simplified-agility-kanban": "kanban",
		"com.pyxis.greenhopper.jira:gh-simplified-basic": "kanban",
	}
	serviceProjectTemplates = map[string]bool{
		"com.atlassian.servicedesk:simplified-it-service-management": true, "com.atlassian.servicedesk:simplified-it-service-management-basic": true,
		"com.atlassian.servicedesk:simplified-it-service-management-operations": true, "com.atlassian.servicedesk:simplified-internal-service-desk": true,
		"com.atlassian.servicedesk:simplified-external-service-desk": true, "com.atlassian.servicedesk:simplified-hr-service-desk": true,
		"com.atlassian.servicedesk:simplified-facilities-service-desk": true, "com.atlassian.servicedesk:simplified-legal-service-desk": true,
		"com.atlassian.servicedesk:simplified-marketing-service-desk": true, "com.atlassian.servicedesk:simplified-finance-service-desk": true,
		"com.atlassian.servicedesk:simplified-analytics-service-desk": true, "com.atlassian.servicedesk:simplified-design-service-desk": true,
		"com.atlassian.servicedesk:simplified-sales-service-desk": true, "com.atlassian.servicedesk:simplified-halp-service-desk": true,
		"com.atlassian.servicedesk:next-gen-it-service-desk": true, "com.atlassian.servicedesk:next-gen-hr-service-desk": true,
		"com.atlassian.servicedesk:next-gen-legal-service-desk": true, "com.atlassian.servicedesk:next-gen-marketing-service-desk": true,
		"com.atlassian.servicedesk:next-gen-facilities-service-desk": true, "com.atlassian.servicedesk:next-gen-analytics-service-desk": true,
		"com.atlassian.servicedesk:next-gen-finance-service-desk": true, "com.atlassian.servicedesk:next-gen-design-service-desk": true,
		"com.atlassian.servicedesk:next-gen-sales-service-desk": true, "com.atlassian.servicedesk:company-managed-blank-service-project": true,
		"com.atlassian.servicedesk:company-managed-general-service-project": true, "com.atlassian.servicedesk:team-managed-general-service-project": true,
	}
	businessProjectTemplates = map[string]bool{
		"": true, "com.atlassian.jira-core-project-templates:jira-core-simplified-content-management": true,
		"com.atlassian.jira-core-project-templates:jira-core-simplified-document-approval": true, "com.atlassian.jira-core-project-templates:jira-core-simplified-lead-tracking": true,
		"com.atlassian.jira-core-project-templates:jira-core-simplified-process-control": true, "com.atlassian.jira-core-project-templates:jira-core-simplified-procurement": true,
		"com.atlassian.jira-core-project-templates:jira-core-simplified-project-management": true, "com.atlassian.jira-core-project-templates:jira-core-simplified-recruitment": true,
		"com.atlassian.jira-core-project-templates:jira-core-simplified-task-tracking": true, "com.atlassian.jira-core-project-templates:jira-core-simplified-task-": true,
	}
)

var projectKeyPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{1,9}$`)

func validateProjectFields(up store.ProjectUpdate) map[string]string {
	fields := map[string]string{}
	if up.Name != nil && (strings.TrimSpace(*up.Name) == "" || utf8.RuneCountInString(*up.Name) > 80) {
		fields["name"] = "Enter a project name of 1–80 characters."
	}
	if up.Description != nil && len(*up.Description) > 1<<20 {
		fields["description"] = "Description must be at most 1 MiB."
	}
	if up.URL != nil && *up.URL != "" {
		u, err := url.Parse(*up.URL)
		if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || len(*up.URL) > 2048 {
			fields["url"] = "Enter a complete http or https URL without credentials."
		}
	}
	if up.AssigneeType != nil && *up.AssigneeType != "UNASSIGNED" && *up.AssigneeType != "PROJECT_LEAD" {
		fields["assigneeType"] = "Choose UNASSIGNED or PROJECT_LEAD."
	}
	return fields
}

func (s *Service) validateProjectLead(ctx context.Context, workspaceID, lead string, fields map[string]string) error {
	if lead == "" {
		return nil
	}
	_, err := s.Store.MemberByID(ctx, workspaceID, lead)
	if errors.Is(err, pgx.ErrNoRows) {
		fields["leadAccountId"] = "Choose an active member of this workspace."
		return nil
	}
	return err
}

// ValidateNewProject checks a new project's details as project creation does
// and returns the project and the board type its template gives it.
func (s *Service) ValidateNewProject(ctx context.Context, workspaceID string, in CreateProjectInput) (models.Project, string, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.AssigneeType == "" {
		in.AssigneeType = "UNASSIGNED"
	}
	fields := validateProjectFields(store.ProjectUpdate{Name: &in.Name, Description: &in.Description, URL: &in.URL, AssigneeType: &in.AssigneeType})
	if !projectKeyPattern.MatchString(in.Key) {
		fields["key"] = "Use 2–10 uppercase letters, numbers or underscores, starting with a letter."
	}
	boardType := "scrum"
	switch in.ProjectTypeKey {
	case "software":
		if board, ok := softwareProjectTemplates[in.ProjectTemplateKey]; ok {
			boardType = board
		} else {
			fields["projectTemplateKey"] = "Choose a software project template."
		}
	case "service_desk":
		boardType = "kanban"
		if !serviceProjectTemplates[in.ProjectTemplateKey] {
			fields["projectTemplateKey"] = "Choose a service management project template."
		}
	case "business":
		boardType = "kanban"
		if !businessProjectTemplates[in.ProjectTemplateKey] {
			fields["projectTemplateKey"] = "Choose a business project template."
		}
	default:
		fields["projectTypeKey"] = "Choose a business, software, or service management project."
	}
	if in.Lead != "" {
		if in.LeadAccountID != "" && in.LeadAccountID != in.Lead {
			fields["lead"] = "Give leadAccountId, or the deprecated lead, but not both."
		}
		in.LeadAccountID = in.Lead
	}
	if in.FieldScheme != 0 && in.FieldConfigurationScheme != 0 && in.FieldScheme != in.FieldConfigurationScheme {
		fields["fieldScheme"] = "Give fieldScheme, or the deprecated fieldConfigurationScheme, but not both."
	}
	if in.LeadAccountID == "" {
		fields["leadAccountId"] = "Choose a project lead."
	}
	if err := s.validateProjectLead(ctx, workspaceID, in.LeadAccountID, fields); err != nil {
		return models.Project{}, "", err
	}
	if len(fields) > 0 {
		return models.Project{}, "", &ProjectValidationError{fields}
	}
	categoryID := ""
	if in.CategoryID > 0 {
		categoryID = strconv.FormatInt(in.CategoryID, 10)
	}
	return models.Project{WorkspaceID: workspaceID, Key: in.Key, Name: in.Name, Description: in.Description, URL: in.URL, LeadAccountID: in.LeadAccountID, AssigneeType: in.AssigneeType, ProjectTypeKey: in.ProjectTypeKey, CategoryID: categoryID}, boardType, nil
}

func (s *Service) CreateProject(ctx context.Context, actorID, workspaceID string, in CreateProjectInput) (*models.Project, error) {
	project, boardType, err := s.ValidateNewProject(ctx, workspaceID, in)
	if err != nil {
		return nil, err
	}
	fieldScheme := in.FieldScheme
	if fieldScheme == 0 {
		fieldScheme = in.FieldConfigurationScheme
	}
	p, err := s.Store.CreateProjectWithSchemes(ctx, actorID, project, boardType, store.NewProjectSchemes{
		AvatarID: in.AvatarID, PermissionScheme: in.PermissionScheme, NotificationScheme: in.NotificationScheme,
		IssueSecurityScheme: in.IssueSecurityScheme, WorkflowScheme: in.WorkflowScheme, IssueTypeScheme: in.IssueTypeScheme,
		IssueTypeScreenScheme: in.IssueTypeScreenScheme, FieldConfigurationScheme: fieldScheme,
	})
	if errors.Is(err, store.ErrProjectCategoryInvalid) {
		return nil, &ProjectValidationError{map[string]string{"categoryId": err.Error()}}
	}
	var schemeErr *store.ProjectSchemeError
	if errors.As(err, &schemeErr) {
		return nil, &ProjectValidationError{map[string]string{schemeErr.Field: schemeErr.Message}}
	}
	var pgerr *pgconn.PgError
	if errors.As(err, &pgerr) && pgerr.Code == "23505" {
		return nil, &ProjectValidationError{map[string]string{"key": "A project with this key already exists."}}
	}
	return p, err
}

func (s *Service) UpdateProject(ctx context.Context, actorID, workspaceID, idOrKey string, up store.ProjectUpdate) (*models.Project, error) {
	if up.Name != nil {
		value := strings.TrimSpace(*up.Name)
		up.Name = &value
	}
	fields := validateProjectFields(up)
	if up.LeadAccountID != nil {
		if *up.LeadAccountID == "" {
			fields["leadAccountId"] = "Choose a project lead."
		}
		if err := s.validateProjectLead(ctx, workspaceID, *up.LeadAccountID, fields); err != nil {
			return nil, err
		}
	}
	if len(fields) > 0 {
		return nil, &ProjectValidationError{fields}
	}
	project, err := s.Store.UpdateProject(ctx, actorID, workspaceID, idOrKey, up)
	if errors.Is(err, store.ErrProjectCategoryInvalid) {
		return nil, &ProjectValidationError{map[string]string{"categoryId": err.Error()}}
	}
	if errors.Is(err, store.ErrProjectLeadRequired) {
		return nil, &ProjectValidationError{map[string]string{"leadAccountId": err.Error()}}
	}
	return project, err
}
