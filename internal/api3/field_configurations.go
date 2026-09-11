package api3

import (
	"errors"
	"net/http"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

func isFieldConfigurationPath(path string) bool {
	return path == "/fieldconfiguration" || strings.HasPrefix(path, "/fieldconfiguration/") ||
		path == "/fieldconfigurationscheme" || strings.HasPrefix(path, "/fieldconfigurationscheme/")
}

func fieldConfigError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrProjectPermission):
		jiraError(w, http.StatusForbidden, "Administrator privileges are required.")
	case errors.Is(err, store.ErrFieldConfigConflict):
		jiraError(w, http.StatusConflict, err.Error())
	case errors.Is(err, store.ErrFieldConfigValidation):
		jiraError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, store.ErrFieldConfigNotFound):
		jiraError(w, http.StatusNotFound, "The field configuration or scheme does not exist.")
	default:
		jiraError(w, http.StatusInternalServerError, "Could not complete the field configuration operation.")
	}
}

func (h *Handler) fieldConfigurationBean(configuration *models.FieldConfiguration) map[string]any {
	return map[string]any{
		"id": wireNumericID(configuration.ID), "name": configuration.Name,
		"description": configuration.Description, "isDefault": configuration.IsDefault,
	}
}

func (h *Handler) fieldConfigurationSchemeBean(scheme *models.FieldConfigurationScheme) map[string]any {
	return map[string]any{
		"id": wireNumericID(scheme.ID), "name": scheme.Name, "description": scheme.Description,
	}
}

func (h *Handler) fieldConfigurationRoute(w http.ResponseWriter, r *http.Request, path string) {
	switch {
	case path == "/fieldconfiguration":
		h.fieldConfigurationCollection(w, r)
		return
	case path == "/fieldconfigurationscheme":
		h.fieldConfigurationSchemeCollection(w, r)
		return
	}
	if strings.HasPrefix(path, "/fieldconfiguration/") {
		parts := strings.Split(strings.Trim(strings.TrimPrefix(path, "/fieldconfiguration/"), "/"), "/")
		switch {
		case len(parts) == 1:
			h.fieldConfigurationResource(w, r, parts[0])
		case len(parts) == 2 && parts[1] == "fields":
			h.fieldConfigurationItems(w, r, parts[0])
		default:
			jiraError(w, http.StatusNotFound, "No resource found")
		}
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(path, "/fieldconfigurationscheme/"), "/"), "/")
	switch {
	case len(parts) == 1 && parts[0] == "mapping":
		h.fieldConfigurationSchemeMappings(w, r)
	case len(parts) == 1 && parts[0] == "project":
		h.fieldConfigurationSchemeProjects(w, r)
	case len(parts) == 1:
		h.fieldConfigurationSchemeResource(w, r, parts[0])
	case len(parts) == 2 && parts[1] == "mapping":
		h.setFieldConfigurationSchemeMapping(w, r, parts[0])
	case len(parts) == 3 && parts[1] == "mapping" && parts[2] == "delete":
		h.removeFieldConfigurationSchemeMapping(w, r, parts[0])
	default:
		jiraError(w, http.StatusNotFound, "No resource found")
	}
}

func (h *Handler) fieldConfigurationCollection(w http.ResponseWriter, r *http.Request) {
	workspaceID, actorID, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	switch r.Method {
	case http.MethodGet:
		startAt, maxResults, err := notificationPage(r)
		if err != nil {
			jiraError(w, http.StatusBadRequest, "startAt and maxResults are invalid.")
			return
		}
		configurations, err := h.Store.FieldConfigurations(r.Context(), workspaceID, securityQueryValues(r, "id"))
		if err != nil {
			fieldConfigError(w, err)
			return
		}
		page := pageSlice(configurations, startAt, maxResults)
		values := make([]map[string]any, 0, len(page))
		for _, configuration := range page {
			values = append(values, h.fieldConfigurationBean(configuration))
		}
		writeJSON(w, http.StatusOK, h.securityPageBean(r, values, len(configurations), startAt, maxResults))
	case http.MethodPost:
		var request struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		if !decodeProjectRequest(w, r, &request) {
			return
		}
		configuration, err := h.Store.CreateFieldConfiguration(r.Context(), workspaceID, actorID, request.Name, request.Description)
		if err != nil {
			fieldConfigError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, h.fieldConfigurationBean(configuration))
	default:
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (h *Handler) fieldConfigurationResource(w http.ResponseWriter, r *http.Request, configurationID string) {
	workspaceID, actorID, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	switch r.Method {
	case http.MethodPut:
		var request struct {
			Name        *string `json:"name"`
			Description *string `json:"description"`
		}
		if !decodeProjectRequest(w, r, &request) {
			return
		}
		if err := h.Store.UpdateFieldConfiguration(r.Context(), workspaceID, actorID, configurationID, request.Name, request.Description); err != nil {
			fieldConfigError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case http.MethodDelete:
		if err := h.Store.DeleteFieldConfiguration(r.Context(), workspaceID, actorID, configurationID); err != nil {
			fieldConfigError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (h *Handler) fieldConfigurationItems(w http.ResponseWriter, r *http.Request, configurationID string) {
	workspaceID, actorID, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	switch r.Method {
	case http.MethodGet:
		startAt, maxResults, err := notificationPage(r)
		if err != nil {
			jiraError(w, http.StatusBadRequest, "startAt and maxResults are invalid.")
			return
		}
		items, err := h.Store.FieldConfigurationItems(r.Context(), workspaceID, configurationID)
		if err != nil {
			fieldConfigError(w, err)
			return
		}
		page := pageSlice(items, startAt, maxResults)
		values := make([]map[string]any, 0, len(page))
		for _, item := range page {
			values = append(values, map[string]any{
				"id": item.FieldID, "isRequired": item.IsRequired,
				"isHidden": item.IsHidden, "description": item.Description,
			})
		}
		writeJSON(w, http.StatusOK, h.securityPageBean(r, values, len(items), startAt, maxResults))
	case http.MethodPut:
		var request struct {
			FieldConfigurationItems []struct {
				ID          string `json:"id"`
				IsRequired  bool   `json:"isRequired"`
				IsHidden    bool   `json:"isHidden"`
				Description string `json:"description"`
			} `json:"fieldConfigurationItems"`
		}
		if !decodeProjectRequest(w, r, &request) {
			return
		}
		items := make([]models.FieldConfigurationItem, 0, len(request.FieldConfigurationItems))
		for _, item := range request.FieldConfigurationItems {
			items = append(items, models.FieldConfigurationItem{
				FieldID: item.ID, IsRequired: item.IsRequired, IsHidden: item.IsHidden, Description: item.Description})
		}
		if err := h.Store.SetFieldConfigurationItems(r.Context(), workspaceID, actorID, configurationID, items); err != nil {
			fieldConfigError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (h *Handler) fieldConfigurationSchemeCollection(w http.ResponseWriter, r *http.Request) {
	workspaceID, actorID, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	switch r.Method {
	case http.MethodGet:
		startAt, maxResults, err := notificationPage(r)
		if err != nil {
			jiraError(w, http.StatusBadRequest, "startAt and maxResults are invalid.")
			return
		}
		schemes, err := h.Store.FieldConfigurationSchemes(r.Context(), workspaceID, securityQueryValues(r, "id"))
		if err != nil {
			fieldConfigError(w, err)
			return
		}
		page := pageSlice(schemes, startAt, maxResults)
		values := make([]map[string]any, 0, len(page))
		for _, scheme := range page {
			values = append(values, h.fieldConfigurationSchemeBean(scheme))
		}
		writeJSON(w, http.StatusOK, h.securityPageBean(r, values, len(schemes), startAt, maxResults))
	case http.MethodPost:
		var request struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		if !decodeProjectRequest(w, r, &request) {
			return
		}
		scheme, err := h.Store.CreateFieldConfigurationScheme(r.Context(), workspaceID, actorID, request.Name, request.Description)
		if err != nil {
			fieldConfigError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, h.fieldConfigurationSchemeBean(scheme))
	default:
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (h *Handler) fieldConfigurationSchemeResource(w http.ResponseWriter, r *http.Request, schemeID string) {
	workspaceID, actorID, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	switch r.Method {
	case http.MethodPut:
		var request struct {
			Name        *string `json:"name"`
			Description *string `json:"description"`
		}
		if !decodeProjectRequest(w, r, &request) {
			return
		}
		if err := h.Store.UpdateFieldConfigurationScheme(r.Context(), workspaceID, actorID, schemeID, request.Name, request.Description); err != nil {
			fieldConfigError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case http.MethodDelete:
		if err := h.Store.DeleteFieldConfigurationScheme(r.Context(), workspaceID, actorID, schemeID); err != nil {
			fieldConfigError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (h *Handler) fieldConfigurationSchemeMappings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	workspaceID, _, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	startAt, maxResults, err := notificationPage(r)
	if err != nil {
		jiraError(w, http.StatusBadRequest, "startAt and maxResults are invalid.")
		return
	}
	schemes, err := h.Store.FieldConfigurationSchemes(r.Context(), workspaceID, securityQueryValues(r, "fieldConfigurationSchemeId"))
	if err != nil {
		fieldConfigError(w, err)
		return
	}
	flattened := []map[string]any{}
	for _, scheme := range schemes {
		for _, mapping := range scheme.Mappings {
			flattened = append(flattened, map[string]any{
				"fieldConfigurationSchemeId": wireNumericID(scheme.ID),
				"issueTypeId":                mapping.IssueTypeID,
				"fieldConfigurationId":       wireNumericID(mapping.FieldConfigurationID),
			})
		}
	}
	page := pageSlice(flattened, startAt, maxResults)
	writeJSON(w, http.StatusOK, h.securityPageBean(r, page, len(flattened), startAt, maxResults))
}

func (h *Handler) fieldConfigurationSchemeProjects(w http.ResponseWriter, r *http.Request) {
	workspaceID, actorID, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	switch r.Method {
	case http.MethodGet:
		startAt, maxResults, err := notificationPage(r)
		if err != nil {
			jiraError(w, http.StatusBadRequest, "startAt and maxResults are invalid.")
			return
		}
		assignments, err := h.Store.FieldConfigurationSchemeProjects(r.Context(), workspaceID)
		if err != nil {
			fieldConfigError(w, err)
			return
		}
		projects := stringQuerySet(securityQueryValues(r, "projectId"))
		values := []map[string]any{}
		for _, assignment := range assignments {
			if len(projects) > 0 && !projects[assignment.ProjectID] {
				continue
			}
			values = append(values, map[string]any{
				"fieldConfigurationScheme": map[string]any{"id": wireNumericID(assignment.SchemeID)},
				"projectIds":               []any{wireNumericID(assignment.ProjectID)},
			})
		}
		page := pageSlice(values, startAt, maxResults)
		writeJSON(w, http.StatusOK, h.securityPageBean(r, page, len(values), startAt, maxResults))
	case http.MethodPut:
		var request struct {
			FieldConfigurationSchemeID string `json:"fieldConfigurationSchemeId"`
			ProjectID                  string `json:"projectId"`
		}
		if !decodeProjectRequest(w, r, &request) {
			return
		}
		if err := h.Store.AssignFieldConfigurationScheme(r.Context(), workspaceID, actorID, request.ProjectID, request.FieldConfigurationSchemeID); err != nil {
			fieldConfigError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (h *Handler) setFieldConfigurationSchemeMapping(w http.ResponseWriter, r *http.Request, schemeID string) {
	if r.Method != http.MethodPut {
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	workspaceID, actorID, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	var request struct {
		Mappings []struct {
			IssueTypeID          string `json:"issueTypeId"`
			FieldConfigurationID string `json:"fieldConfigurationId"`
		} `json:"mappings"`
	}
	if !decodeProjectRequest(w, r, &request) {
		return
	}
	mappings := make([]models.FieldConfigurationSchemeItem, 0, len(request.Mappings))
	for _, mapping := range request.Mappings {
		mappings = append(mappings, models.FieldConfigurationSchemeItem{
			IssueTypeID: mapping.IssueTypeID, FieldConfigurationID: mapping.FieldConfigurationID})
	}
	if err := h.Store.SetFieldConfigurationSchemeMappings(r.Context(), workspaceID, actorID, schemeID, mappings); err != nil {
		fieldConfigError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) removeFieldConfigurationSchemeMapping(w http.ResponseWriter, r *http.Request, schemeID string) {
	if r.Method != http.MethodPost {
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	workspaceID, actorID, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	var request struct {
		IssueTypeIDs []string `json:"issueTypeIds"`
	}
	if !decodeProjectRequest(w, r, &request) {
		return
	}
	if err := h.Store.RemoveFieldConfigurationSchemeMappings(r.Context(), workspaceID, actorID, schemeID, request.IssueTypeIDs); err != nil {
		fieldConfigError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
