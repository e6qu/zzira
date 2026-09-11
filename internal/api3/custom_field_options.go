package api3

import (
	"net/http"
	"strings"

	"github.com/e6qu/zzira/internal/models"
)

func (h *Handler) customFieldOptionBean(option models.CustomFieldOption) map[string]any {
	return map[string]any{
		"id": wireNumericID(option.ID), "value": option.Value, "disabled": option.Disabled,
	}
}

// customFieldOptionResource serves /customFieldOption/{id}.
func (h *Handler) customFieldOptionResource(w http.ResponseWriter, r *http.Request, optionID string) {
	if r.Method != http.MethodGet {
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	workspaceID, _, authErr := h.authWorkspace(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	option, err := h.Store.CustomFieldOption(r.Context(), workspaceID, optionID)
	if err != nil {
		fieldContextError(w, err)
		return
	}
	bean := h.customFieldOptionBean(option)
	bean["self"] = h.BaseURL + "/rest/api/3/customFieldOption/" + option.ID
	writeJSON(w, http.StatusOK, bean)
}

func (h *Handler) customFieldOptionCollection(w http.ResponseWriter, r *http.Request, fieldID, contextID string) {
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
		options, err := h.Store.CustomFieldOptions(r.Context(), workspaceID, fieldID, contextID)
		if err != nil {
			fieldContextError(w, err)
			return
		}
		page := pageSlice(options, startAt, maxResults)
		values := make([]map[string]any, 0, len(page))
		for _, option := range page {
			values = append(values, h.customFieldOptionBean(option))
		}
		writeJSON(w, http.StatusOK, h.securityPageBean(r, values, len(options), startAt, maxResults))
	case http.MethodPost:
		var request struct {
			Options []struct {
				Value    string `json:"value"`
				Disabled bool   `json:"disabled"`
			} `json:"options"`
		}
		if !decodeProjectRequest(w, r, &request) {
			return
		}
		values := make([]string, 0, len(request.Options))
		for _, option := range request.Options {
			values = append(values, option.Value)
		}
		created, err := h.Store.CreateCustomFieldOptions(r.Context(), workspaceID, actorID, fieldID, contextID, values)
		if err != nil {
			fieldContextError(w, err)
			return
		}
		beans := make([]map[string]any, 0, len(created))
		for _, option := range created {
			beans = append(beans, h.customFieldOptionBean(option))
		}
		writeJSON(w, http.StatusOK, map[string]any{"options": beans})
	case http.MethodPut:
		var request struct {
			Options []struct {
				ID       string `json:"id"`
				Value    string `json:"value"`
				Disabled bool   `json:"disabled"`
			} `json:"options"`
		}
		if !decodeProjectRequest(w, r, &request) {
			return
		}
		options := make([]models.CustomFieldOption, 0, len(request.Options))
		for _, option := range request.Options {
			options = append(options, models.CustomFieldOption{
				ID: option.ID, Value: option.Value, Disabled: option.Disabled})
		}
		if err := h.Store.UpdateCustomFieldOptions(r.Context(), workspaceID, actorID, fieldID, contextID, options); err != nil {
			fieldContextError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (h *Handler) moveCustomFieldOptions(w http.ResponseWriter, r *http.Request, fieldID, contextID string) {
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
		CustomFieldOptionIDs []string `json:"customFieldOptionIds"`
		After                string   `json:"after"`
		Position             string   `json:"position"`
	}
	if !decodeProjectRequest(w, r, &request) {
		return
	}
	after := request.After
	if index := strings.LastIndex(after, "/"); index >= 0 {
		after = after[index+1:]
	}
	if err := h.Store.ReorderCustomFieldOptions(r.Context(), workspaceID, actorID, fieldID, contextID,
		request.CustomFieldOptionIDs, after, request.Position); err != nil {
		fieldContextError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// deleteCustomFieldOption serves both the plain delete and Jira's
// .../option/{id}/issue form, which replaces the option on work items first.
func (h *Handler) deleteCustomFieldOption(w http.ResponseWriter, r *http.Request, fieldID, contextID, optionID string, replaceOnIssues bool) {
	if r.Method != http.MethodDelete {
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	workspaceID, actorID, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	replacement := ""
	if replaceOnIssues {
		replacement = strings.TrimSpace(r.URL.Query().Get("replaceWith"))
	}
	if err := h.Store.DeleteCustomFieldOption(r.Context(), workspaceID, actorID, fieldID, contextID, optionID, replacement); err != nil {
		fieldContextError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
