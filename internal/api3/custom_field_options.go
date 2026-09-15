package api3

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/e6qu/zzira/internal/authz"
	"github.com/e6qu/zzira/internal/models"
)

func (h *Handler) customFieldOptionBean(option models.CustomFieldOption) map[string]any {
	bean := map[string]any{
		"id": wireNumericID(option.ID), "value": option.Value, "disabled": option.Disabled,
	}
	if option.ParentID != "" {
		bean["optionId"] = wireNumericID(option.ParentID)
	}
	return bean
}

// customFieldOptionResource serves /customFieldOption/{id}.
func (h *Handler) customFieldOptionResource(w http.ResponseWriter, r *http.Request, optionID string) {
	if r.Method != http.MethodGet {
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	workspaceID, userID, authErr := h.authWorkspace(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	option, err := h.Store.CustomFieldOption(r.Context(), workspaceID, optionID)
	if err != nil {
		fieldContextError(w, err)
		return
	}
	// Jira returns the option to administrators, and to callers who can
	// browse a project the field is used in where a field configuration shows
	// it; anyone else is told it does not exist.
	admin := false
	if userID != "" {
		if admin, err = authz.IsWorkspaceAdmin(r.Context(), h.Store, workspaceID, userID); err != nil {
			fieldContextError(w, err)
			return
		}
	}
	if !admin {
		projects, projectErr := h.Store.ProjectsWithPermissions(r.Context(), workspaceID, userID, []string{"BROWSE_PROJECTS"})
		if projectErr != nil {
			fieldContextError(w, projectErr)
			return
		}
		ids := make([]string, 0, len(projects))
		for _, project := range projects {
			ids = append(ids, project.ID)
		}
		visible, visibleErr := h.Store.CustomFieldOptionVisibleInProjects(r.Context(), workspaceID, option.ID, ids)
		if visibleErr != nil {
			fieldContextError(w, visibleErr)
			return
		}
		if !visible {
			jiraError(w, http.StatusNotFound, "The custom field option does not exist or you do not have permission to see it.")
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"self": h.BaseURL + "/rest/api/3/customFieldOption/" + option.ID, "value": option.Value})
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
		onlyOptions, parseErr := queryBool(r, "onlyOptions", false)
		if parseErr != nil {
			jiraError(w, http.StatusBadRequest, "onlyOptions must be true or false.")
			return
		}
		// optionId selects an option and its cascading children; onlyOptions
		// leaves the children out.
		optionID := strings.TrimSpace(r.URL.Query().Get("optionId"))
		matching := options[:0]
		for _, option := range options {
			if onlyOptions && option.ParentID != "" {
				continue
			}
			if optionID != "" && fmt.Sprint(h.customFieldOptionBean(option)["id"]) != optionID && option.ID != optionID && option.ParentID != optionID {
				continue
			}
			matching = append(matching, option)
		}
		options = matching
		page := pageSlice(options, startAt, maxResults)
		values := make([]map[string]any, 0, len(page))
		for _, option := range page {
			values = append(values, h.customFieldOptionBean(option))
		}
		writeJSON(w, http.StatusOK, h.securityPageBean(r, values, len(options), startAt, maxResults))
	case http.MethodPost:
		var request struct {
			Options []struct {
				Value    string          `json:"value"`
				Disabled bool            `json:"disabled"`
				OptionID json.RawMessage `json:"optionId"`
			} `json:"options"`
		}
		if !decodeProjectRequest(w, r, &request) {
			return
		}
		options := make([]models.CustomFieldOption, 0, len(request.Options))
		for _, option := range request.Options {
			parent := strings.Trim(string(option.OptionID), `"`)
			if parent == "null" {
				parent = ""
			}
			options = append(options, models.CustomFieldOption{Value: option.Value, Disabled: option.Disabled, ParentID: parent})
		}
		created, err := h.Store.CreateCustomFieldOptionsWithParents(r.Context(), workspaceID, actorID, fieldID, contextID, options)
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
