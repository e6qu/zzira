package api3

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
)

func siteConfigurationError(w http.ResponseWriter, err error) {
	if errors.Is(err, commands.ErrSiteConfigurationValidation) {
		jiraError(w, http.StatusBadRequest, strings.TrimSpace(strings.TrimPrefix(err.Error(), commands.ErrSiteConfigurationValidation.Error()+":")))
		return
	}
	jiraError(w, http.StatusInternalServerError, "Could not update Jira configuration.")
}

func (h *Handler) siteAnnouncementBanner(w http.ResponseWriter, r *http.Request) {
	workspaceID, actorID, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	switch r.Method {
	case http.MethodGet:
		cfg, err := h.Store.JiraSiteConfiguration(r.Context(), workspaceID)
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "Could not load announcement banner.")
			return
		}
		writeJSON(w, http.StatusOK, cfg.Announcement)
	case http.MethodPut:
		var input models.AnnouncementBanner
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			jiraError(w, http.StatusBadRequest, "Request body must be a valid announcement banner configuration.")
			return
		}
		if err := h.Commands.UpdateAnnouncementBanner(r.Context(), workspaceID, actorID, input); err != nil {
			siteConfigurationError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		jiraError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) siteApplicationProperties(w http.ResponseWriter, r *http.Request, path string) {
	workspaceID, actorID, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	cfg, err := h.Store.JiraSiteConfiguration(r.Context(), workspaceID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "Could not load application properties.")
		return
	}
	if path == "/application-properties" || path == "/application-properties/advanced-settings" {
		if r.Method != http.MethodGet {
			jiraError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		properties := commands.JiraApplicationProperties(cfg.ApplicationProperties)
		if path == "/application-properties/advanced-settings" {
			filtered := properties[:0]
			for _, property := range properties {
				if property.Key != "jira.issuenav.criteria.autoupdate" {
					filtered = append(filtered, property)
				}
			}
			writeJSON(w, http.StatusOK, filtered)
			return
		}
		key := r.URL.Query().Get("key")
		if key != "" {
			for _, property := range properties {
				if property.Key == key {
					writeJSON(w, http.StatusOK, property)
					return
				}
			}
			jiraError(w, http.StatusNotFound, "Application property was not found.")
			return
		}
		filter := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("keyFilter")))
		if filter != "" {
			filtered := properties[:0]
			for _, property := range properties {
				if strings.Contains(strings.ToLower(property.Key), filter) {
					filtered = append(filtered, property)
				}
			}
			properties = filtered
		}
		writeJSON(w, http.StatusOK, properties)
		return
	}
	if r.Method != http.MethodPut {
		jiraError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	key, err := url.PathUnescape(strings.TrimPrefix(path, "/application-properties/"))
	if err != nil || key == "" || strings.Contains(key, "/") {
		jiraError(w, http.StatusNotFound, "Application property was not found.")
		return
	}
	var input struct {
		ID    string `json:"id"`
		Value string `json:"value"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		jiraError(w, http.StatusBadRequest, "Request body must contain an application property value.")
		return
	}
	if input.ID != "" && input.ID != key {
		jiraError(w, http.StatusBadRequest, "Application property id must match the request path.")
		return
	}
	if _, ok := commands.JiraApplicationPropertyDefinition(key); !ok {
		jiraError(w, http.StatusNotFound, "Application property was not found.")
		return
	}
	if err := h.Commands.UpdateApplicationProperty(r.Context(), workspaceID, actorID, key, input.Value); err != nil {
		siteConfigurationError(w, err)
		return
	}
	property, _ := commands.JiraApplicationPropertyDefinition(key)
	property.Value = input.Value
	writeJSON(w, http.StatusOK, property)
}

func (h *Handler) globalJiraConfiguration(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jiraError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	workspaceID, _, authErr := h.authWorkspace(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	cfg, err := h.Store.JiraSiteConfiguration(r.Context(), workspaceID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "Could not load Jira configuration.")
		return
	}
	result := map[string]any{
		"attachmentsEnabled": cfg.AttachmentsEnabled, "issueLinkingEnabled": cfg.IssueLinkingEnabled,
		"subTasksEnabled": cfg.SubTasksEnabled, "timeTrackingEnabled": cfg.TimeTrackingEnabled,
		"unassignedIssuesAllowed": cfg.UnassignedIssuesAllowed, "votingEnabled": cfg.VotingEnabled,
		"watchingEnabled": cfg.WatchingEnabled,
	}
	if cfg.TimeTrackingEnabled {
		result["timeTrackingConfiguration"] = cfg.TimeTracking
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) siteTimeTracking(w http.ResponseWriter, r *http.Request, path string) {
	workspaceID, actorID, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	cfg, err := h.Store.JiraSiteConfiguration(r.Context(), workspaceID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "Could not load time tracking configuration.")
		return
	}
	provider := models.TimeTrackingProvider{Key: "Jira", Name: "JIRA provided time tracking", URL: h.BaseURL + "/admin#admin-jira-configuration"}
	switch path {
	case "/configuration/timetracking/list":
		if r.Method != http.MethodGet {
			jiraError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		writeJSON(w, http.StatusOK, []models.TimeTrackingProvider{provider})
	case "/configuration/timetracking/options":
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, cfg.TimeTracking)
		case http.MethodPut:
			var input models.TimeTrackingConfiguration
			decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&input); err != nil {
				jiraError(w, http.StatusBadRequest, "Request body must contain all time tracking options.")
				return
			}
			if err := h.Commands.UpdateTimeTrackingOptions(r.Context(), workspaceID, actorID, input); err != nil {
				siteConfigurationError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, input)
		default:
			jiraError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	case "/configuration/timetracking":
		switch r.Method {
		case http.MethodGet:
			if !cfg.TimeTrackingEnabled {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			writeJSON(w, http.StatusOK, provider)
		case http.MethodPut:
			var input struct {
				Key  string `json:"key"`
				Name string `json:"name"`
			}
			decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&input); err != nil || input.Key == "" {
				jiraError(w, http.StatusBadRequest, "A time tracking provider key is required.")
				return
			}
			if err := h.Commands.SelectTimeTrackingProvider(r.Context(), workspaceID, actorID, input.Key); err != nil {
				siteConfigurationError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			jiraError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	}
}

var navigatorColumnLabels = map[string]string{"issuekey": "Key", "summary": "Summary", "description": "Description", "issuetype": "Work type", "priority": "Priority", "status": "Status", "assignee": "Assignee", "reporter": "Reporter", "created": "Created", "updated": "Updated", "fixVersions": "Fix versions", "versions": "Affects versions", "components": "Components", "labels": "Labels"}

func (h *Handler) issueNavigatorColumns(w http.ResponseWriter, r *http.Request) {
	workspaceID, actorID, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	switch r.Method {
	case http.MethodGet:
		cfg, err := h.Store.JiraSiteConfiguration(r.Context(), workspaceID)
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "Could not load issue navigator columns.")
			return
		}
		custom, err := h.Store.CustomFieldsForWorkspace(r.Context(), workspaceID)
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "Could not load issue navigator columns.")
			return
		}
		labels := map[string]string{}
		for key, label := range navigatorColumnLabels {
			labels[key] = label
		}
		for _, field := range custom {
			labels[field.ID] = field.Name
		}
		items := make([]models.ColumnItem, 0, len(cfg.NavigatorColumns))
		for _, value := range cfg.NavigatorColumns {
			items = append(items, models.ColumnItem{Label: labels[value], Value: value})
		}
		writeJSON(w, http.StatusOK, items)
	case http.MethodPut:
		if err := r.ParseMultipartForm(1 << 20); err != nil && !errors.Is(err, http.ErrNotMultipart) {
			jiraError(w, http.StatusBadRequest, "Could not parse issue navigator columns.")
			return
		}
		if err := r.ParseForm(); err != nil {
			jiraError(w, http.StatusBadRequest, "Could not parse issue navigator columns.")
			return
		}
		if err := h.Commands.UpdateNavigatorColumns(r.Context(), workspaceID, actorID, r.Form["columns"]); err != nil {
			siteConfigurationError(w, err)
			return
		}
		w.WriteHeader(http.StatusOK)
	default:
		jiraError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}
