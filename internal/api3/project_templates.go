package api3

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
)

func projectTemplateError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrProjectTemplateValidation):
		jiraError(w, http.StatusBadRequest, strings.TrimPrefix(err.Error(), store.ErrProjectTemplateValidation.Error()+": "))
	case errors.Is(err, store.ErrProjectTemplateNotFound):
		jiraError(w, http.StatusNotFound, "The project template was not found.")
	default:
		jiraError(w, http.StatusInternalServerError, "internal error")
	}
}

// projectTemplateRoute serves Jira's custom project templates.
func (h *Handler) projectTemplateRoute(w http.ResponseWriter, r *http.Request, path string) {
	workspaceID, userID, e := h.authWorkspaceAdmin(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	switch {
	case path == "/project-template" && r.Method == http.MethodPost:
		h.createProjectWithCustomTemplate(w, r, workspaceID, userID)
	case path == "/project-template/save-template" && r.Method == http.MethodPost:
		var request struct {
			TemplateName               string `json:"templateName"`
			TemplateDescription        string `json:"templateDescription"`
			TemplateFromProjectRequest *struct {
				ProjectID                 int64           `json:"projectId"`
				TemplateType              string          `json:"templateType"`
				TemplateGenerationOptions json.RawMessage `json:"templateGenerationOptions"`
			} `json:"templateFromProjectRequest"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&request); err != nil || request.TemplateFromProjectRequest == nil {
			jiraError(w, http.StatusBadRequest, "The template name and templateFromProjectRequest are required.")
			return
		}
		from := request.TemplateFromProjectRequest
		template, err := h.Store.SaveProjectTemplate(r.Context(), workspaceID, userID, strconv.FormatInt(from.ProjectID, 10), from.TemplateType, request.TemplateName, request.TemplateDescription, from.TemplateGenerationOptions)
		if err != nil {
			projectTemplateError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"projectTemplateKey": map[string]string{"key": template.Key, "uuid": template.UUID}})
	case path == "/project-template/live-template" && r.Method == http.MethodGet:
		templateKey, projectRef := r.URL.Query().Get("templateKey"), r.URL.Query().Get("projectId")
		if templateKey == "" && projectRef == "" {
			jiraError(w, http.StatusBadRequest, "Give a templateKey or a projectId.")
			return
		}
		projectID := ""
		if templateKey == "" {
			project, err := h.Store.ProjectByIDOrKey(r.Context(), workspaceID, projectRef)
			if err != nil {
				projectTemplateError(w, store.ErrProjectTemplateNotFound)
				return
			}
			projectID = project.ID
		}
		template, err := h.Store.ProjectTemplate(r.Context(), workspaceID, templateKey, projectID)
		if err != nil {
			projectTemplateError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, projectTemplateModel(template))
	case path == "/project-template/edit-template" && r.Method == http.MethodPut:
		var request struct {
			TemplateKey               string          `json:"templateKey"`
			TemplateName              *string         `json:"templateName"`
			TemplateDescription       *string         `json:"templateDescription"`
			TemplateGenerationOptions json.RawMessage `json:"templateGenerationOptions"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&request); err != nil || request.TemplateKey == "" {
			jiraError(w, http.StatusBadRequest, "The templateKey is required.")
			return
		}
		if err := h.Store.EditProjectTemplate(r.Context(), workspaceID, request.TemplateKey, request.TemplateName, request.TemplateDescription, request.TemplateGenerationOptions); err != nil {
			projectTemplateError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{})
	case path == "/project-template/remove-template" && r.Method == http.MethodDelete:
		templateKey := r.URL.Query().Get("templateKey")
		if templateKey == "" {
			jiraError(w, http.StatusBadRequest, "The templateKey is required.")
			return
		}
		if err := h.Store.RemoveProjectTemplate(r.Context(), workspaceID, templateKey); err != nil {
			projectTemplateError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{})
	default:
		jiraError(w, http.StatusNotFound, "No resource found")
	}
}

var projectArchetypes = map[string]string{"software": "SOFTWARE", "business": "BUSINESS", "service_desk": "SERVICE_DESK", "product_discovery": "PRODUCT_DISCOVERY"}

func projectTemplateModel(template store.ProjectTemplate) map[string]any {
	archetype := projectArchetypes[template.Snapshot.ProjectTypeKey]
	snapshot := map[string]any{}
	raw, _ := json.Marshal(template.Snapshot)
	_ = json.Unmarshal(raw, &snapshot)
	options := map[string]any{}
	_ = json.Unmarshal(template.GenerationOptions, &options)
	boardView := "board"
	if template.Snapshot.BoardType == "" {
		boardView = "list"
	}
	model := map[string]any{
		"archetype": map[string]string{"type": archetype, "realType": archetype, "style": "classic"}, "defaultBoardView": boardView,
		"description": template.Description, "name": template.Name, "projectTemplateKey": map[string]string{"key": template.Key, "uuid": template.UUID},
		"snapshotTemplate": snapshot, "templateGenerationOptions": options, "type": template.Type,
	}
	if template.Type == "LIVE" && template.ProjectID != "" {
		if id, err := strconv.ParseInt(template.ProjectID, 10, 64); err == nil {
			model["liveTemplateProjectIdReference"] = id
		}
	}
	return model
}

// projectCreateReference is Jira's ProjectCreateResourceIdentifier.
type projectCreateReference struct {
	Type string          `json:"type"`
	ID   json.RawMessage `json:"id"`
}

func (reference *projectCreateReference) schemeID(name string) (*int64, error) {
	if reference == nil {
		return nil, nil
	}
	if reference.Type != "ID" {
		return nil, errors.New("The " + name + " must reference an existing scheme by ID; creating it from the template is not supported.")
	}
	raw := strings.Trim(string(reference.ID), `"`)
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return nil, errors.New("The " + name + " ID must be a number.")
	}
	return &id, nil
}

// createProjectWithCustomTemplate validates the project and the configuration
// it references, then creates the project in a background task.
func (h *Handler) createProjectWithCustomTemplate(w http.ResponseWriter, r *http.Request, workspaceID, userID string) {
	var request struct {
		Details *struct {
			Key           string `json:"key"`
			Name          string `json:"name"`
			Description   string `json:"description"`
			URL           string `json:"url"`
			LeadAccountID string `json:"leadAccountId"`
			AssigneeType  string `json:"assigneeType"`
			CategoryID    int64  `json:"categoryId"`
			AvatarID      int64  `json:"avatarId"`
			Language      string `json:"language"`
			AccessLevel   string `json:"accessLevel"`
		} `json:"details"`
		Template map[string]json.RawMessage `json:"template"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&request); err != nil || request.Details == nil || request.Template == nil {
		jiraError(w, http.StatusBadRequest, "The project details and template are required.")
		return
	}
	for capability, raw := range request.Template {
		trimmed := strings.TrimSpace(string(raw))
		if capability == "project" || capability == "scope" || trimmed == "null" || trimmed == "{}" {
			continue
		}
		jiraError(w, http.StatusBadRequest, "The "+capability+" capability is not supported; reference existing configuration in the project capability.")
		return
	}
	var scope struct {
		Type string `json:"type"`
	}
	if raw, ok := request.Template["scope"]; ok && json.Unmarshal(raw, &scope) != nil {
		jiraError(w, http.StatusBadRequest, "The scope is not valid.")
		return
	}
	if scope.Type == "PROJECT" {
		jiraError(w, http.StatusBadRequest, "Team-managed projects are not supported; use a GLOBAL scope.")
		return
	}
	var project struct {
		ProjectTypeKey          string                  `json:"projectTypeKey"`
		PermissionSchemeID      *projectCreateReference `json:"permissionSchemeId"`
		NotificationSchemeID    *projectCreateReference `json:"notificationSchemeId"`
		IssueTypeSchemeID       *projectCreateReference `json:"issueTypeSchemeId"`
		IssueTypeScreenSchemeID *projectCreateReference `json:"issueTypeScreenSchemeId"`
		FieldLayoutSchemeID     *projectCreateReference `json:"fieldLayoutSchemeId"`
		WorkflowSchemeID        *projectCreateReference `json:"workflowSchemeId"`
		IssueSecuritySchemeID   *projectCreateReference `json:"issueSecuritySchemeId"`
		PCRI                    *projectCreateReference `json:"pcri"`
	}
	if raw, ok := request.Template["project"]; !ok || json.Unmarshal(raw, &project) != nil {
		jiraError(w, http.StatusBadRequest, "The project capability with a projectTypeKey is required.")
		return
	}
	configuration := store.ProjectTemplateConfiguration{ProjectTypeKey: project.ProjectTypeKey}
	for _, reference := range []struct {
		ref    *projectCreateReference
		name   string
		target **int64
	}{
		{project.PermissionSchemeID, "permission scheme", &configuration.PermissionSchemeID},
		{project.NotificationSchemeID, "notification scheme", &configuration.NotificationSchemeID},
		{project.IssueTypeSchemeID, "issue type scheme", &configuration.IssueTypeSchemeID},
		{project.IssueTypeScreenSchemeID, "issue type screen scheme", &configuration.IssueTypeScreenSchemeID},
		{project.FieldLayoutSchemeID, "field layout scheme", &configuration.FieldConfigurationSchemeID},
		{project.WorkflowSchemeID, "workflow scheme", &configuration.WorkflowSchemeID},
		{project.IssueSecuritySchemeID, "issue security scheme", &configuration.IssueSecuritySchemeID},
	} {
		id, err := reference.ref.schemeID(reference.name)
		if err != nil {
			jiraError(w, http.StatusBadRequest, err.Error())
			return
		}
		*reference.target = id
	}
	details := request.Details
	assigneeType := details.AssigneeType
	switch assigneeType {
	case "", "PROJECT_DEFAULT":
		assigneeType = "UNASSIGNED"
	case "COMPONENT_LEAD":
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"assigneeType": "Choose UNASSIGNED or PROJECT_LEAD."})
		return
	}
	templateKey := ""
	if project.ProjectTypeKey == "service_desk" {
		templateKey = "com.atlassian.servicedesk:simplified-it-service-management"
	}
	newProject, boardType, err := h.Commands.ValidateNewProject(r.Context(), workspaceID, commands.CreateProjectInput{
		Key: details.Key, Name: details.Name, Description: details.Description, URL: details.URL, LeadAccountID: details.LeadAccountID,
		AssigneeType: assigneeType, ProjectTypeKey: project.ProjectTypeKey, ProjectTemplateKey: templateKey, CategoryID: details.CategoryID,
	})
	if err != nil {
		projectError(w, err)
		return
	}
	if _, err := h.Store.ProjectByIDOrKey(r.Context(), workspaceID, details.Key); err == nil {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"key": "A project with this key already exists."})
		return
	}
	configuration.BoardType, configuration.AssigneeType = boardType, assigneeType
	if err := h.Store.ValidateProjectConfiguration(r.Context(), workspaceID, configuration); err != nil {
		projectTemplateError(w, err)
		return
	}
	task, err := h.Store.EnqueueProjectFromTemplate(r.Context(), workspaceID, userID, newProject, boardType, configuration)
	if err != nil {
		projectTemplateError(w, err)
		return
	}
	w.Header().Set("Location", h.BaseURL+"/rest/api/3/task/"+task.WireID())
	writeJSON(w, http.StatusSeeOther, h.apiTaskBean(task))
}
