package api3

import (
	"context"
	"errors"
	"net/http"
	"net/mail"
	urlpkg "net/url"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
	"github.com/jackc/pgx/v5"
)

type projectCategoryRequest struct {
	ID          string  `json:"id"`
	Self        string  `json:"self"`
	Name        *string `json:"name"`
	Description *string `json:"description"`
}

func isProjectGovernancePath(path string) bool {
	return path == "/projectCategory" || strings.HasPrefix(path, "/projectCategory/") ||
		strings.HasPrefix(path, "/project/type") || strings.HasPrefix(path, "/projectvalidate/") ||
		(strings.HasPrefix(path, "/project/") && (strings.Contains(path, "/properties") || strings.Contains(path, "/features") || strings.HasSuffix(path, "/email")))
}

func (h *Handler) projectGovernanceRoute(w http.ResponseWriter, r *http.Request, path string) {
	switch {
	case path == "/projectCategory":
		h.projectCategories(w, r)
	case strings.HasPrefix(path, "/projectCategory/"):
		h.projectCategory(w, r, strings.TrimPrefix(path, "/projectCategory/"))
	case strings.HasPrefix(path, "/project/type"):
		h.projectTypes(w, r, strings.TrimPrefix(path, "/project/type"))
	case strings.HasPrefix(path, "/projectvalidate/"):
		h.projectValidation(w, r, strings.TrimPrefix(path, "/projectvalidate/"))
	case strings.HasPrefix(path, "/project/"):
		h.projectMetadataRoute(w, r, strings.Split(strings.TrimPrefix(path, "/project/"), "/"))
	default:
		jiraError(w, http.StatusNotFound, "No resource found")
	}
}

func (h *Handler) categoryBean(c *models.ProjectCategory) map[string]any {
	return map[string]any{"id": c.ID, "name": c.Name, "description": c.Description, "self": h.BaseURL + "/rest/api/3/projectCategory/" + c.ID}
}

func (h *Handler) projectCategoryMap(ctx context.Context, workspaceID string) (map[string]*models.ProjectCategory, error) {
	categories, err := h.Store.ProjectCategories(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]*models.ProjectCategory, len(categories))
	for _, category := range categories {
		out[category.ID] = category
	}
	return out, nil
}

func projectGovernanceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrProjectPermission):
		jiraError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, store.ErrProjectCategoryConflict):
		jiraError(w, http.StatusConflict, err.Error())
	case errors.Is(err, store.ErrProjectFeatureInvalid), errors.Is(err, store.ErrProjectFeatureWrongType):
		jiraError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, pgx.ErrNoRows):
		jiraError(w, http.StatusNotFound, "Project resource does not exist or is not visible.")
	default:
		jiraError(w, http.StatusInternalServerError, "Could not update project configuration.")
	}
}

func (h *Handler) projectCategories(w http.ResponseWriter, r *http.Request) {
	workspaceID, actorID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	switch r.Method {
	case http.MethodGet:
		categories, err := h.Store.ProjectCategories(r.Context(), workspaceID)
		if err != nil {
			projectGovernanceError(w, err)
			return
		}
		out := make([]map[string]any, 0, len(categories))
		for _, category := range categories {
			out = append(out, h.categoryBean(category))
		}
		writeJSON(w, http.StatusOK, out)
	case http.MethodPost:
		if _, _, e = h.authWorkspaceAdmin(r); e != nil {
			writeJerr(w, e)
			return
		}
		var input projectCategoryRequest
		if !decodeProjectRequest(w, r, &input) {
			return
		}
		if input.Name == nil || strings.TrimSpace(*input.Name) == "" || len(*input.Name) > 255 {
			jiraFieldError(w, http.StatusBadRequest, map[string]string{"name": "Project category name must contain 1 to 255 characters."})
			return
		}
		description := ""
		if input.Description != nil {
			description = *input.Description
		}
		category, err := h.Store.CreateProjectCategory(r.Context(), workspaceID, actorID, *input.Name, description)
		if err != nil {
			projectGovernanceError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, h.categoryBean(category))
	default:
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (h *Handler) projectCategory(w http.ResponseWriter, r *http.Request, id string) {
	workspaceID, actorID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	if _, err := strconv.ParseInt(id, 10, 64); err != nil {
		jiraError(w, http.StatusBadRequest, "Project category id must be an integer.")
		return
	}
	switch r.Method {
	case http.MethodGet:
		category, err := h.Store.ProjectCategory(r.Context(), workspaceID, id)
		if err != nil {
			projectGovernanceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, h.categoryBean(category))
	case http.MethodPut:
		if _, _, e = h.authWorkspaceAdmin(r); e != nil {
			writeJerr(w, e)
			return
		}
		var input projectCategoryRequest
		if !decodeProjectRequest(w, r, &input) {
			return
		}
		if input.Name != nil && (strings.TrimSpace(*input.Name) == "" || len(*input.Name) > 255) {
			jiraFieldError(w, http.StatusBadRequest, map[string]string{"name": "Project category name must contain 1 to 255 characters."})
			return
		}
		category, err := h.Store.UpdateProjectCategory(r.Context(), workspaceID, actorID, id, input.Name, input.Description)
		if err != nil {
			projectGovernanceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, h.categoryBean(category))
	case http.MethodDelete:
		if _, _, e = h.authWorkspaceAdmin(r); e != nil {
			writeJerr(w, e)
			return
		}
		if err := h.Store.DeleteProjectCategory(r.Context(), workspaceID, actorID, id); err != nil {
			projectGovernanceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

var jiraProjectTypes = []map[string]any{
	{"key": "business", "formattedKey": "Business", "descriptionI18nKey": "jira.project.type.business.description", "color": "#FFFFFF", "icon": ""},
	{"key": "software", "formattedKey": "Software", "descriptionI18nKey": "jira.project.type.software.description", "color": "#AAAAAA", "icon": ""},
	{"key": "service_desk", "formattedKey": "Service management", "descriptionI18nKey": "jira.project.type.service_desk.description", "color": "#0052CC", "icon": ""},
}

func (h *Handler) projectTypes(w http.ResponseWriter, r *http.Request, suffix string) {
	if r.Method != http.MethodGet {
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	accessible := suffix == "/accessible" || strings.HasSuffix(suffix, "/accessible")
	if accessible {
		if _, _, e := h.authWorkspace(r); e != nil {
			writeJerr(w, e)
			return
		}
	}
	if suffix == "" || suffix == "/accessible" {
		writeJSON(w, http.StatusOK, jiraProjectTypes)
		return
	}
	key := strings.TrimSuffix(strings.TrimPrefix(suffix, "/"), "/accessible")
	for _, projectType := range jiraProjectTypes {
		if projectType["key"] == key {
			writeJSON(w, http.StatusOK, projectType)
			return
		}
	}
	jiraError(w, http.StatusNotFound, "Project type does not exist.")
}

func (h *Handler) projectMetadataRoute(w http.ResponseWriter, r *http.Request, parts []string) {
	if len(parts) < 2 {
		jiraError(w, http.StatusNotFound, "No resource found")
		return
	}
	projectIDOrKey := parts[0]
	switch parts[1] {
	case "properties":
		h.projectProperties(w, r, projectIDOrKey, parts[2:])
	case "features":
		h.projectFeatures(w, r, projectIDOrKey, parts[2:])
	case "email":
		if len(parts) == 2 {
			h.projectEmail(w, r, projectIDOrKey)
		} else {
			jiraError(w, http.StatusNotFound, "No resource found")
		}
	default:
		jiraError(w, http.StatusNotFound, "No resource found")
	}
}

func (h *Handler) projectProperties(w http.ResponseWriter, r *http.Request, projectIDOrKey string, parts []string) {
	workspaceID, actorID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	if len(parts) == 0 && r.Method == http.MethodGet {
		properties, err := h.Store.ProjectProperties(r.Context(), workspaceID, projectIDOrKey)
		if err != nil {
			projectGovernanceError(w, err)
			return
		}
		keys := make([]map[string]string, 0, len(properties))
		for _, property := range properties {
			keys = append(keys, map[string]string{"key": property.Key, "self": h.BaseURL + "/rest/api/3/project/" + urlpkg.PathEscape(projectIDOrKey) + "/properties/" + urlpkg.PathEscape(property.Key)})
		}
		writeJSON(w, http.StatusOK, map[string]any{"keys": keys})
		return
	}
	if len(parts) != 1 {
		jiraError(w, http.StatusNotFound, "No resource found")
		return
	}
	key, err := urlpkg.PathUnescape(parts[0])
	if err != nil || key == "" || len(key) > 255 {
		jiraError(w, http.StatusBadRequest, "Project property key must contain 1 to 255 characters.")
		return
	}
	switch r.Method {
	case http.MethodGet:
		property, err := h.Store.ProjectProperty(r.Context(), workspaceID, projectIDOrKey, key)
		if err != nil {
			projectGovernanceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, property)
	case http.MethodPut:
		if _, _, e = h.authWorkspaceAdmin(r); e != nil {
			writeJerr(w, e)
			return
		}
		value, ok := decodeIssuePropertyValue(w, r)
		if !ok {
			return
		}
		_, created, err := h.Store.SetProjectProperty(r.Context(), workspaceID, actorID, projectIDOrKey, key, value)
		if err != nil {
			projectGovernanceError(w, err)
			return
		}
		status := http.StatusOK
		if created {
			status = http.StatusCreated
		}
		w.WriteHeader(status)
	case http.MethodDelete:
		if _, _, e = h.authWorkspaceAdmin(r); e != nil {
			writeJerr(w, e)
			return
		}
		if err := h.Store.DeleteProjectProperty(r.Context(), workspaceID, actorID, projectIDOrKey, key); err != nil {
			projectGovernanceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func projectFeatureBean(projectID string, feature models.ProjectFeature) map[string]any {
	id, _ := strconv.ParseInt(projectID, 10, 64)
	return map[string]any{"projectId": id, "feature": feature.Key, "localisedName": feature.Name, "localisedDescription": feature.Description, "state": feature.State, "prerequisites": feature.Prerequisites, "toggleLocked": feature.ToggleLocked, "imageUri": ""}
}

func projectFeatureContainer(projectID string, features []models.ProjectFeature) map[string]any {
	out := make([]map[string]any, 0, len(features))
	for _, feature := range features {
		out = append(out, projectFeatureBean(projectID, feature))
	}
	return map[string]any{"features": out}
}

func (h *Handler) projectFeatures(w http.ResponseWriter, r *http.Request, projectIDOrKey string, parts []string) {
	workspaceID, actorID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	p, err := h.Store.ProjectByIDOrKey(r.Context(), workspaceID, projectIDOrKey)
	if err != nil {
		projectGovernanceError(w, err)
		return
	}
	if len(parts) == 0 && r.Method == http.MethodGet {
		features, err := h.Store.ProjectFeatures(r.Context(), workspaceID, p.ID)
		if err != nil {
			projectGovernanceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, projectFeatureContainer(p.ID, features))
		return
	}
	if len(parts) == 1 && r.Method == http.MethodPut {
		if _, _, e = h.authWorkspaceAdmin(r); e != nil {
			writeJerr(w, e)
			return
		}
		var input struct {
			State string `json:"state"`
		}
		if !decodeProjectRequest(w, r, &input) {
			return
		}
		if input.State != "ENABLED" && input.State != "DISABLED" {
			jiraFieldError(w, http.StatusBadRequest, map[string]string{"state": "Choose ENABLED or DISABLED."})
			return
		}
		features, err := h.Store.SetProjectFeature(r.Context(), workspaceID, actorID, p.ID, parts[0], input.State)
		if err != nil {
			projectGovernanceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, projectFeatureContainer(p.ID, features))
		return
	}
	jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
}

func (h *Handler) projectEmail(w http.ResponseWriter, r *http.Request, projectID string) {
	workspaceID, actorID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	if _, err := strconv.ParseInt(projectID, 10, 64); err != nil {
		jiraError(w, http.StatusBadRequest, "Project id must be an integer.")
		return
	}
	p, err := h.Store.ProjectByIDOrKey(r.Context(), workspaceID, projectID)
	if err != nil {
		projectGovernanceError(w, err)
		return
	}
	switch r.Method {
	case http.MethodGet:
		email := p.SenderEmail
		if email == "" {
			host := "zzira.local"
			if base, err := urlpkg.Parse(h.BaseURL); err == nil && base.Hostname() != "" {
				host = base.Hostname()
			}
			email = "jira@" + host
		}
		writeJSON(w, http.StatusOK, map[string]any{"emailAddress": email, "emailAddressStatus": []string{}})
	case http.MethodPut:
		if _, _, e = h.authWorkspaceAdmin(r); e != nil {
			writeJerr(w, e)
			return
		}
		var input struct {
			EmailAddress string `json:"emailAddress"`
		}
		if !decodeProjectRequest(w, r, &input) {
			return
		}
		if input.EmailAddress != "" {
			address, err := mail.ParseAddress(input.EmailAddress)
			if err != nil || address.Address != input.EmailAddress || len(input.EmailAddress) > 254 {
				jiraFieldError(w, http.StatusBadRequest, map[string]string{"emailAddress": "Enter a valid email address."})
				return
			}
		}
		if err := h.Store.SetProjectEmail(r.Context(), workspaceID, actorID, p.ID, input.EmailAddress); err != nil {
			projectGovernanceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func normalizedProjectKey(input string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(input) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		}
	}
	key := b.String()
	if key == "" || key[0] < 'A' || key[0] > 'Z' {
		key = "P" + key
	}
	if len(key) < 2 {
		key += "P"
	}
	if len(key) > 10 {
		key = key[:10]
	}
	return key
}

func (h *Handler) projectValidation(w http.ResponseWriter, r *http.Request, kind string) {
	if r.Method != http.MethodGet {
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	workspaceID, _, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	projects, err := h.Store.ProjectsByWorkspace(r.Context(), workspaceID)
	if err != nil {
		projectGovernanceError(w, err)
		return
	}
	usedKeys, usedNames := map[string]bool{}, map[string]bool{}
	for _, p := range projects {
		usedKeys[strings.ToUpper(p.Key)] = true
		usedNames[strings.ToLower(p.Name)] = true
	}
	switch kind {
	case "key":
		key := r.URL.Query().Get("key")
		errs := map[string]string{}
		if normalizedProjectKey(key) != key || len(key) < 2 {
			errs["projectKey"] = "Project keys must start with an uppercase letter followed by uppercase letters, numbers, or underscores."
		} else if usedKeys[key] {
			errs["projectKey"] = "A project with that project key already exists."
		}
		writeJSON(w, http.StatusOK, map[string]any{"errorMessages": []string{}, "errors": errs})
	case "validProjectKey":
		base := normalizedProjectKey(r.URL.Query().Get("key"))
		candidate := base
		for n := 2; usedKeys[candidate]; n++ {
			suffix := strconv.Itoa(n)
			prefix := base
			if len(prefix)+len(suffix) > 10 {
				prefix = prefix[:10-len(suffix)]
			}
			candidate = prefix + suffix
		}
		writeJSON(w, http.StatusOK, candidate)
	case "validProjectName":
		name := strings.TrimSpace(r.URL.Query().Get("name"))
		if name == "" {
			jiraError(w, http.StatusBadRequest, "The name parameter is required.")
			return
		}
		candidate := name
		for n := 2; usedNames[strings.ToLower(candidate)]; n++ {
			candidate = name + " (" + strconv.Itoa(n) + ")"
		}
		writeJSON(w, http.StatusOK, candidate)
	default:
		jiraError(w, http.StatusNotFound, "No resource found")
	}
}
