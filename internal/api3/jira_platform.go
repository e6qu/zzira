package api3

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"

	"github.com/e6qu/zzira/internal/apps"
	"github.com/e6qu/zzira/internal/build"
	"github.com/e6qu/zzira/internal/jql"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

// jiraTimeLayout is the timestamp format Jira's platform APIs use.
const jiraTimeLayout = "2006-01-02T15:04:05.000-0700"

// serverStarted stands in for the build date of a build that records none.
var serverStarted = time.Now().UTC()

// ---- server information and labels ----

func versionNumbers(version string) []int {
	numbers := []int{}
	for _, part := range strings.FieldsFunc(version, func(r rune) bool { return !unicode.IsDigit(r) }) {
		if number, err := strconv.Atoi(part); err == nil {
			numbers = append(numbers, number)
		}
		if len(numbers) == 3 {
			break
		}
	}
	for len(numbers) < 3 {
		numbers = append(numbers, 0)
	}
	return numbers
}

func (h *Handler) serverInfo(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC()
	writeJSON(w, http.StatusOK, map[string]any{
		"baseUrl":                         h.BaseURL,
		"displayUrl":                      h.BaseURL,
		"displayUrlConfluence":            h.BaseURL + "/wiki",
		"displayUrlServicedeskHelpCenter": h.BaseURL + "/servicedesk/customer/portals",
		"version":                         build.Version,
		"versionNumbers":                  versionNumbers(build.Version),
		"deploymentType":                  "Cloud",
		"buildNumber":                     0,
		"buildDate":                       serverStarted.Format(jiraTimeLayout),
		"serverTime":                      now.Format(jiraTimeLayout),
		"serverTimeZone":                  "Etc/UTC",
		"scmInfo":                         build.Renderer,
		"serverTitle":                     build.Product,
	})
}

// pageRequest reads Jira's startAt and maxResults, capping maxResults.
func pageRequest(w http.ResponseWriter, r *http.Request, defaultMax, maximum int) (int, int, bool) {
	startAt, maxResults := 0, defaultMax
	if raw := r.URL.Query().Get("startAt"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			jiraError(w, http.StatusBadRequest, "startAt must be a non-negative integer.")
			return 0, 0, false
		}
		startAt = parsed
	}
	if raw := r.URL.Query().Get("maxResults"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			jiraError(w, http.StatusBadRequest, "maxResults must be a positive integer.")
			return 0, 0, false
		}
		maxResults = min(parsed, maximum)
	}
	return startAt, maxResults, true
}

// pageBean is Jira's PageBean around a slice of values already paged.
func (h *Handler) pageBean(r *http.Request, startAt, maxResults, total int, values any, count int) map[string]any {
	isLast := startAt+count >= total
	self := h.BaseURL + r.URL.Path + "?maxResults=" + strconv.Itoa(maxResults) + "&startAt=" + strconv.Itoa(startAt)
	bean := map[string]any{"self": self, "maxResults": maxResults, "startAt": startAt, "total": total, "isLast": isLast, "values": values}
	if !isLast {
		bean["nextPage"] = h.BaseURL + r.URL.Path + "?maxResults=" + strconv.Itoa(maxResults) + "&startAt=" + strconv.Itoa(startAt+count)
	}
	return bean
}

func (h *Handler) labelsEndpoint(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	startAt, maxResults, ok := pageRequest(w, r, 1000, 1000)
	if !ok {
		return
	}
	_, labels, err := h.Store.Labels(r.Context(), workspaceID, userID, "")
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if labels == nil {
		labels = []string{}
	}
	start := min(startAt, len(labels))
	end := min(start+maxResults, len(labels))
	writeJSON(w, http.StatusOK, h.pageBean(r, startAt, maxResults, len(labels), labels[start:end], end-start))
}

// ---- app properties ----

// connectClientKeyProperty is Jira's reserved, read-only app property holding
// the tenant's Connect client key.
const connectClientKeyProperty = "connect_client_key_019cdff3-8bfb-71fe-9628-875b700aebb8"

func operationMessage(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"message": message, "statusCode": status})
}

func (h *Handler) connectAddonProperties(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/rest/atlassian-connect/1/addons/"), "/")
	if len(parts) < 2 || len(parts) > 3 || parts[1] != "properties" || parts[0] == "" {
		operationMessage(w, http.StatusNotFound, "No resource found.")
		return
	}
	installation, ok := apps.InstallationFromContext(r.Context())
	if !ok || installation.Key != parts[0] {
		operationMessage(w, http.StatusUnauthorized, "Only the app whose key matches addonKey can access its properties.")
		return
	}
	base := h.BaseURL + "/rest/atlassian-connect/1/addons/" + parts[0] + "/properties/"
	if len(parts) == 2 || parts[2] == "" {
		if r.Method != http.MethodGet {
			operationMessage(w, http.StatusMethodNotAllowed, "Method not allowed.")
			return
		}
		h.writeAppPropertyKeys(w, r, installation, base)
		return
	}
	h.appProperty(w, r, installation, parts[2], true)
}

func (h *Handler) forgeAppProperties(w http.ResponseWriter, r *http.Request) {
	installation, ok := apps.InstallationFromContext(r.Context())
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if installation.Format == "connect" {
		writeJSON(w, http.StatusForbidden, nil)
		return
	}
	key := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, "/rest/forge/1/app/properties"), "/")
	if key == "" {
		if r.Method != http.MethodGet {
			operationMessage(w, http.StatusMethodNotAllowed, "Method not allowed.")
			return
		}
		h.writeAppPropertyKeys(w, r, installation, h.BaseURL+"/rest/forge/1/app/properties/")
		return
	}
	h.appProperty(w, r, installation, key, false)
}

func (h *Handler) writeAppPropertyKeys(w http.ResponseWriter, r *http.Request, installation *models.AppInstallation, base string) {
	keys, err := h.Store.JiraAppPropertyKeys(r.Context(), installation.ID)
	if err != nil {
		operationMessage(w, http.StatusInternalServerError, "The properties could not be read.")
		return
	}
	values := make([]map[string]string, 0, len(keys))
	for _, key := range keys {
		values = append(values, map[string]string{"key": key, "self": base + key})
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": values})
}

func (h *Handler) appProperty(w http.ResponseWriter, r *http.Request, installation *models.AppInstallation, key string, connect bool) {
	if err := store.ValidJiraAppProperty(key, nil, false); err != nil {
		operationMessage(w, http.StatusBadRequest, "The property key must be between 1 and 127 characters.")
		return
	}
	reserved := connect && key == connectClientKeyProperty
	switch r.Method {
	case http.MethodGet:
		if reserved {
			writeJSON(w, http.StatusOK, map[string]any{"key": key, "value": installation.ID})
			return
		}
		value, err := h.Store.JiraAppProperty(r.Context(), installation.ID, key)
		if errors.Is(err, pgx.ErrNoRows) {
			operationMessage(w, http.StatusNotFound, "The property was not found.")
			return
		}
		if err != nil {
			operationMessage(w, http.StatusInternalServerError, "The property could not be read.")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"key": key, "value": json.RawMessage(value)})
	case http.MethodPut:
		if reserved {
			operationMessage(w, http.StatusForbidden, "The property is reserved and read-only.")
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			operationMessage(w, http.StatusBadRequest, "The value must be at most 32768 characters.")
			return
		}
		created, err := h.Store.PutJiraAppProperty(r.Context(), installation.ID, key, body)
		if errors.Is(err, store.ErrJiraAppPropertyValidation) {
			operationMessage(w, http.StatusBadRequest, strings.TrimPrefix(err.Error(), store.ErrJiraAppPropertyValidation.Error()+": "))
			return
		}
		if err != nil {
			operationMessage(w, http.StatusInternalServerError, "The property could not be saved.")
			return
		}
		if created {
			operationMessage(w, http.StatusCreated, "Property created.")
			return
		}
		operationMessage(w, http.StatusOK, "Property updated.")
	case http.MethodDelete:
		if reserved {
			operationMessage(w, http.StatusForbidden, "The property is reserved and read-only.")
			return
		}
		deleted, err := h.Store.DeleteJiraAppProperty(r.Context(), installation.ID, key)
		if err != nil {
			operationMessage(w, http.StatusInternalServerError, "The property could not be deleted.")
			return
		}
		if !deleted {
			operationMessage(w, http.StatusNotFound, "The property was not found.")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		operationMessage(w, http.StatusMethodNotAllowed, "Method not allowed.")
	}
}

// ---- UI modifications ----

type uiModificationContextInput struct {
	ID            json.RawMessage `json:"id"`
	IsAvailable   json.RawMessage `json:"isAvailable"`
	ProjectID     *string         `json:"projectId"`
	IssueTypeID   *string         `json:"issueTypeId"`
	PortalID      *string         `json:"portalId"`
	RequestTypeID *string         `json:"requestTypeId"`
	ViewType      *string         `json:"viewType"`
}

func uiModificationContexts(inputs []uiModificationContextInput) ([]store.UIModificationContext, error) {
	contexts := make([]store.UIModificationContext, 0, len(inputs))
	for _, input := range inputs {
		viewType := ""
		if input.ViewType != nil {
			viewType = *input.ViewType
		}
		wildcards := 0
		for _, value := range []*string{input.ProjectID, input.IssueTypeID} {
			if value == nil {
				wildcards++
			}
		}
		switch viewType {
		case "JSMRequestCreate":
			if input.PortalID == nil || input.RequestTypeID == nil || input.ProjectID != nil || input.IssueTypeID != nil {
				return nil, fmt.Errorf("A JSMRequestCreate context needs portalId and requestTypeId and no projectId or issueTypeId.")
			}
		case "GICAgentView", "IssueViewAgentView", "IssueTransitionAgentView":
			if input.PortalID != nil {
				return nil, fmt.Errorf("An agent view context must not set portalId.")
			}
			if wildcards > 1 {
				return nil, fmt.Errorf("A UI modification context can have at most one wildcard.")
			}
		case "", "GIC", "IssueView", "IssueTransition":
			if input.PortalID != nil || input.RequestTypeID != nil {
				return nil, fmt.Errorf("A Jira context must not set portalId or requestTypeId.")
			}
			if viewType == "" {
				wildcards++
			}
			if wildcards > 1 {
				return nil, fmt.Errorf("A UI modification context can have at most one wildcard.")
			}
		default:
			return nil, fmt.Errorf("The view type %s is not supported.", viewType)
		}
		contexts = append(contexts, store.UIModificationContext{
			ProjectID: input.ProjectID, IssueTypeID: input.IssueTypeID, PortalID: input.PortalID, RequestTypeID: input.RequestTypeID, ViewType: input.ViewType,
		})
	}
	return contexts, nil
}

func (h *Handler) uiModificationsRoute(w http.ResponseWriter, r *http.Request, path string) {
	workspaceID, _, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	installation, ok := apps.InstallationFromContext(r.Context())
	if !ok || installation.Format == "connect" {
		jiraError(w, http.StatusForbidden, "UI modifications can only be managed by Forge apps.")
		return
	}
	id := strings.TrimPrefix(strings.TrimPrefix(path, "/uiModifications"), "/")
	switch {
	case id == "" && r.Method == http.MethodGet:
		startAt, maxResults, ok := pageRequest(w, r, 50, 100)
		if !ok {
			return
		}
		expand := map[string]bool{}
		for _, option := range strings.Split(r.URL.Query().Get("expand"), ",") {
			expand[strings.TrimSpace(option)] = true
		}
		modifications, total, err := h.Store.UIModifications(r.Context(), workspaceID, installation.ID, startAt, maxResults)
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "internal error")
			return
		}
		values := make([]map[string]any, 0, len(modifications))
		for _, modification := range modifications {
			bean := map[string]any{"id": modification.ID, "name": modification.Name, "description": modification.Description, "self": h.BaseURL + "/rest/api/3/uiModifications/" + modification.ID}
			if expand["data"] && modification.Data != nil {
				bean["data"] = *modification.Data
			}
			if expand["contexts"] {
				contexts := make([]map[string]any, 0, len(modification.Contexts))
				for _, context := range modification.Contexts {
					contexts = append(contexts, map[string]any{
						"id": context.ID, "projectId": context.ProjectID, "issueTypeId": context.IssueTypeID, "portalId": context.PortalID,
						"requestTypeId": context.RequestTypeID, "viewType": context.ViewType, "isAvailable": context.IsAvailable,
					})
				}
				bean["contexts"] = contexts
			}
			values = append(values, bean)
		}
		writeJSON(w, http.StatusOK, h.pageBean(r, startAt, maxResults, total, values, len(values)))
	case id == "" && r.Method == http.MethodPost:
		var request struct {
			Name        string                       `json:"name"`
			Description string                       `json:"description"`
			Data        *string                      `json:"data"`
			Contexts    []uiModificationContextInput `json:"contexts"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			jiraError(w, http.StatusBadRequest, "Invalid request payload.")
			return
		}
		if problem := uiModificationProblem(&request.Name, request.Data); problem != "" {
			jiraError(w, http.StatusBadRequest, problem)
			return
		}
		contexts, err := uiModificationContexts(request.Contexts)
		if err != nil {
			jiraError(w, http.StatusBadRequest, err.Error())
			return
		}
		created, err := h.Store.CreateUIModification(r.Context(), workspaceID, installation.ID, store.UIModification{
			Name: request.Name, Description: request.Description, Data: request.Data, Contexts: contexts,
		})
		if errors.Is(err, store.ErrUIModificationValidation) {
			jiraError(w, http.StatusBadRequest, strings.TrimPrefix(err.Error(), store.ErrUIModificationValidation.Error()+": "))
			return
		}
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "internal error")
			return
		}
		writeJSON(w, http.StatusCreated, map[string]string{"id": created, "self": h.BaseURL + "/rest/api/3/uiModifications/" + created})
	case id != "" && r.Method == http.MethodPut:
		var raw map[string]json.RawMessage
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&raw); err != nil {
			jiraError(w, http.StatusBadRequest, "Invalid request payload.")
			return
		}
		update := store.UIModificationUpdate{}
		for field, value := range raw {
			var err error
			switch field {
			case "name":
				err = json.Unmarshal(value, &update.Name)
			case "description":
				err = json.Unmarshal(value, &update.Description)
			case "data":
				update.SetData = true
				err = json.Unmarshal(value, &update.Data)
			case "contexts":
				var inputs []uiModificationContextInput
				if err = json.Unmarshal(value, &inputs); err == nil {
					var contexts []store.UIModificationContext
					if contexts, err = uiModificationContexts(inputs); err != nil {
						jiraError(w, http.StatusBadRequest, err.Error())
						return
					}
					update.Contexts = &contexts
				}
			default:
				err = fmt.Errorf("unrecognized field")
			}
			if err != nil {
				jiraError(w, http.StatusBadRequest, "Invalid request payload.")
				return
			}
		}
		if update.Name != nil || update.SetData {
			if problem := uiModificationProblem(update.Name, update.Data); problem != "" {
				jiraError(w, http.StatusBadRequest, problem)
				return
			}
		}
		err := h.Store.UpdateUIModification(r.Context(), workspaceID, installation.ID, id, update)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			jiraError(w, http.StatusNotFound, "The UI modification was not found.")
		case errors.Is(err, store.ErrUIModificationValidation):
			jiraError(w, http.StatusBadRequest, strings.TrimPrefix(err.Error(), store.ErrUIModificationValidation.Error()+": "))
		case err != nil:
			jiraError(w, http.StatusInternalServerError, "internal error")
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	case id != "" && r.Method == http.MethodDelete:
		err := h.Store.DeleteUIModification(r.Context(), workspaceID, installation.ID, id)
		if errors.Is(err, pgx.ErrNoRows) {
			jiraError(w, http.StatusNotFound, "The UI modification was not found.")
			return
		}
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "internal error")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		jiraError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func uiModificationProblem(name *string, data *string) string {
	if name != nil && (strings.TrimSpace(*name) == "" || len([]rune(*name)) > 255) {
		return "The UI modification name is required and must be at most 255 characters."
	}
	if data != nil && len([]rune(*data)) > 50000 {
		return "The UI modification data must be at most 50000 characters."
	}
	return ""
}

// ---- webhooks ----

var dynamicWebhookEvents = map[string]bool{
	"jira:issue_created": true, "jira:issue_updated": true, "jira:issue_deleted": true,
	"comment_created": true, "comment_updated": true, "comment_deleted": true,
	"issue_property_set": true, "issue_property_deleted": true,
	"sprint_created": true, "sprint_updated": true, "sprint_closed": true, "sprint_deleted": true, "sprint_started": true,
	"jira:version_released": true, "jira:version_unreleased": true, "jira:version_created": true, "jira:version_moved": true,
	"jira:version_updated": true, "jira:version_merged": true, "jira:version_deleted": true,
}

func decodeWebhookIDs(w http.ResponseWriter, r *http.Request) ([]int64, bool) {
	var request struct {
		WebhookIDs []int64 `json:"webhookIds"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&request); err != nil || request.WebhookIDs == nil {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"webhookIds": "The list of webhook IDs is missing."})
		return nil, false
	}
	return request.WebhookIDs, true
}

func (h *Handler) dynamicWebhookRoute(w http.ResponseWriter, r *http.Request, path string) {
	workspaceID, _, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	installation, ok := apps.InstallationFromContext(r.Context())
	if !ok {
		jiraError(w, http.StatusForbidden, "Only Connect and OAuth 2.0 apps can use this operation.")
		return
	}
	switch {
	case path == "/webhook" && r.Method == http.MethodGet:
		startAt, maxResults, ok := pageRequest(w, r, 100, 100)
		if !ok {
			return
		}
		webhooks, total, err := h.Store.AppWebhooks(r.Context(), workspaceID, installation.ID, startAt, maxResults)
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "internal error")
			return
		}
		values := make([]map[string]any, 0, len(webhooks))
		for _, webhook := range webhooks {
			bean := map[string]any{"id": webhook.JiraID, "jqlFilter": webhook.JQL, "events": webhook.Events, "url": webhook.URL}
			if webhook.ExpiresAt != nil {
				bean["expirationDate"] = webhook.ExpiresAt.UnixMilli()
			}
			if len(webhook.FieldIDs) > 0 {
				bean["fieldIdsFilter"] = webhook.FieldIDs
			}
			if len(webhook.PropertyKeys) > 0 {
				bean["issuePropertyKeysFilter"] = webhook.PropertyKeys
			}
			values = append(values, bean)
		}
		writeJSON(w, http.StatusOK, h.pageBean(r, startAt, maxResults, total, values, len(values)))
	case path == "/webhook" && r.Method == http.MethodPost:
		h.registerDynamicWebhooks(w, r, workspaceID, installation)
	case path == "/webhook" && r.Method == http.MethodDelete:
		ids, ok := decodeWebhookIDs(w, r)
		if !ok {
			return
		}
		if err := h.Store.DeleteAppWebhooks(r.Context(), workspaceID, installation.ID, ids); err != nil {
			jiraError(w, http.StatusInternalServerError, "internal error")
			return
		}
		w.WriteHeader(http.StatusAccepted)
	case path == "/webhook/refresh" && r.Method == http.MethodPut:
		ids, ok := decodeWebhookIDs(w, r)
		if !ok {
			return
		}
		expiration, err := h.Store.RefreshAppWebhooks(r.Context(), workspaceID, installation.ID, ids)
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "internal error")
			return
		}
		writeJSON(w, http.StatusOK, map[string]int64{"expirationDate": expiration.UnixMilli()})
	case path == "/webhook/failed" && r.Method == http.MethodGet:
		if installation.Format != "connect" {
			jiraError(w, http.StatusForbidden, "Only Connect apps can use this operation.")
			return
		}
		maxResults := 100
		if raw := r.URL.Query().Get("maxResults"); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed < 1 {
				jiraError(w, http.StatusBadRequest, "maxResults must be a positive integer.")
				return
			}
			maxResults = min(parsed, 100)
		}
		after := time.UnixMilli(0)
		if raw := r.URL.Query().Get("after"); raw != "" {
			parsed, err := strconv.ParseInt(raw, 10, 64)
			if err != nil {
				jiraError(w, http.StatusBadRequest, "after must be milliseconds since the UNIX epoch.")
				return
			}
			after = time.UnixMilli(parsed)
		}
		failed, err := h.Store.FailedAppWebhooks(r.Context(), workspaceID, installation.ID, after, maxResults)
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "internal error")
			return
		}
		values := make([]map[string]any, 0, len(failed))
		for _, webhook := range failed {
			values = append(values, map[string]any{"id": webhook.ID, "body": webhook.Body, "url": webhook.URL, "failureTime": webhook.FailureTime.UnixMilli()})
		}
		response := map[string]any{"maxResults": maxResults, "values": values}
		if len(failed) > 0 {
			response["next"] = fmt.Sprintf("%s/rest/api/3/webhook/failed?maxResults=%d&after=%d", h.BaseURL, maxResults, failed[len(failed)-1].FailureTime.UnixMilli())
		}
		writeJSON(w, http.StatusOK, response)
	case path == "/webhook" || path == "/webhook/refresh" || path == "/webhook/failed":
		jiraError(w, http.StatusMethodNotAllowed, "method not allowed")
	default:
		jiraError(w, http.StatusNotFound, "No resource found")
	}
}

func (h *Handler) registerDynamicWebhooks(w http.ResponseWriter, r *http.Request, workspaceID string, installation *models.AppInstallation) {
	var request struct {
		URL      string `json:"url"`
		Webhooks []struct {
			Events                  []string `json:"events"`
			FieldIDsFilter          []string `json:"fieldIdsFilter"`
			IssuePropertyKeysFilter []string `json:"issuePropertyKeysFilter"`
			JQLFilter               string   `json:"jqlFilter"`
		} `json:"webhooks"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		jiraError(w, http.StatusBadRequest, "Invalid request payload.")
		return
	}
	if strings.TrimSpace(request.URL) == "" || len(request.Webhooks) == 0 {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"url": "A webhook URL and at least one webhook are required."})
		return
	}
	if installation.Format == "connect" && installation.BaseURL != "" && !strings.HasPrefix(request.URL, strings.TrimRight(installation.BaseURL, "/")) {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"url": "The URL must use the same base URL as the Connect app."})
		return
	}
	results := make([]map[string]any, 0, len(request.Webhooks))
	for _, spec := range request.Webhooks {
		problems := []string{}
		if len(spec.Events) == 0 {
			problems = append(problems, "At least one event is required.")
		}
		for _, event := range spec.Events {
			if !dynamicWebhookEvents[event] {
				problems = append(problems, "The event "+event+" is not supported.")
			}
		}
		if strings.TrimSpace(spec.JQLFilter) == "" {
			problems = append(problems, "The JQL filter is required.")
		} else if _, err := jql.Parse(spec.JQLFilter); err != nil {
			problems = append(problems, "The JQL filter is invalid: "+err.Error())
		}
		if len(problems) > 0 {
			results = append(results, map[string]any{"errors": problems})
			continue
		}
		id, err := h.Store.RegisterAppWebhook(r.Context(), workspaceID, installation.ID, request.URL, store.DynamicWebhookSpec{
			Events: spec.Events, JQL: spec.JQLFilter, FieldIDs: spec.FieldIDsFilter, PropertyKeys: spec.IssuePropertyKeysFilter,
		})
		if errors.Is(err, store.ErrWebhookValidation) {
			results = append(results, map[string]any{"errors": []string{strings.TrimPrefix(err.Error(), store.ErrWebhookValidation.Error()+": ")}})
			continue
		}
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "internal error")
			return
		}
		results = append(results, map[string]any{"createdWebhookId": id})
	}
	writeJSON(w, http.StatusOK, map[string]any{"webhookRegistrationResult": results})
}

// adminWebhookRoute serves the administrator webhooks of Jira's
// /rest/webhooks/1.0/webhook resource.
func (h *Handler) adminWebhookRoute(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, e := h.authWorkspaceAdmin(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	rest, found := strings.CutPrefix(r.URL.Path, "/rest/webhooks/1.0/webhook")
	if !found {
		jiraError(w, http.StatusNotFound, "No resource found")
		return
	}
	id := strings.Trim(rest, "/")
	bean := func(webhook *models.Webhook) map[string]any {
		return map[string]any{
			"self": h.BaseURL + "/rest/webhooks/1.0/webhook/" + strconv.FormatInt(webhook.JiraID, 10), "name": webhook.Name, "url": webhook.URL,
			"excludeBody": webhook.ExcludeBody, "filters": map[string]string{"issue-related-events-section": webhook.JQL},
			"events": webhook.Events, "enabled": webhook.Active, "lastUpdated": webhook.UpdatedAt.UnixMilli(),
			"lastUpdatedUser": webhook.UpdatedBy, "lastUpdatedDisplayName": webhook.UpdatedByName,
		}
	}
	decodeInput := func() (store.AdminWebhookInput, bool) {
		var request struct {
			Name        string            `json:"name"`
			URL         string            `json:"url"`
			Events      []string          `json:"events"`
			Filters     map[string]string `json:"filters"`
			ExcludeBody bool              `json:"excludeBody"`
			Enabled     *bool             `json:"enabled"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&request); err != nil {
			jiraError(w, http.StatusBadRequest, "Invalid request payload.")
			return store.AdminWebhookInput{}, false
		}
		query := request.Filters["issue-related-events-section"]
		if strings.TrimSpace(query) != "" {
			if _, err := jql.Parse(query); err != nil {
				jiraFieldError(w, http.StatusBadRequest, map[string]string{"filters": jql.QueryMessage(err.Error())})
				return store.AdminWebhookInput{}, false
			}
		}
		if request.Events == nil {
			request.Events = []string{}
		}
		enabled := request.Enabled == nil || *request.Enabled
		return store.AdminWebhookInput{Name: request.Name, URL: request.URL, Events: request.Events, JQL: query, ExcludeBody: request.ExcludeBody, Enabled: enabled}, true
	}
	writeError := func(err error) {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			jiraError(w, http.StatusNotFound, "The webhook does not exist.")
		case errors.Is(err, store.ErrWebhookValidation):
			jiraError(w, http.StatusBadRequest, strings.TrimPrefix(err.Error(), store.ErrWebhookValidation.Error()+": "))
		default:
			jiraError(w, http.StatusInternalServerError, "internal error")
		}
	}
	switch {
	case id == "" && r.Method == http.MethodGet:
		webhooks, err := h.Store.AdminWebhooks(r.Context(), workspaceID)
		if err != nil {
			writeError(err)
			return
		}
		values := make([]map[string]any, 0, len(webhooks))
		for _, webhook := range webhooks {
			values = append(values, bean(webhook))
		}
		writeJSON(w, http.StatusOK, values)
	case id == "" && r.Method == http.MethodPost:
		input, ok := decodeInput()
		if !ok {
			return
		}
		webhook, err := h.Store.CreateAdminWebhook(r.Context(), workspaceID, userID, input)
		if err != nil {
			writeError(err)
			return
		}
		writeJSON(w, http.StatusCreated, bean(webhook))
	case id != "" && r.Method == http.MethodGet:
		webhook, err := h.Store.AdminWebhook(r.Context(), workspaceID, id)
		if err != nil {
			writeError(err)
			return
		}
		writeJSON(w, http.StatusOK, bean(webhook))
	case id != "" && r.Method == http.MethodPut:
		input, ok := decodeInput()
		if !ok {
			return
		}
		webhook, err := h.Store.UpdateAdminWebhook(r.Context(), workspaceID, userID, id, input)
		if err != nil {
			writeError(err)
			return
		}
		writeJSON(w, http.StatusOK, bean(webhook))
	case id != "" && r.Method == http.MethodDelete:
		if err := h.Store.DeleteAdminWebhook(r.Context(), workspaceID, id); err != nil {
			writeError(err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		jiraError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// ---- data classification and data policy ----

func classificationLevelBean(level models.DataClassificationLevel) map[string]any {
	return map[string]any{
		"id": level.ID, "status": level.Status, "name": level.Name, "rank": level.Rank,
		"description": level.Description, "guideline": level.Guideline, "color": level.Color,
	}
}

func (h *Handler) classificationLevelsEndpoint(w http.ResponseWriter, r *http.Request) {
	workspaceID, _, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	statuses := map[string]bool{}
	for _, value := range r.URL.Query()["status"] {
		for _, status := range strings.Split(value, ",") {
			status = strings.TrimSpace(status)
			if status != "PUBLISHED" && status != "ARCHIVED" && status != "DRAFT" {
				jiraError(w, http.StatusBadRequest, "The status must be PUBLISHED, ARCHIVED or DRAFT.")
				return
			}
			statuses[status] = true
		}
	}
	orderBy := r.URL.Query().Get("orderBy")
	if orderBy != "" && orderBy != "rank" && orderBy != "+rank" && orderBy != "-rank" {
		jiraError(w, http.StatusBadRequest, "orderBy must be rank, +rank or -rank.")
		return
	}
	all, err := h.Store.DataClassificationLevels(r.Context(), workspaceID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	levels := []models.DataClassificationLevel{}
	for _, level := range all {
		if len(statuses) == 0 || statuses[level.Status] {
			levels = append(levels, level)
		}
	}
	if orderBy != "" {
		sort.SliceStable(levels, func(i, j int) bool {
			if orderBy == "-rank" {
				return levels[i].Rank > levels[j].Rank
			}
			return levels[i].Rank < levels[j].Rank
		})
	}
	beans := make([]map[string]any, 0, len(levels))
	for _, level := range levels {
		beans = append(beans, classificationLevelBean(level))
	}
	writeJSON(w, http.StatusOK, map[string]any{"classifications": beans})
}

func (h *Handler) workspaceDataPolicy(w http.ResponseWriter, r *http.Request) {
	if _, _, e := h.authWorkspace(r); e != nil {
		writeJerr(w, e)
		return
	}
	if _, ok := apps.InstallationFromContext(r.Context()); !ok {
		jiraError(w, http.StatusForbidden, "The client is not authorized to make the request.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"anyContentBlocked": false})
}

func (h *Handler) projectDataPolicies(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	if _, ok := apps.InstallationFromContext(r.Context()); !ok {
		jiraError(w, http.StatusForbidden, "The client is not authorized to make the request.")
		return
	}
	refs := []string{}
	for _, value := range strings.Split(r.URL.Query().Get("ids"), ",") {
		if value = strings.TrimSpace(value); value != "" {
			refs = append(refs, value)
		}
	}
	if len(refs) == 0 || len(refs) > 50 {
		jiraError(w, http.StatusBadRequest, "Give between 1 and 50 project IDs.")
		return
	}
	policies := make([]map[string]any, 0, len(refs))
	for _, ref := range refs {
		id, err := strconv.ParseInt(ref, 10, 64)
		project, projectErr := h.Store.ProjectByIDOrKey(r.Context(), workspaceID, ref)
		if err != nil || projectErr != nil {
			jiraError(w, http.StatusBadRequest, "The project identifier "+ref+" is invalid or not permitted.")
			return
		}
		if allowed, permErr := h.Store.HasProjectPermission(r.Context(), workspaceID, userID, project.ID, "", "BROWSE_PROJECTS"); permErr != nil || !allowed {
			jiraError(w, http.StatusBadRequest, "The project identifier "+ref+" is invalid or not permitted.")
			return
		}
		policies = append(policies, map[string]any{"id": id, "dataPolicy": map[string]bool{"anyContentBlocked": false}})
	}
	writeJSON(w, http.StatusOK, map[string]any{"projectDataPolicies": policies})
}

// ---- licensing ----

// jiraApplicationID maps a site product to the Jira application it licenses.
var jiraApplicationID = map[string]string{"jira-software": "jira-software", "jira-service-management": "jira-servicedesk"}

func (h *Handler) instanceLicense(w http.ResponseWriter, r *http.Request) {
	workspaceID, _, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	products, err := h.Store.SiteProductKeys(r.Context(), workspaceID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	applications := []map[string]string{}
	for _, product := range products {
		if id, ok := jiraApplicationID[product]; ok {
			applications = append(applications, map[string]string{"id": id, "plan": "PAID"})
		}
	}
	if len(applications) == 0 {
		applications = append(applications, map[string]string{"id": "jira-software", "plan": "PAID"})
	}
	writeJSON(w, http.StatusOK, map[string]any{"applications": applications})
}

func (h *Handler) approximateLicenseCount(w http.ResponseWriter, r *http.Request, applicationKey string) {
	workspaceID, _, e := h.authWorkspaceAdmin(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	count, err := h.Store.ActiveMemberCount(r.Context(), workspaceID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if applicationKey == "" {
		writeJSON(w, http.StatusOK, map[string]string{"key": "jira", "value": strconv.Itoa(count)})
		return
	}
	switch applicationKey {
	case "jira-core", "jira-product-discovery", "jira-software", "jira-servicedesk":
	default:
		jiraError(w, http.StatusBadRequest, "The application key "+applicationKey+" is not valid.")
		return
	}
	products, err := h.Store.SiteProductKeys(r.Context(), workspaceID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	licensed := applicationKey == "jira-core"
	for _, product := range products {
		if jiraApplicationID[product] == applicationKey {
			licensed = true
		}
	}
	if len(products) == 0 && applicationKey == "jira-software" {
		licensed = true
	}
	value := "0"
	if licensed {
		value = strconv.Itoa(count)
	}
	writeJSON(w, http.StatusOK, map[string]string{"key": applicationKey, "value": value})
}

// ---- audit records ----

func parseAuditTime(raw string) (*time.Time, error) {
	if raw == "" {
		return nil, nil
	}
	if millis, err := strconv.ParseInt(raw, 10, 64); err == nil {
		value := time.UnixMilli(millis)
		return &value, nil
	}
	for _, layout := range []string{time.RFC3339Nano, jiraTimeLayout, "2006-01-02T15:04:05-0700", "2006-01-02T15:04", "2006-01-02"} {
		if value, err := time.Parse(layout, raw); err == nil {
			return &value, nil
		}
	}
	return nil, fmt.Errorf("invalid time")
}

func auditCategory(targetType string) string {
	switch {
	case strings.HasPrefix(targetType, "workflow"), targetType == "status":
		return "workflows"
	case targetType == "filter":
		return "filters"
	case strings.HasPrefix(targetType, "project"):
		return "projects"
	case targetType == "user":
		return "user management"
	case targetType == "group":
		return "group management"
	case strings.Contains(targetType, "permission"):
		return "permissions"
	case targetType == "app":
		return "apps"
	case strings.HasPrefix(targetType, "identity"):
		return "security"
	case strings.HasPrefix(targetType, "service"):
		return "service management"
	case strings.HasPrefix(targetType, "wiki"):
		return "confluence"
	}
	return "system"
}

func auditSummary(action string) string {
	words := strings.FieldsFunc(action, func(r rune) bool { return r == '.' || r == '_' || r == '-' })
	summary := strings.Join(words, " ")
	if summary == "" {
		return action
	}
	return strings.ToUpper(summary[:1]) + summary[1:]
}

func (h *Handler) auditRecords(w http.ResponseWriter, r *http.Request) {
	workspaceID, _, e := h.authWorkspaceAdmin(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	query := r.URL.Query()
	offset, limit := 0, 1000
	if raw := query.Get("offset"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			jiraError(w, http.StatusBadRequest, "offset must be a non-negative integer.")
			return
		}
		offset = parsed
	}
	if raw := query.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			jiraError(w, http.StatusBadRequest, "limit must be a positive integer.")
			return
		}
		limit = min(parsed, 1000)
	}
	from, err := parseAuditTime(query.Get("from"))
	if err != nil {
		jiraError(w, http.StatusBadRequest, "from must be a date and time.")
		return
	}
	to, err := parseAuditTime(query.Get("to"))
	if err != nil {
		jiraError(w, http.StatusBadRequest, "to must be a date and time.")
		return
	}
	records, total, err := h.Store.JiraAuditRecords(r.Context(), workspaceID, strings.Fields(query.Get("filter")), from, to, offset, limit)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	beans := make([]map[string]any, 0, len(records))
	for _, record := range records {
		name := record.TargetID
		if value, ok := record.Detail["name"].(string); ok && value != "" {
			name = value
		}
		remoteAddress, _ := record.Detail["ip"].(string)
		description, _ := record.Detail["description"].(string)
		beans = append(beans, map[string]any{
			"id": record.ID, "summary": auditSummary(record.Action), "remoteAddress": remoteAddress, "authorKey": record.ActorID,
			"created": record.CreatedAt.UTC().Format(jiraTimeLayout), "category": auditCategory(record.TargetType), "eventSource": "",
			"description": description, "objectItem": map[string]any{"id": record.TargetID, "name": name, "typeName": record.TargetType},
			"changedValues": []any{}, "associatedItems": []any{},
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"offset": offset, "limit": limit, "total": total, "records": beans})
}

// ---- project statuses, hierarchy and classification ----

func (h *Handler) projectPlatformRoute(w http.ResponseWriter, r *http.Request, path string) {
	workspaceID, userID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	parts := strings.Split(strings.TrimPrefix(path, "/project/"), "/")
	project, err := h.Store.ProjectByIDOrKey(r.Context(), workspaceID, parts[0])
	notFound := func() { jiraError(w, http.StatusNotFound, "No project could be found with key or id "+parts[0]+".") }
	if err != nil {
		notFound()
		return
	}
	if allowed, permErr := h.Store.HasProjectPermission(r.Context(), workspaceID, userID, project.ID, "", "BROWSE_PROJECTS"); permErr != nil || !allowed {
		notFound()
		return
	}
	sub := strings.Join(parts[1:], "/")
	switch {
	case sub == "statuses" && r.Method == http.MethodGet:
		h.projectStatuses(w, r, workspaceID, project)
	case sub == "hierarchy" && r.Method == http.MethodGet:
		if _, err := strconv.ParseInt(parts[0], 10, 64); err != nil {
			jiraError(w, http.StatusBadRequest, "The project ID must be a number.")
			return
		}
		h.projectHierarchy(w, r, workspaceID, project)
	case sub == "classification-config" && r.Method == http.MethodGet:
		levelID, err := h.Store.ProjectDefaultClassification(r.Context(), workspaceID, project.ID)
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "internal error")
			return
		}
		levels, err := h.Store.DataClassificationLevels(r.Context(), workspaceID)
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "internal error")
			return
		}
		permitted := make([]map[string]any, 0, len(levels))
		var projectDefault any
		for _, level := range levels {
			if level.Status == "PUBLISHED" {
				permitted = append(permitted, classificationLevelBean(level))
			}
			if level.ID == levelID {
				projectDefault = classificationLevelBean(level)
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"permittedClassificationLevels": permitted, "projectDefaultClassificationLevel": projectDefault,
			"organizationDefaultClassificationLevel": nil, "containerOverrideEnabled": true,
		})
	case sub == "classification-level/default":
		h.projectDefaultClassification(w, r, workspaceID, userID, project)
	default:
		jiraError(w, http.StatusNotFound, "No resource found")
	}
}

func (h *Handler) projectDefaultClassification(w http.ResponseWriter, r *http.Request, workspaceID, userID string, project *models.Project) {
	if r.Method == http.MethodGet {
		levelID, err := h.Store.ProjectDefaultClassification(r.Context(), workspaceID, project.ID)
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if levelID != "" {
			level, err := h.Store.DataClassificationLevel(r.Context(), workspaceID, levelID)
			if err == nil {
				writeJSON(w, http.StatusOK, classificationLevelBean(level))
				return
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				jiraError(w, http.StatusInternalServerError, "internal error")
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{})
		return
	}
	if r.Method != http.MethodPut && r.Method != http.MethodDelete {
		jiraError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	admin, err := h.Store.HasProjectPermission(r.Context(), workspaceID, userID, project.ID, "", "ADMINISTER_PROJECTS")
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if !admin {
		jiraError(w, http.StatusUnauthorized, "You do not have permission to change the project's classification.")
		return
	}
	levelID := ""
	if r.Method == http.MethodPut {
		var request struct {
			ID string `json:"id"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&request); err != nil || request.ID == "" {
			jiraFieldError(w, http.StatusBadRequest, map[string]string{"id": "The classification level id is required."})
			return
		}
		if err := h.Store.PublishedDataClassificationLevel(r.Context(), workspaceID, request.ID); err != nil {
			jiraFieldError(w, http.StatusBadRequest, map[string]string{"id": "The classification level does not exist or is not published."})
			return
		}
		levelID = request.ID
	}
	if err := h.Store.SetProjectDefaultClassification(r.Context(), workspaceID, project.ID, levelID); err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) projectStatuses(w http.ResponseWriter, r *http.Request, workspaceID string, project *models.Project) {
	issueTypes, err := h.Store.ProjectIssueTypes(r.Context(), workspaceID, project.ID, nil)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	result := make([]map[string]any, 0, len(issueTypes))
	for _, issueType := range issueTypes {
		flow, err := h.Store.WorkflowForProjectAndIssueType(r.Context(), project.ID, issueType.ID)
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "internal error")
			return
		}
		references := flow.StatusIDs()
		statuses := []map[string]any{}
		for _, reference := range references {
			status, err := h.Store.StatusByIDForProject(r.Context(), reference, project.ID)
			if err != nil {
				continue
			}
			statuses = append(statuses, h.statusBean(status))
		}
		id := strconv.FormatInt(issueType.JiraID, 10)
		result = append(result, map[string]any{
			"self": h.BaseURL + "/rest/api/3/issuetype/" + id, "id": id, "name": issueType.Name, "subtask": issueType.Subtask, "statuses": statuses,
		})
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) projectHierarchy(w http.ResponseWriter, r *http.Request, workspaceID string, project *models.Project) {
	issueTypes, err := h.Store.ProjectIssueTypes(r.Context(), workspaceID, project.ID, nil)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	levels, err := h.Store.HierarchyLevels(r.Context(), workspaceID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	hierarchy := []map[string]any{}
	for _, level := range levels {
		types := []map[string]any{}
		for _, issueType := range issueTypes {
			if issueType.HierarchyLevel == level.Level {
				types = append(types, map[string]any{"id": issueType.JiraID, "name": issueType.Name, "avatarId": issueType.AvatarID})
			}
		}
		if len(types) > 0 {
			hierarchy = append(hierarchy, map[string]any{"level": level.Level, "name": level.Name, "issueTypes": types})
		}
	}
	projectID, _ := strconv.ParseInt(project.ID, 10, 64)
	writeJSON(w, http.StatusOK, map[string]any{"projectId": projectID, "hierarchy": hierarchy})
}

// ---- internal worklog keys ----

func (h *Handler) internalWorklogBulk(w http.ResponseWriter, r *http.Request) {
	workspaceID, _, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	if r.Method != http.MethodPost {
		jiraError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var request struct {
		Requests []store.WorklogKey `json:"requests"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&request); err != nil || len(request.Requests) == 0 || len(request.Requests) > 1000 {
		jiraError(w, http.StatusBadRequest, "Give between 1 and 1000 issue and worklog ID pairs.")
		return
	}
	found, err := h.Store.ExistingWorklogKeys(r.Context(), workspaceID, request.Requests)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"worklogs": found})
}
