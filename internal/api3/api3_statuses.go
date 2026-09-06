package api3

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

func jiraStatusCategory(category string) string {
	return map[string]string{"new": "TODO", "indeterminate": "IN_PROGRESS", "done": "DONE"}[category]
}

func internalStatusCategory(category string) string {
	return map[string]string{"TODO": "new", "IN_PROGRESS": "indeterminate", "DONE": "done"}[category]
}

func (h *Handler) jiraStatusBean(status models.Status) map[string]any {
	scope := jiraStatusScope(status)
	return map[string]any{
		"id": status.ID, "name": status.Name, "description": status.Description,
		"statusCategory": jiraStatusCategory(status.Category), "scope": scope,
	}
}

func jiraStatusScope(status models.Status) map[string]any {
	if status.ProjectID != "" {
		return map[string]any{"type": "PROJECT", "project": map[string]string{"id": status.ProjectID}}
	}
	return map[string]any{"type": "GLOBAL"}
}

func statusAPIError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrAdminValidation):
		jiraError(w, http.StatusBadRequest, strings.TrimPrefix(err.Error(), store.ErrAdminValidation.Error()+": "))
	case errors.Is(err, store.ErrAdminConflict):
		jiraError(w, http.StatusConflict, strings.TrimPrefix(err.Error(), store.ErrAdminConflict.Error()+": "))
	case errors.Is(err, store.ErrAdminNotFound), errors.Is(err, pgx.ErrNoRows):
		jiraError(w, http.StatusNotFound, "The status does not exist.")
	default:
		jiraError(w, http.StatusInternalServerError, "internal error")
	}
}

func statusesByID(statuses []models.Status) map[string]models.Status {
	byID := make(map[string]models.Status, len(statuses))
	for _, status := range statuses {
		byID[status.ID] = status
	}
	return byID
}

func (h *Handler) bulkStatusesEndpoint(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		workspaceID, _, e := h.authWorkspace(r)
		if e != nil {
			writeJerr(w, e)
			return
		}
		ids := r.URL.Query()["id"]
		if len(ids) == 0 {
			jiraError(w, http.StatusBadRequest, "At least one status id is required.")
			return
		}
		statuses, err := h.Store.StatusesForAdministration(r.Context(), workspaceID)
		if err != nil {
			statusAPIError(w, err)
			return
		}
		byID := statusesByID(statuses)
		out := make([]map[string]any, 0, len(ids))
		for _, id := range ids {
			if status, ok := byID[id]; ok {
				out = append(out, h.jiraStatusBean(status))
			}
		}
		writeJSON(w, http.StatusOK, out)
		return
	}

	workspaceID, userID, e := h.authWorkspaceAdmin(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	switch r.Method {
	case http.MethodPost:
		var request struct {
			Scope struct {
				Type    string `json:"type"`
				Project struct {
					ID string `json:"id"`
				} `json:"project"`
			} `json:"scope"`
			Statuses []struct {
				Name, Description, StatusCategory string
			} `json:"statuses"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || len(request.Statuses) == 0 {
			jiraError(w, http.StatusBadRequest, "A valid scope and at least one status are required.")
			return
		}
		projectID := ""
		switch request.Scope.Type {
		case "GLOBAL":
			if request.Scope.Project.ID != "" {
				jiraError(w, http.StatusBadRequest, "GLOBAL scope cannot include a project.")
				return
			}
		case "PROJECT":
			project, err := h.Store.ProjectByIDOrKey(r.Context(), workspaceID, request.Scope.Project.ID)
			if err != nil {
				jiraError(w, http.StatusBadRequest, "PROJECT scope requires a project in this workspace.")
				return
			}
			projectID = project.ID
		default:
			jiraError(w, http.StatusBadRequest, "Scope type must be GLOBAL or PROJECT.")
			return
		}
		out := make([]map[string]any, 0, len(request.Statuses))
		for _, input := range request.Statuses {
			created, err := h.Store.CreateStatus(r.Context(), workspaceID, userID, models.Status{Name: input.Name, Description: input.Description, Category: internalStatusCategory(input.StatusCategory), ProjectID: projectID})
			if err != nil {
				statusAPIError(w, err)
				return
			}
			out = append(out, h.jiraStatusBean(created))
		}
		writeJSON(w, http.StatusOK, out)
	case http.MethodPut:
		var request struct {
			Statuses []struct {
				ID, Name, Description, StatusCategory string
			} `json:"statuses"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || len(request.Statuses) == 0 {
			jiraError(w, http.StatusBadRequest, "At least one status is required.")
			return
		}
		for _, input := range request.Statuses {
			err := h.Store.UpdateStatus(r.Context(), workspaceID, userID, models.Status{ID: input.ID, Name: input.Name, Description: input.Description, Category: internalStatusCategory(input.StatusCategory)})
			if err != nil {
				statusAPIError(w, err)
				return
			}
		}
		w.WriteHeader(http.StatusNoContent)
	case http.MethodDelete:
		ids := r.URL.Query()["id"]
		if len(ids) == 0 {
			jiraError(w, http.StatusBadRequest, "At least one status id is required.")
			return
		}
		for _, id := range ids {
			if err := h.Store.DeleteStatus(r.Context(), workspaceID, userID, id); err != nil {
				statusAPIError(w, err)
				return
			}
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (h *Handler) statusDetailEndpoint(w http.ResponseWriter, r *http.Request, idOrName string) {
	workspaceID, _, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	statuses, err := h.Store.StatusesForWorkspace(r.Context(), workspaceID)
	if err != nil {
		statusAPIError(w, err)
		return
	}
	for _, status := range statuses {
		if status.ID == idOrName || strings.EqualFold(status.Name, idOrName) {
			writeJSON(w, http.StatusOK, h.statusBean(status))
			return
		}
	}
	jiraError(w, http.StatusNotFound, "The status does not exist.")
}

func (h *Handler) statusesByNameEndpoint(w http.ResponseWriter, r *http.Request) {
	workspaceID, _, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	names := r.URL.Query()["name"]
	if len(names) == 0 {
		jiraError(w, http.StatusBadRequest, "At least one status name is required.")
		return
	}
	wanted := make(map[string]bool, len(names))
	for _, name := range names {
		wanted[strings.ToLower(name)] = true
	}
	var statuses []models.Status
	var err error
	if projectID := r.URL.Query().Get("projectId"); projectID != "" {
		project, projectErr := h.Store.ProjectByIDOrKey(r.Context(), workspaceID, projectID)
		if projectErr != nil {
			jiraError(w, http.StatusBadRequest, "projectId does not identify a project in this workspace.")
			return
		}
		statuses, err = h.Store.StatusesForProject(r.Context(), workspaceID, project.ID, false)
	} else {
		statuses, err = h.Store.StatusesForWorkspace(r.Context(), workspaceID)
	}
	if err != nil {
		statusAPIError(w, err)
		return
	}
	out := make([]map[string]any, 0)
	for _, status := range statuses {
		if wanted[strings.ToLower(status.Name)] {
			out = append(out, h.jiraStatusBean(status))
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) searchStatusesEndpoint(w http.ResponseWriter, r *http.Request) {
	workspaceID, _, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	start, err := strconv.Atoi(defaultString(r.URL.Query().Get("startAt"), "0"))
	if err != nil || start < 0 {
		jiraError(w, http.StatusBadRequest, "startAt must be a non-negative integer.")
		return
	}
	max, err := strconv.Atoi(defaultString(r.URL.Query().Get("maxResults"), "200"))
	if err != nil || max < 1 || max > 1000 {
		jiraError(w, http.StatusBadRequest, "maxResults must be between 1 and 1000.")
		return
	}
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("searchString")))
	category := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("statusCategory")))
	var statuses []models.Status
	projectID := r.URL.Query().Get("projectId")
	if projectID != "" {
		project, projectErr := h.Store.ProjectByIDOrKey(r.Context(), workspaceID, projectID)
		if projectErr != nil {
			jiraError(w, http.StatusBadRequest, "projectId does not identify a project in this workspace.")
			return
		}
		includeGlobal := false
		if raw := r.URL.Query().Get("includeGlobalStatuses"); raw != "" {
			includeGlobal, err = strconv.ParseBool(raw)
			if err != nil {
				jiraError(w, http.StatusBadRequest, "includeGlobalStatuses must be true or false.")
				return
			}
		}
		statuses, err = h.Store.StatusesForProject(r.Context(), workspaceID, project.ID, includeGlobal)
	} else {
		statuses, err = h.Store.StatusesForWorkspace(r.Context(), workspaceID)
	}
	if err != nil {
		statusAPIError(w, err)
		return
	}
	filtered := statuses[:0]
	for _, status := range statuses {
		if query != "" && !strings.Contains(strings.ToLower(status.Name+" "+status.Description), query) {
			continue
		}
		if category != "" && jiraStatusCategory(status.Category) != category {
			continue
		}
		filtered = append(filtered, status)
	}
	sort.SliceStable(filtered, func(i, j int) bool { return strings.ToLower(filtered[i].Name) < strings.ToLower(filtered[j].Name) })
	total := len(filtered)
	if start > total {
		start = total
	}
	end := start + max
	if end > total {
		end = total
	}
	values := make([]map[string]any, 0, end-start)
	for _, status := range filtered[start:end] {
		values = append(values, h.jiraStatusBean(status))
	}
	self := h.BaseURL + "/rest/api/3/statuses/search"
	response := map[string]any{"self": self, "startAt": start, "maxResults": max, "total": total, "isLast": end == total, "values": values}
	if end < total {
		nextQuery := r.URL.Query()
		nextQuery.Set("startAt", strconv.Itoa(end))
		nextQuery.Set("maxResults", strconv.Itoa(max))
		response["nextPage"] = self + "?" + nextQuery.Encode()
	}
	writeJSON(w, http.StatusOK, response)
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func statusUsagePage(r *http.Request, values []string) ([]map[string]any, string, error) {
	max, err := strconv.Atoi(defaultString(r.URL.Query().Get("maxResults"), "50"))
	if err != nil || max < 1 || max > 1000 {
		return nil, "", errors.New("maxResults must be between 1 and 1000")
	}
	start := 0
	if token := r.URL.Query().Get("nextPageToken"); token != "" {
		raw, decodeErr := base64.RawURLEncoding.DecodeString(token)
		if decodeErr != nil {
			return nil, "", errors.New("nextPageToken is invalid")
		}
		start, err = strconv.Atoi(string(raw))
		if err != nil || start < 0 || start > len(values) {
			return nil, "", errors.New("nextPageToken is invalid")
		}
	}
	end := start + max
	if end > len(values) {
		end = len(values)
	}
	page := make([]map[string]any, 0, end-start)
	for _, id := range values[start:end] {
		page = append(page, map[string]any{"id": id})
	}
	next := ""
	if end < len(values) {
		next = base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(end)))
	}
	return page, next, nil
}

func (h *Handler) statusUsageEndpoint(w http.ResponseWriter, r *http.Request, parts []string) {
	workspaceID, _, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	if len(parts) < 2 {
		jiraError(w, http.StatusNotFound, "No resource found")
		return
	}
	statusID := parts[0]
	if _, err := h.Store.StatusUsage(r.Context(), workspaceID, statusID); err != nil {
		statusAPIError(w, err)
		return
	}
	var ids []string
	var err error
	container := ""
	response := map[string]any{"statusId": statusID}
	switch {
	case len(parts) == 2 && parts[1] == "projectUsages":
		ids, err = h.Store.StatusProjectUsages(r.Context(), workspaceID, statusID)
		container = "projects"
	case len(parts) == 2 && parts[1] == "workflowUsages":
		ids, err = h.Store.StatusWorkflowUsages(r.Context(), workspaceID, statusID)
		container = "workflows"
	case len(parts) == 4 && parts[1] == "project" && parts[3] == "issueTypeUsages":
		project, projectErr := h.Store.ProjectByIDOrKey(r.Context(), workspaceID, parts[2])
		if projectErr != nil {
			jiraError(w, http.StatusNotFound, "The project does not exist.")
			return
		}
		ids, err = h.Store.StatusProjectIssueTypeUsages(r.Context(), workspaceID, project.ID, statusID)
		container = "issueTypes"
		response["projectId"] = project.ID
	default:
		jiraError(w, http.StatusNotFound, "No resource found")
		return
	}
	if err != nil {
		statusAPIError(w, err)
		return
	}
	values, next, err := statusUsagePage(r, ids)
	if err != nil {
		jiraError(w, http.StatusBadRequest, err.Error())
		return
	}
	page := map[string]any{"values": values}
	if next != "" {
		page["nextPageToken"] = next
	}
	response[container] = page
	writeJSON(w, http.StatusOK, response)
}
