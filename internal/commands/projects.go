package commands

import (
	"context"
	"errors"
	"net/url"
	"regexp"
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
}

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

func (s *Service) CreateProject(ctx context.Context, actorID, workspaceID string, in CreateProjectInput) (*models.Project, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.AssigneeType == "" {
		in.AssigneeType = "UNASSIGNED"
	}
	fields := validateProjectFields(store.ProjectUpdate{Name: &in.Name, Description: &in.Description, URL: &in.URL, AssigneeType: &in.AssigneeType})
	if !projectKeyPattern.MatchString(in.Key) {
		fields["key"] = "Use 2–10 uppercase letters, numbers or underscores, starting with a letter."
	}
	if in.ProjectTypeKey != "software" {
		fields["projectTypeKey"] = "Only software projects are currently supported."
	}
	boardType := "scrum"
	switch in.ProjectTemplateKey {
	case "", "com.pyxis.greenhopper.jira:gh-simplified-scrum-classic":
	case "com.pyxis.greenhopper.jira:gh-simplified-kanban-classic":
		boardType = "kanban"
	default:
		fields["projectTemplateKey"] = "Choose a company-managed Scrum or Kanban template."
	}
	if in.LeadAccountID == "" {
		fields["leadAccountId"] = "Choose a project lead."
	}
	if err := s.validateProjectLead(ctx, workspaceID, in.LeadAccountID, fields); err != nil {
		return nil, err
	}
	if len(fields) > 0 {
		return nil, &ProjectValidationError{fields}
	}
	p, err := s.Store.CreateProject(ctx, actorID, models.Project{WorkspaceID: workspaceID, Key: in.Key, Name: in.Name, Description: in.Description, URL: in.URL, LeadAccountID: in.LeadAccountID, AssigneeType: in.AssigneeType}, boardType)
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
	if errors.Is(err, store.ErrProjectLeadRequired) {
		return nil, &ProjectValidationError{map[string]string{"leadAccountId": err.Error()}}
	}
	return project, err
}
