package api3

import (
	"net/http"
	"strings"

	"github.com/e6qu/zzira/internal/models"
)

func isScreenSchemePath(path string) bool {
	return path == "/screenscheme" || strings.HasPrefix(path, "/screenscheme/") ||
		path == "/issuetypescreenscheme" || strings.HasPrefix(path, "/issuetypescreenscheme/")
}

func (h *Handler) screenSchemeBean(scheme *models.ScreenScheme) map[string]any {
	screens := map[string]any{}
	for operation, screenID := range scheme.Screens {
		screens[operation] = wireNumericID(screenID)
	}
	return map[string]any{
		"id": wireNumericID(scheme.ID), "name": scheme.Name,
		"description": scheme.Description, "screens": screens,
	}
}

func (h *Handler) issueTypeScreenSchemeBean(scheme *models.IssueTypeScreenScheme) map[string]any {
	return map[string]any{
		"id": wireNumericID(scheme.ID), "name": scheme.Name, "description": scheme.Description,
	}
}

func (h *Handler) screenSchemeRoute(w http.ResponseWriter, r *http.Request, path string) {
	if path == "/screenscheme" {
		h.screenSchemeCollection(w, r)
		return
	}
	if strings.HasPrefix(path, "/screenscheme/") {
		h.screenSchemeResource(w, r, strings.Trim(strings.TrimPrefix(path, "/screenscheme/"), "/"))
		return
	}
	if path == "/issuetypescreenscheme" {
		h.issueTypeScreenSchemeCollection(w, r)
		return
	}
	rest := strings.Trim(strings.TrimPrefix(path, "/issuetypescreenscheme/"), "/")
	parts := strings.Split(rest, "/")
	switch {
	case len(parts) == 1 && parts[0] == "mapping":
		h.issueTypeScreenSchemeMappings(w, r)
	case len(parts) == 1 && parts[0] == "project":
		h.issueTypeScreenSchemeProjects(w, r)
	case len(parts) == 1:
		h.issueTypeScreenSchemeResource(w, r, parts[0])
	case len(parts) == 2 && parts[1] == "mapping":
		h.appendIssueTypeScreenSchemeMapping(w, r, parts[0])
	case len(parts) == 2 && parts[1] == "project":
		h.projectsForIssueTypeScreenScheme(w, r, parts[0])
	case len(parts) == 3 && parts[1] == "mapping" && parts[2] == "default":
		h.setIssueTypeScreenSchemeDefault(w, r, parts[0])
	case len(parts) == 3 && parts[1] == "mapping" && parts[2] == "remove":
		h.removeIssueTypeScreenSchemeMapping(w, r, parts[0])
	default:
		jiraError(w, http.StatusNotFound, "No resource found")
	}
}

func (h *Handler) screenSchemeCollection(w http.ResponseWriter, r *http.Request) {
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
		schemes, err := h.Store.ScreenSchemes(r.Context(), workspaceID, securityQueryValues(r, "id"))
		if err != nil {
			screenError(w, err)
			return
		}
		page := pageSlice(schemes, startAt, maxResults)
		values := make([]map[string]any, 0, len(page))
		for _, scheme := range page {
			values = append(values, h.screenSchemeBean(scheme))
		}
		writeJSON(w, http.StatusOK, h.securityPageBean(r, values, len(schemes), startAt, maxResults))
	case http.MethodPost:
		var request struct {
			Name        string            `json:"name"`
			Description string            `json:"description"`
			Screens     map[string]string `json:"screens"`
		}
		if !decodeProjectRequest(w, r, &request) {
			return
		}
		scheme, err := h.Store.CreateScreenScheme(r.Context(), workspaceID, actorID, request.Name, request.Description, request.Screens)
		if err != nil {
			screenError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, h.screenSchemeBean(scheme))
	default:
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (h *Handler) screenSchemeResource(w http.ResponseWriter, r *http.Request, schemeID string) {
	workspaceID, actorID, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	switch r.Method {
	case http.MethodPut:
		var request struct {
			Name        *string           `json:"name"`
			Description *string           `json:"description"`
			Screens     map[string]string `json:"screens"`
		}
		if !decodeProjectRequest(w, r, &request) {
			return
		}
		if _, err := h.Store.UpdateScreenScheme(r.Context(), workspaceID, actorID, schemeID, request.Name, request.Description, request.Screens); err != nil {
			screenError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{})
	case http.MethodDelete:
		if err := h.Store.DeleteScreenScheme(r.Context(), workspaceID, actorID, schemeID); err != nil {
			screenError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func issueTypeScreenSchemeMappingInputs(raw []struct {
	IssueTypeID    string `json:"issueTypeId"`
	ScreenSchemeID string `json:"screenSchemeId"`
}) []models.IssueTypeScreenSchemeItem {
	mappings := make([]models.IssueTypeScreenSchemeItem, 0, len(raw))
	for _, item := range raw {
		mappings = append(mappings, models.IssueTypeScreenSchemeItem{
			IssueTypeID: item.IssueTypeID, ScreenSchemeID: item.ScreenSchemeID})
	}
	return mappings
}

func (h *Handler) issueTypeScreenSchemeCollection(w http.ResponseWriter, r *http.Request) {
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
		schemes, err := h.Store.IssueTypeScreenSchemes(r.Context(), workspaceID, securityQueryValues(r, "id"))
		if err != nil {
			screenError(w, err)
			return
		}
		page := pageSlice(schemes, startAt, maxResults)
		values := make([]map[string]any, 0, len(page))
		for _, scheme := range page {
			values = append(values, h.issueTypeScreenSchemeBean(scheme))
		}
		writeJSON(w, http.StatusOK, h.securityPageBean(r, values, len(schemes), startAt, maxResults))
	case http.MethodPost:
		var request struct {
			Name              string `json:"name"`
			Description       string `json:"description"`
			IssueTypeMappings []struct {
				IssueTypeID    string `json:"issueTypeId"`
				ScreenSchemeID string `json:"screenSchemeId"`
			} `json:"issueTypeMappings"`
		}
		if !decodeProjectRequest(w, r, &request) {
			return
		}
		scheme, err := h.Store.CreateIssueTypeScreenScheme(r.Context(), workspaceID, actorID, request.Name, request.Description,
			issueTypeScreenSchemeMappingInputs(request.IssueTypeMappings))
		if err != nil {
			screenError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"id": wireNumericID(scheme.ID)})
	default:
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (h *Handler) issueTypeScreenSchemeResource(w http.ResponseWriter, r *http.Request, schemeID string) {
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
		if err := h.Store.UpdateIssueTypeScreenScheme(r.Context(), workspaceID, actorID, schemeID, request.Name, request.Description); err != nil {
			screenError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case http.MethodDelete:
		if err := h.Store.DeleteIssueTypeScreenScheme(r.Context(), workspaceID, actorID, schemeID); err != nil {
			screenError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (h *Handler) issueTypeScreenSchemeMappings(w http.ResponseWriter, r *http.Request) {
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
	schemes, err := h.Store.IssueTypeScreenSchemes(r.Context(), workspaceID, securityQueryValues(r, "issueTypeScreenSchemeId"))
	if err != nil {
		screenError(w, err)
		return
	}
	flattened := []map[string]any{}
	for _, scheme := range schemes {
		for _, mapping := range scheme.Mappings {
			flattened = append(flattened, map[string]any{
				"issueTypeScreenSchemeId": wireNumericID(scheme.ID),
				"issueTypeId":             mapping.IssueTypeID,
				"screenSchemeId":          wireNumericID(mapping.ScreenSchemeID),
			})
		}
	}
	page := pageSlice(flattened, startAt, maxResults)
	writeJSON(w, http.StatusOK, h.securityPageBean(r, page, len(flattened), startAt, maxResults))
}

func (h *Handler) issueTypeScreenSchemeProjects(w http.ResponseWriter, r *http.Request) {
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
		assignments, err := h.Store.IssueTypeScreenSchemeProjects(r.Context(), workspaceID)
		if err != nil {
			screenError(w, err)
			return
		}
		projects := stringQuerySet(securityQueryValues(r, "projectId"))
		values := []map[string]any{}
		for _, assignment := range assignments {
			if len(projects) > 0 && !projects[assignment.ProjectID] {
				continue
			}
			values = append(values, map[string]any{
				"issueTypeScreenSchemeId": wireNumericID(assignment.SchemeID),
				"projectId":               wireNumericID(assignment.ProjectID),
			})
		}
		page := pageSlice(values, startAt, maxResults)
		writeJSON(w, http.StatusOK, h.securityPageBean(r, page, len(values), startAt, maxResults))
	case http.MethodPut:
		var request struct {
			IssueTypeScreenSchemeID string `json:"issueTypeScreenSchemeId"`
			ProjectID               string `json:"projectId"`
		}
		if !decodeProjectRequest(w, r, &request) {
			return
		}
		if err := h.Store.AssignIssueTypeScreenScheme(r.Context(), workspaceID, actorID, request.ProjectID, request.IssueTypeScreenSchemeID); err != nil {
			screenError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (h *Handler) projectsForIssueTypeScreenScheme(w http.ResponseWriter, r *http.Request, schemeID string) {
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
	assignments, err := h.Store.IssueTypeScreenSchemeProjects(r.Context(), workspaceID)
	if err != nil {
		screenError(w, err)
		return
	}
	values := []map[string]any{}
	for _, assignment := range assignments {
		if assignment.SchemeID != schemeID {
			continue
		}
		project, projectErr := h.Store.ProjectByIDOrKey(r.Context(), workspaceID, assignment.ProjectID)
		if projectErr != nil {
			continue
		}
		values = append(values, h.projectBean(project))
	}
	page := pageSlice(values, startAt, maxResults)
	writeJSON(w, http.StatusOK, h.securityPageBean(r, page, len(values), startAt, maxResults))
}

func (h *Handler) appendIssueTypeScreenSchemeMapping(w http.ResponseWriter, r *http.Request, schemeID string) {
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
		IssueTypeMappings []struct {
			IssueTypeID    string `json:"issueTypeId"`
			ScreenSchemeID string `json:"screenSchemeId"`
		} `json:"issueTypeMappings"`
	}
	if !decodeProjectRequest(w, r, &request) {
		return
	}
	if err := h.Store.AppendIssueTypeScreenSchemeMappings(r.Context(), workspaceID, actorID, schemeID,
		issueTypeScreenSchemeMappingInputs(request.IssueTypeMappings)); err != nil {
		screenError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) setIssueTypeScreenSchemeDefault(w http.ResponseWriter, r *http.Request, schemeID string) {
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
		ScreenSchemeID string `json:"screenSchemeId"`
	}
	if !decodeProjectRequest(w, r, &request) {
		return
	}
	if err := h.Store.SetIssueTypeScreenSchemeDefault(r.Context(), workspaceID, actorID, schemeID, request.ScreenSchemeID); err != nil {
		screenError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) removeIssueTypeScreenSchemeMapping(w http.ResponseWriter, r *http.Request, schemeID string) {
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
	if err := h.Store.RemoveIssueTypeScreenSchemeMappings(r.Context(), workspaceID, actorID, schemeID, request.IssueTypeIDs); err != nil {
		screenError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
