package api3

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

func siteConfigurationError(w http.ResponseWriter, err error) {
	if errors.Is(err, commands.ErrSiteConfigurationNotFound) {
		jiraError(w, http.StatusNotFound, strings.TrimSpace(strings.TrimPrefix(err.Error(), commands.ErrSiteConfigurationNotFound.Error()+":")))
		return
	}
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
		// Cloud administrators are its system administrators, and no editable
		// property is kept for system administrators alone.
		switch r.URL.Query().Get("permissionLevel") {
		case "", "ADMIN", "SYSADMIN":
		case "SYSADMIN_ONLY":
			properties = properties[:0]
		default:
			jiraError(w, http.StatusBadRequest, "permissionLevel must be ADMIN, SYSADMIN or SYSADMIN_ONLY.")
			return
		}
		// keyFilter is a regular expression the whole key must match, so
		// jira.lf.* selects the look and feel properties.
		if filter := r.URL.Query().Get("keyFilter"); filter != "" {
			pattern, compileErr := regexp.Compile("^(?:" + filter + ")$")
			if compileErr != nil {
				jiraError(w, http.StatusBadRequest, "keyFilter must be a valid regular expression.")
				return
			}
			filtered := properties[:0]
			for _, property := range properties {
				if pattern.MatchString(property.Key) {
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
		"subTasksEnabled": cfg.SubTasksEnabled, "parallelSprintsEnabled": cfg.ParallelSprintsEnabled, "timeTrackingEnabled": cfg.TimeTrackingEnabled,
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
	installed, err := h.Store.TimeTrackingProviders(r.Context(), workspaceID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "Could not load time tracking providers.")
		return
	}
	// Jira's provider is configured on the site administration page; an app's
	// provider on the admin page its descriptor names, when it names one.
	providers := make([]models.TimeTrackingProvider, 0, len(installed))
	provider := models.TimeTrackingProvider{}
	for _, item := range installed {
		bean := models.TimeTrackingProvider{Key: item.Key, Name: item.Name}
		switch {
		case item.Key == store.JiraTimeTrackingProviderKey:
			bean.URL = h.BaseURL + "/admin#admin-jira-configuration"
		case item.AdminPageKey != "":
			bean.URL = h.BaseURL + "/plugins/servlet/ac/" + item.AppKey + "/" + item.AdminPageKey
		}
		providers = append(providers, bean)
		if item.Key == cfg.TimeTrackingProvider || (provider.Key == "" && item.Key == store.JiraTimeTrackingProviderKey) {
			provider = bean
		}
	}
	switch path {
	case "/configuration/timetracking/list":
		if r.Method != http.MethodGet {
			jiraError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		writeJSON(w, http.StatusOK, providers)
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
		labels, err := h.Store.NavigableColumnLabels(r.Context(), workspaceID)
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "Could not load issue navigator columns.")
			return
		}
		items := make([]models.ColumnItem, 0, len(cfg.NavigatorColumns))
		for _, value := range cfg.NavigatorColumns {
			items = append(items, models.ColumnItem{Label: labels[value], Value: value})
		}
		writeJSON(w, http.StatusOK, items)
	case http.MethodPut:
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		if err := r.ParseForm(); err != nil {
			jiraError(w, http.StatusBadRequest, "Could not parse issue navigator columns.")
			return
		}
		if strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "multipart/form-data") {
			if err := r.ParseMultipartForm(1 << 20); err != nil { // #nosec G120 -- MaxBytesReader caps the complete body above.
				jiraError(w, http.StatusBadRequest, "Could not parse issue navigator columns.")
				return
			}
		}
		columns := r.Form["columns"]
		if columns == nil {
			columns = []string{}
		}
		if err := h.Commands.UpdateNavigatorColumns(r.Context(), workspaceID, actorID, columns); err != nil {
			siteConfigurationError(w, err)
			return
		}
		w.WriteHeader(http.StatusOK)
	default:
		jiraError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}
