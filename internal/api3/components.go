package api3

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
	"github.com/jackc/pgx/v5"
)

type componentRequest struct {
	Project       string  `json:"project"`
	ProjectID     string  `json:"projectId"`
	Name          *string `json:"name"`
	Description   *string `json:"description"`
	LeadAccountID *string `json:"leadAccountId"`
	AssigneeType  *string `json:"assigneeType"`
	AssigneeValid *bool   `json:"isAssigneeTypeValid"`
}

func componentError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrProjectPermission):
		jiraError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, pgx.ErrNoRows):
		jiraError(w, http.StatusNotFound, "The component does not exist.")
	case errors.Is(err, store.ErrComponentConflict):
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"name": err.Error()})
	case errors.Is(err, store.ErrComponentValidation):
		jiraError(w, http.StatusBadRequest, err.Error())
	default:
		jiraError(w, http.StatusInternalServerError, "Could not complete the component operation.")
	}
}

func (h *Handler) componentBean(r *http.Request, component *models.ProjectComponent, includeCount bool) map[string]any {
	projectID := any(component.ProjectID)
	if numeric, err := strconv.ParseInt(component.ProjectID, 10, 64); err == nil {
		projectID = numeric
	}
	bean := map[string]any{
		"id": component.ID, "name": component.Name, "description": component.Description,
		"project": component.ProjectKey, "projectId": projectID,
		"assigneeType": component.AssigneeType, "realAssigneeType": component.RealAssigneeType,
		"isAssigneeTypeValid": component.IsAssigneeTypeValid,
		"self":                h.BaseURL + "/rest/api/3/component/" + component.ID,
	}
	if includeCount {
		bean["issueCount"] = component.IssueCount
	}
	if component.LeadAccountID != "" {
		if lead, err := h.Store.UserByID(r.Context(), component.LeadAccountID); err == nil {
			bean["lead"] = h.userBean(lead)
		}
	}
	if component.RealAssigneeID != "" {
		if assignee, err := h.Store.UserByID(r.Context(), component.RealAssigneeID); err == nil {
			bean["assignee"], bean["realAssignee"] = h.userBean(assignee), h.userBean(assignee)
		}
	}
	return bean
}

func (h *Handler) componentCollection(w http.ResponseWriter, r *http.Request) {
	workspaceID, _, authErr := h.authWorkspace(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	for key := range r.URL.Query() {
		switch key {
		case "projectIds", "startAt", "maxResults", "orderBy", "query":
		default:
			jiraError(w, http.StatusBadRequest, "Unsupported component search parameter: "+key)
			return
		}
	}
	components, err := h.Store.Components(r.Context(), workspaceID, "", r.URL.Query().Get("query"), r.URL.Query().Get("orderBy"))
	if err != nil {
		componentError(w, err)
		return
	}
	projects := commaQuerySet(r, "projectIds")
	if len(projects) > 0 {
		filtered := components[:0]
		for _, component := range components {
			if querySetContains(projects, component.ProjectID) || querySetContains(projects, component.ProjectKey) {
				filtered = append(filtered, component)
			}
		}
		components = filtered
	}
	h.writeComponentPage(w, r, components, "/rest/api/3/component")
}

func (h *Handler) writeComponentPage(w http.ResponseWriter, r *http.Request, components []*models.ProjectComponent, endpoint string) {
	start, limit := 0, 50
	var err error
	if raw := r.URL.Query().Get("startAt"); raw != "" {
		start, err = strconv.Atoi(raw)
		if err != nil || start < 0 {
			jiraError(w, http.StatusBadRequest, "startAt must be a non-negative integer.")
			return
		}
	}
	if raw := r.URL.Query().Get("maxResults"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 100 {
			jiraError(w, http.StatusBadRequest, "maxResults must be between 1 and 100.")
			return
		}
	}
	total := len(components)
	if start > total {
		start = total
	}
	end := start + limit
	if end > total {
		end = total
	}
	values := make([]map[string]any, 0, end-start)
	for _, component := range components[start:end] {
		values = append(values, h.componentBean(r, component, true))
	}
	link := func(position int) string {
		q := r.URL.Query()
		q.Set("startAt", strconv.Itoa(position))
		q.Set("maxResults", strconv.Itoa(limit))
		return h.BaseURL + endpoint + "?" + q.Encode()
	}
	response := map[string]any{"self": link(start), "startAt": start, "maxResults": limit, "total": total, "isLast": end >= total, "values": values}
	if end < total {
		response["nextPage"] = link(end)
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) createComponent(w http.ResponseWriter, r *http.Request) {
	workspaceID, actorID, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	var request componentRequest
	if !decodeProjectRequest(w, r, &request) {
		return
	}
	project := request.Project
	if project == "" {
		project = request.ProjectID
	}
	name := ""
	if request.Name != nil {
		name = *request.Name
	}
	input := store.ComponentInput{ProjectIDOrKey: project, Name: name}
	if request.Description != nil {
		input.Description = *request.Description
	}
	if request.LeadAccountID != nil {
		input.LeadAccountID = *request.LeadAccountID
	}
	if request.AssigneeType != nil {
		input.AssigneeType = *request.AssigneeType
	}
	component, err := h.Store.CreateComponent(r.Context(), workspaceID, actorID, input)
	if err != nil {
		componentError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, h.componentBean(r, component, false))
}

func (h *Handler) componentResource(w http.ResponseWriter, r *http.Request, parts []string) {
	workspaceID, actorID, authErr := h.authWorkspace(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	if len(parts) == 2 && parts[1] == "relatedIssueCounts" && r.Method == http.MethodGet {
		component, err := h.Store.ComponentByID(r.Context(), workspaceID, parts[0])
		if err != nil {
			componentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"issueCount": component.IssueCount, "self": h.BaseURL + "/rest/api/3/component/" + component.ID})
		return
	}
	if len(parts) != 1 {
		jiraError(w, http.StatusNotFound, "No resource found")
		return
	}
	component, err := h.Store.ComponentByID(r.Context(), workspaceID, parts[0])
	if err != nil {
		componentError(w, err)
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, h.componentBean(r, component, false))
	case http.MethodPut:
		if _, _, adminErr := h.authWorkspaceAdmin(r); adminErr != nil {
			writeJerr(w, adminErr)
			return
		}
		var request componentRequest
		if !decodeProjectRequest(w, r, &request) {
			return
		}
		input := store.ComponentInput{Name: component.Name, Description: component.Description, LeadAccountID: component.LeadAccountID, AssigneeType: component.AssigneeType}
		if request.Name != nil {
			input.Name = *request.Name
		}
		if request.Description != nil {
			input.Description = *request.Description
		}
		if request.LeadAccountID != nil {
			input.LeadAccountID = *request.LeadAccountID
		}
		if request.AssigneeType != nil {
			input.AssigneeType = *request.AssigneeType
		}
		updated, updateErr := h.Store.UpdateComponent(r.Context(), workspaceID, actorID, component.ID, input)
		if updateErr != nil {
			componentError(w, updateErr)
			return
		}
		writeJSON(w, http.StatusOK, h.componentBean(r, updated, false))
	case http.MethodDelete:
		if _, _, adminErr := h.authWorkspaceAdmin(r); adminErr != nil {
			writeJerr(w, adminErr)
			return
		}
		if err := h.Store.DeleteComponent(r.Context(), workspaceID, actorID, component.ID, r.URL.Query().Get("moveIssuesTo")); err != nil {
			componentError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		jiraError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) projectComponents(w http.ResponseWriter, r *http.Request, projectIDOrKey string, paged bool) {
	workspaceID, _, authErr := h.authWorkspace(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	if _, err := h.Store.ProjectByIDOrKey(r.Context(), workspaceID, projectIDOrKey); err != nil {
		projectError(w, err)
		return
	}
	for key := range r.URL.Query() {
		if paged && (key == "startAt" || key == "maxResults" || key == "orderBy" || key == "query" || key == "componentSource") || !paged && key == "componentSource" {
			continue
		}
		jiraError(w, http.StatusBadRequest, "Unsupported component search parameter: "+key)
		return
	}
	if source := r.URL.Query().Get("componentSource"); source != "" && !strings.EqualFold(source, "jira") {
		jiraError(w, http.StatusBadRequest, "componentSource must be jira.")
		return
	}
	components, err := h.Store.Components(r.Context(), workspaceID, projectIDOrKey, r.URL.Query().Get("query"), r.URL.Query().Get("orderBy"))
	if err != nil {
		componentError(w, err)
		return
	}
	if paged {
		h.writeComponentPage(w, r, components, "/rest/api/3/project/"+url.PathEscape(projectIDOrKey)+"/component")
		return
	}
	values := make([]map[string]any, 0, len(components))
	for _, component := range components {
		values = append(values, h.componentBean(r, component, false))
	}
	writeJSON(w, http.StatusOK, values)
}
