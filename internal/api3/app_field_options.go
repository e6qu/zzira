package api3

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/e6qu/zzira/internal/store"
	"github.com/jackc/pgx/v5"
)

// appFieldDeselectLimit caps the work items one deselect touches, matching the
// bound Jira's other bulk operations carry.
const appFieldDeselectLimit = 1000

func appFieldOptionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrProjectPermission):
		jiraError(w, http.StatusForbidden, "Administrator privileges are required.")
	case errors.Is(err, store.ErrFieldContextConflict):
		jiraError(w, http.StatusConflict, err.Error())
	case errors.Is(err, store.ErrAppFieldOption):
		jiraError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, pgx.ErrNoRows):
		jiraError(w, http.StatusNotFound, "The issue field option does not exist.")
	default:
		jiraError(w, http.StatusInternalServerError, "Could not complete the issue field option operation.")
	}
}

func (h *Handler) appFieldOptionBean(option store.AppFieldOption) map[string]any {
	bean := map[string]any{"id": wireNumericID(option.ID), "value": option.Value}
	if len(option.Properties) > 0 && string(option.Properties) != "{}" {
		bean["properties"] = option.Properties
	}
	config := map[string]any{}
	if len(option.Scope) > 0 {
		if err := json.Unmarshal(option.Scope, &config); err != nil {
			config = map[string]any{}
		}
	}
	if option.NotSelectable {
		config["attributes"] = []string{"notSelectable"}
	}
	if len(config) > 0 {
		bean["config"] = config
	}
	return bean
}

// appFieldOptionRoute serves Jira's issue field option family, which manages
// the options of a select list an app provides.
func (h *Handler) appFieldOptionRoute(w http.ResponseWriter, r *http.Request, fieldKey string, rest []string) {
	switch {
	case len(rest) == 0 && r.Method == http.MethodGet:
		h.listAppFieldOptions(w, r, fieldKey)
	case len(rest) == 0 && r.Method == http.MethodPost:
		h.saveAppFieldOption(w, r, fieldKey, "")
	case len(rest) == 2 && rest[0] == "suggestions" && rest[1] == "edit" && r.Method == http.MethodGet:
		h.appFieldOptionSuggestions(w, r, fieldKey, true)
	case len(rest) == 2 && rest[0] == "suggestions" && rest[1] == "search" && r.Method == http.MethodGet:
		h.appFieldOptionSuggestions(w, r, fieldKey, false)
	case len(rest) == 1 && r.Method == http.MethodGet:
		h.readAppFieldOption(w, r, fieldKey, rest[0])
	case len(rest) == 1 && r.Method == http.MethodPut:
		h.saveAppFieldOption(w, r, fieldKey, rest[0])
	case len(rest) == 1 && r.Method == http.MethodDelete:
		h.deleteAppFieldOption(w, r, fieldKey, rest[0])
	case len(rest) == 2 && rest[1] == "issue" && r.Method == http.MethodDelete:
		h.deselectAppFieldOption(w, r, fieldKey, rest[0])
	default:
		jiraError(w, http.StatusNotFound, "No resource found")
	}
}

func (h *Handler) listAppFieldOptions(w http.ResponseWriter, r *http.Request, fieldKey string) {
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
	options, err := h.Store.AppFieldOptions(r.Context(), workspaceID, fieldKey)
	if err != nil {
		appFieldOptionError(w, err)
		return
	}
	h.writeAppFieldOptionPage(w, r, options, startAt, maxResults)
}

func (h *Handler) writeAppFieldOptionPage(w http.ResponseWriter, r *http.Request, options []store.AppFieldOption, startAt, maxResults int) {
	page := pageSlice(options, startAt, maxResults)
	values := make([]map[string]any, 0, len(page))
	for _, option := range page {
		values = append(values, h.appFieldOptionBean(option))
	}
	writeJSON(w, http.StatusOK, h.securityPageBean(r, values, len(options), startAt, maxResults))
}

func (h *Handler) readAppFieldOption(w http.ResponseWriter, r *http.Request, fieldKey, optionID string) {
	workspaceID, _, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	option, err := h.Store.AppFieldOptionByID(r.Context(), workspaceID, fieldKey, optionID)
	if err != nil {
		appFieldOptionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, h.appFieldOptionBean(option))
}

func (h *Handler) saveAppFieldOption(w http.ResponseWriter, r *http.Request, fieldKey, optionID string) {
	workspaceID, actorID, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	var request struct {
		Value      string          `json:"value"`
		Properties json.RawMessage `json:"properties"`
		Config     json.RawMessage `json:"config"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&request); err != nil {
		jiraError(w, http.StatusBadRequest, "Invalid request payload.")
		return
	}
	option, err := h.Store.SaveAppFieldOption(r.Context(), workspaceID, actorID, fieldKey, optionID,
		request.Value, request.Properties, request.Config)
	if err != nil {
		appFieldOptionError(w, err)
		return
	}
	status := http.StatusOK
	if optionID == "" {
		status = http.StatusCreated
	}
	writeJSON(w, status, h.appFieldOptionBean(option))
}

func (h *Handler) deleteAppFieldOption(w http.ResponseWriter, r *http.Request, fieldKey, optionID string) {
	workspaceID, actorID, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	if err := h.Store.DeleteAppFieldOption(r.Context(), workspaceID, actorID, fieldKey, optionID); err != nil {
		appFieldOptionError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) appFieldOptionSuggestions(w http.ResponseWriter, r *http.Request, fieldKey string, selectableOnly bool) {
	// Suggestions are what a contributor sees while filling in a form, so they
	// need ordinary access rather than administration.
	workspaceID, _, authErr := h.authWorkspace(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	startAt, maxResults, err := notificationPage(r)
	if err != nil {
		jiraError(w, http.StatusBadRequest, "startAt and maxResults are invalid.")
		return
	}
	options, err := h.Store.AppFieldOptionSuggestions(r.Context(), workspaceID, fieldKey,
		strings.TrimSpace(r.URL.Query().Get("projectId")), selectableOnly)
	if err != nil {
		appFieldOptionError(w, err)
		return
	}
	h.writeAppFieldOptionPage(w, r, options, startAt, maxResults)
}

// deselectAppFieldOption takes an option off the work items that carry it,
// optionally replacing it and optionally narrowed by a JQL query. Jira runs
// this in the background, and so does this: the work is queued as an ordinary
// bulk edit rather than a path of its own.
func (h *Handler) deselectAppFieldOption(w http.ResponseWriter, r *http.Request, fieldKey, optionID string) {
	workspaceID, actorID, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	replaceWith := strings.TrimSpace(r.URL.Query().Get("replaceWith"))
	if replaceWith == optionID {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{
			"replaceWith": "The replacement must be a different option."})
		return
	}
	if replaceWith != "" {
		replacement, err := h.Store.AppFieldOptionByID(r.Context(), workspaceID, fieldKey, replaceWith)
		if errors.Is(err, pgx.ErrNoRows) {
			jiraFieldError(w, http.StatusBadRequest, map[string]string{
				"replaceWith": "The replacement option is not on this select list."})
			return
		}
		if err != nil {
			appFieldOptionError(w, err)
			return
		}
		// An option nobody can choose is not a replacement: the write would be
		// refused per work item and the deselect would report success while
		// leaving every one of them on the option it was meant to remove.
		if replacement.NotSelectable {
			jiraFieldError(w, http.StatusBadRequest, map[string]string{
				"replaceWith": "The replacement option cannot be selected."})
			return
		}
	}
	// A JQL query narrows which work items are touched; without one every work
	// item carrying the option is.
	var narrowed []string
	if jqlText := strings.TrimSpace(r.URL.Query().Get("jql")); jqlText != "" {
		compiled, jerr := h.compileJQL(r.Context(), workspaceID, jqlText, actorID)
		if jerr != nil {
			writeJerr(w, jerr)
			return
		}
		issues, _, searchErr := h.Store.Search(r.Context(), workspaceID, actorID, compiled, appFieldDeselectLimit, 0)
		if searchErr != nil {
			jiraError(w, http.StatusBadRequest, "Error in the JQL Query: "+searchErr.Error())
			return
		}
		// An empty match must not read as "no filter at all", which would
		// deselect the option everywhere.
		narrowed = []string{""}
		for _, issue := range issues {
			narrowed = append(narrowed, issue.ID)
		}
	}
	items, fieldID, err := h.Store.AppFieldOptionIssues(r.Context(), workspaceID, fieldKey, optionID, narrowed)
	if err != nil {
		appFieldOptionError(w, err)
		return
	}
	// A deselect is an ordinary field write: set the option, or clear it when
	// no replacement was named.
	value := json.RawMessage(`null`)
	if replaceWith != "" {
		encoded, marshalErr := json.Marshal(replaceWith)
		if marshalErr != nil {
			appFieldOptionError(w, marshalErr)
			return
		}
		value = encoded
	}
	task, err := h.Store.EnqueueBulkEditTask(r.Context(), workspaceID, actorID, items,
		[]store.BulkIssueEditOperation{{FieldID: fieldID, Action: "SET", Value: value}})
	if err != nil {
		appFieldOptionError(w, err)
		return
	}
	self := h.BaseURL + "/rest/api/3/task/" + task.ID
	w.Header().Set("Location", self)
	writeJSON(w, http.StatusSeeOther, map[string]any{"self": self, "id": task.ID})
}
