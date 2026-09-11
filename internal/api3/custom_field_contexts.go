package api3

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

func fieldContextError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrProjectPermission):
		jiraError(w, http.StatusForbidden, "Administrator privileges are required.")
	case errors.Is(err, store.ErrFieldContextConflict):
		jiraError(w, http.StatusConflict, err.Error())
	case errors.Is(err, store.ErrFieldContextValidation):
		jiraError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, store.ErrFieldContextNotFound):
		jiraError(w, http.StatusNotFound, "The custom field or context does not exist.")
	default:
		jiraError(w, http.StatusInternalServerError, "Could not complete the custom field context operation.")
	}
}

func (h *Handler) fieldContextBean(found *models.CustomFieldContext) map[string]any {
	return map[string]any{
		"id": wireNumericID(found.ID), "name": found.Name, "description": found.Description,
		"isGlobalContext": found.AllProjects, "isAnyIssueType": found.AllIssueTypes,
	}
}

// fieldContextRoute serves every /field/{fieldId}/context path.
func (h *Handler) fieldContextRoute(w http.ResponseWriter, r *http.Request, fieldID, rest string) {
	parts := []string{}
	if trimmed := strings.Trim(rest, "/"); trimmed != "" {
		parts = strings.Split(trimmed, "/")
	}
	switch {
	case len(parts) == 0:
		h.fieldContextCollection(w, r, fieldID)
	case len(parts) == 1 && parts[0] == "defaultValue":
		h.fieldContextDefaultValue(w, r, fieldID)
	case len(parts) == 1 && parts[0] == "defaultValues":
		h.fieldContextDefaultValues(w, r, fieldID)
	case len(parts) == 1 && parts[0] == "issuetypemapping":
		h.fieldContextIssueTypeMapping(w, r, fieldID)
	case len(parts) == 1 && parts[0] == "projectmapping":
		h.fieldContextProjectMapping(w, r, fieldID)
	case len(parts) == 1 && parts[0] == "mapping":
		h.fieldContextsForProjectsAndIssueTypes(w, r, fieldID)
	case len(parts) == 1:
		h.fieldContextResource(w, r, fieldID, parts[0])
	case len(parts) == 2 && parts[1] == "issuetype":
		h.changeFieldContextScope(w, r, fieldID, parts[0], "issuetype", false)
	case len(parts) == 3 && parts[1] == "issuetype" && parts[2] == "remove":
		h.changeFieldContextScope(w, r, fieldID, parts[0], "issuetype", true)
	case len(parts) == 2 && parts[1] == "project":
		h.changeFieldContextScope(w, r, fieldID, parts[0], "project", false)
	case len(parts) == 3 && parts[1] == "project" && parts[2] == "remove":
		h.changeFieldContextScope(w, r, fieldID, parts[0], "project", true)
	case len(parts) == 2 && parts[1] == "option":
		h.customFieldOptionCollection(w, r, fieldID, parts[0])
	case len(parts) == 3 && parts[1] == "option" && parts[2] == "move":
		h.moveCustomFieldOptions(w, r, fieldID, parts[0])
	case len(parts) == 3 && parts[1] == "option":
		h.deleteCustomFieldOption(w, r, fieldID, parts[0], parts[2], false)
	case len(parts) == 4 && parts[1] == "option" && parts[3] == "issue":
		h.deleteCustomFieldOption(w, r, fieldID, parts[0], parts[2], true)
	default:
		jiraError(w, http.StatusNotFound, "No resource found")
	}
}

func (h *Handler) fieldContextCollection(w http.ResponseWriter, r *http.Request, fieldID string) {
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
		contexts, err := h.Store.CustomFieldContexts(r.Context(), workspaceID, fieldID, securityQueryValues(r, "contextId"))
		if err != nil {
			fieldContextError(w, err)
			return
		}
		page := pageSlice(contexts, startAt, maxResults)
		values := make([]map[string]any, 0, len(page))
		for _, found := range page {
			values = append(values, h.fieldContextBean(found))
		}
		writeJSON(w, http.StatusOK, h.securityPageBean(r, values, len(contexts), startAt, maxResults))
	case http.MethodPost:
		var request struct {
			Name         string   `json:"name"`
			Description  string   `json:"description"`
			ProjectIDs   []string `json:"projectIds"`
			IssueTypeIDs []string `json:"issueTypeIds"`
		}
		if !decodeProjectRequest(w, r, &request) {
			return
		}
		found, err := h.Store.CreateCustomFieldContext(r.Context(), workspaceID, actorID, fieldID,
			request.Name, request.Description, request.ProjectIDs, request.IssueTypeIDs)
		if err != nil {
			fieldContextError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, h.fieldContextBean(found))
	default:
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (h *Handler) fieldContextResource(w http.ResponseWriter, r *http.Request, fieldID, contextID string) {
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
		if err := h.Store.UpdateCustomFieldContext(r.Context(), workspaceID, actorID, fieldID, contextID, request.Name, request.Description); err != nil {
			fieldContextError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case http.MethodDelete:
		if err := h.Store.DeleteCustomFieldContext(r.Context(), workspaceID, actorID, fieldID, contextID); err != nil {
			fieldContextError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (h *Handler) changeFieldContextScope(w http.ResponseWriter, r *http.Request, fieldID, contextID, kind string, remove bool) {
	wantMethod := http.MethodPut
	if remove {
		wantMethod = http.MethodPost
	}
	if r.Method != wantMethod {
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	workspaceID, actorID, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	var request struct {
		ProjectIDs   []string `json:"projectIds"`
		IssueTypeIDs []string `json:"issueTypeIds"`
	}
	if !decodeProjectRequest(w, r, &request) {
		return
	}
	projectIDs, issueTypeIDs := request.ProjectIDs, request.IssueTypeIDs
	if kind == "project" {
		issueTypeIDs = nil
	} else {
		projectIDs = nil
	}
	if len(projectIDs) == 0 && len(issueTypeIDs) == 0 {
		fieldContextError(w, store.ErrFieldContextValidation)
		return
	}
	if err := h.Store.ChangeCustomFieldContextScope(r.Context(), workspaceID, actorID, fieldID, contextID, projectIDs, issueTypeIDs, remove); err != nil {
		fieldContextError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) fieldContextDefaultValue(w http.ResponseWriter, r *http.Request, fieldID string) {
	workspaceID, actorID, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.writeFieldContextDefaults(w, r, workspaceID, fieldID)
	case http.MethodPut:
		var request struct {
			DefaultValues []struct {
				ContextID string          `json:"contextId"`
				Value     json.RawMessage `json:"value"`
			} `json:"defaultValues"`
		}
		if !decodeProjectRequest(w, r, &request) {
			return
		}
		for _, value := range request.DefaultValues {
			if value.ContextID == "" {
				fieldContextError(w, store.ErrFieldContextValidation)
				return
			}
			if err := h.Store.SetCustomFieldContextDefault(r.Context(), workspaceID, actorID, fieldID, value.ContextID, string(value.Value)); err != nil {
				fieldContextError(w, err)
				return
			}
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (h *Handler) fieldContextDefaultValues(w http.ResponseWriter, r *http.Request, fieldID string) {
	if r.Method != http.MethodGet {
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	workspaceID, _, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	h.writeFieldContextDefaults(w, r, workspaceID, fieldID)
}

func (h *Handler) writeFieldContextDefaults(w http.ResponseWriter, r *http.Request, workspaceID, fieldID string) {
	startAt, maxResults, err := notificationPage(r)
	if err != nil {
		jiraError(w, http.StatusBadRequest, "startAt and maxResults are invalid.")
		return
	}
	contexts, err := h.Store.CustomFieldContexts(r.Context(), workspaceID, fieldID, securityQueryValues(r, "contextId"))
	if err != nil {
		fieldContextError(w, err)
		return
	}
	values := []map[string]any{}
	for _, found := range contexts {
		if found.DefaultValue == "" {
			continue
		}
		values = append(values, map[string]any{
			"contextId": wireNumericID(found.ID), "value": json.RawMessage(found.DefaultValue)})
	}
	page := pageSlice(values, startAt, maxResults)
	writeJSON(w, http.StatusOK, h.securityPageBean(r, page, len(values), startAt, maxResults))
}

func (h *Handler) fieldContextIssueTypeMapping(w http.ResponseWriter, r *http.Request, fieldID string) {
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
	contexts, err := h.Store.CustomFieldContexts(r.Context(), workspaceID, fieldID, securityQueryValues(r, "contextId"))
	if err != nil {
		fieldContextError(w, err)
		return
	}
	values := []map[string]any{}
	for _, found := range contexts {
		if found.AllIssueTypes {
			values = append(values, map[string]any{
				"contextId": wireNumericID(found.ID), "isAnyIssueType": true})
			continue
		}
		for _, issueTypeID := range found.IssueTypeIDs {
			values = append(values, map[string]any{
				"contextId": wireNumericID(found.ID), "issueTypeId": issueTypeID})
		}
	}
	page := pageSlice(values, startAt, maxResults)
	writeJSON(w, http.StatusOK, h.securityPageBean(r, page, len(values), startAt, maxResults))
}

func (h *Handler) fieldContextProjectMapping(w http.ResponseWriter, r *http.Request, fieldID string) {
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
	contexts, err := h.Store.CustomFieldContexts(r.Context(), workspaceID, fieldID, securityQueryValues(r, "contextId"))
	if err != nil {
		fieldContextError(w, err)
		return
	}
	values := []map[string]any{}
	for _, found := range contexts {
		if found.AllProjects {
			values = append(values, map[string]any{
				"contextId": wireNumericID(found.ID), "isGlobalContext": true})
			continue
		}
		for _, projectID := range found.ProjectIDs {
			values = append(values, map[string]any{
				"contextId": wireNumericID(found.ID), "projectId": wireNumericID(projectID)})
		}
	}
	page := pageSlice(values, startAt, maxResults)
	writeJSON(w, http.StatusOK, h.securityPageBean(r, page, len(values), startAt, maxResults))
}

func (h *Handler) fieldContextsForProjectsAndIssueTypes(w http.ResponseWriter, r *http.Request, fieldID string) {
	if r.Method != http.MethodPost {
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	workspaceID, _, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	var request struct {
		Mappings []struct {
			ProjectID   string `json:"projectId"`
			IssueTypeID string `json:"issueTypeId"`
		} `json:"mappings"`
	}
	if !decodeProjectRequest(w, r, &request) {
		return
	}
	startAt, maxResults, err := notificationPage(r)
	if err != nil {
		jiraError(w, http.StatusBadRequest, "startAt and maxResults are invalid.")
		return
	}
	if _, err = h.Store.CustomFieldContexts(r.Context(), workspaceID, fieldID, nil); err != nil {
		fieldContextError(w, err)
		return
	}
	values := []map[string]any{}
	for _, mapping := range request.Mappings {
		found, lookupErr := h.Store.ApplicableCustomFieldContext(r.Context(), fieldID, mapping.ProjectID, mapping.IssueTypeID)
		if lookupErr != nil {
			fieldContextError(w, lookupErr)
			return
		}
		if found == nil {
			continue
		}
		values = append(values, map[string]any{
			"contextId": wireNumericID(found.ID), "projectId": wireNumericID(mapping.ProjectID),
			"issueTypeId": mapping.IssueTypeID,
		})
	}
	page := pageSlice(values, startAt, maxResults)
	writeJSON(w, http.StatusOK, h.securityPageBean(r, page, len(values), startAt, maxResults))
}
