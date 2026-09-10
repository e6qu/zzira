package api3

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

type notificationRecipientRequest struct {
	NotificationType string `json:"notificationType"`
	Parameter        string `json:"parameter"`
}

type notificationEventRequest struct {
	Event struct {
		ID string `json:"id"`
	} `json:"event"`
	Notifications []notificationRecipientRequest `json:"notifications"`
}

type createNotificationSchemeRequest struct {
	Name                     string                     `json:"name"`
	Description              string                     `json:"description"`
	NotificationSchemeEvents []notificationEventRequest `json:"notificationSchemeEvents"`
}

func isNotificationSchemePath(path string) bool {
	return path == "/notificationscheme" || path == "/notificationscheme/project" ||
		strings.HasPrefix(path, "/notificationscheme/") ||
		(strings.HasPrefix(path, "/project/") && strings.HasSuffix(path, "/notificationscheme"))
}

func notificationSchemeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrProjectPermission):
		jiraError(w, http.StatusForbidden, "Administrator privileges are required.")
	case errors.Is(err, store.ErrNotificationSchemeValidation), errors.Is(err, store.ErrNotificationSchemeConflict):
		jiraError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, store.ErrNotificationSchemeNotFound):
		jiraError(w, http.StatusNotFound, "The notification scheme, notification, or project does not exist.")
	default:
		jiraError(w, http.StatusInternalServerError, "Could not complete the notification scheme operation.")
	}
}

func parsePositiveInt64(raw string) (int64, error) {
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return 0, store.ErrNotificationSchemeValidation
	}
	return value, nil
}

func notificationSchemeExpand(r *http.Request) (bool, error) {
	raw := strings.TrimSpace(r.URL.Query().Get("expand"))
	if raw == "" {
		return false, nil
	}
	expand := false
	for _, value := range strings.Split(raw, ",") {
		switch strings.TrimSpace(value) {
		case "notificationSchemeEvents", "user", "group", "projectRole", "field", "all":
			expand = true
		case "":
		default:
			return false, store.ErrNotificationSchemeValidation
		}
	}
	return expand, nil
}

func notificationInputs(events []notificationEventRequest) ([]store.NotificationEntryInput, error) {
	inputs := []store.NotificationEntryInput{}
	for _, event := range events {
		eventID, err := parsePositiveInt64(event.Event.ID)
		if err != nil {
			return nil, err
		}
		for _, notification := range event.Notifications {
			inputs = append(inputs, store.NotificationEntryInput{EventID: eventID, NotificationType: notification.NotificationType, Parameter: notification.Parameter})
		}
	}
	return inputs, nil
}

func (h *Handler) eventNotificationBean(entry models.NotificationSchemeEntry) map[string]any {
	bean := map[string]any{"id": entry.ID, "notificationType": entry.NotificationType}
	if entry.Parameter != "" {
		bean["parameter"] = entry.Parameter
	}
	if entry.Recipient != "" {
		bean["recipient"] = entry.Recipient
	}
	if entry.NotificationType == "EmailAddress" {
		bean["emailAddress"] = entry.Parameter
	}
	return bean
}

func (h *Handler) notificationSchemeBean(scheme *models.NotificationScheme, expanded bool, projectIDs []int64) map[string]any {
	bean := map[string]any{
		"id": scheme.ID, "name": scheme.Name, "description": scheme.Description,
		"self":  h.BaseURL + "/rest/api/3/notificationscheme/" + strconv.FormatInt(scheme.ID, 10),
		"scope": map[string]any{"type": "PROJECT"},
	}
	if projectIDs != nil {
		bean["projects"] = projectIDs
	}
	if expanded {
		events := []map[string]any{}
		for _, configured := range scheme.Events {
			definition, _ := store.NotificationEvent(configured.EventID)
			notifications := make([]map[string]any, 0, len(configured.Notifications))
			for _, entry := range configured.Notifications {
				notifications = append(notifications, h.eventNotificationBean(entry))
			}
			events = append(events, map[string]any{
				"event":         map[string]any{"id": configured.EventID, "name": definition.Name, "description": definition.Description},
				"notifications": notifications,
			})
		}
		bean["notificationSchemeEvents"] = events
		bean["expand"] = "notificationSchemeEvents,user,group,projectRole,field,all"
	}
	return bean
}

func notificationPage(r *http.Request) (int, int, error) {
	startAt, maxResults := 0, 50
	var err error
	if raw := r.URL.Query().Get("startAt"); raw != "" {
		startAt, err = strconv.Atoi(raw)
		if err != nil || startAt < 0 {
			return 0, 0, store.ErrNotificationSchemeValidation
		}
	}
	if raw := r.URL.Query().Get("maxResults"); raw != "" {
		maxResults, err = strconv.Atoi(raw)
		if err != nil || maxResults < 1 || maxResults > 100 {
			return 0, 0, store.ErrNotificationSchemeValidation
		}
	}
	return startAt, maxResults, nil
}

func pageSlice[T any](values []T, startAt, maxResults int) []T {
	if startAt >= len(values) {
		return []T{}
	}
	end := startAt + maxResults
	if end > len(values) {
		end = len(values)
	}
	return values[startAt:end]
}

func int64QuerySet(values []string) (map[int64]bool, error) {
	set := map[int64]bool{}
	for _, raw := range values {
		value, err := parsePositiveInt64(raw)
		if err != nil {
			return nil, err
		}
		set[value] = true
	}
	return set, nil
}

func stringQuerySet(values []string) map[string]bool {
	set := map[string]bool{}
	for _, value := range values {
		if value != "" {
			set[value] = true
		}
	}
	return set
}

func (h *Handler) notificationSchemeRoute(w http.ResponseWriter, r *http.Request, path string) {
	if strings.HasPrefix(path, "/project/") {
		h.projectNotificationSchemeRoute(w, r, path)
		return
	}
	if path == "/notificationscheme/project" {
		h.notificationSchemeMappings(w, r)
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(path, "/notificationscheme"), "/"), "/")
	if len(parts) == 1 && parts[0] == "" {
		h.notificationSchemeCollection(w, r)
		return
	}
	if len(parts) < 1 || len(parts) > 3 || parts[0] == "" {
		jiraError(w, http.StatusNotFound, "No resource found")
		return
	}
	schemeID, err := parsePositiveInt64(parts[0])
	if err != nil {
		notificationSchemeError(w, err)
		return
	}
	if len(parts) >= 2 {
		if parts[1] != "notification" {
			jiraError(w, http.StatusNotFound, "No resource found")
			return
		}
		h.notificationEntryRoute(w, r, schemeID, parts[2:])
		return
	}
	expanded, err := notificationSchemeExpand(r)
	if err != nil {
		notificationSchemeError(w, err)
		return
	}
	switch r.Method {
	case http.MethodGet:
		workspaceID, _, authErr := h.authWorkspaceAdmin(r)
		if authErr != nil {
			writeJerr(w, authErr)
			return
		}
		scheme, getErr := h.Store.NotificationScheme(r.Context(), workspaceID, schemeID, expanded)
		if getErr != nil {
			notificationSchemeError(w, getErr)
			return
		}
		writeJSON(w, http.StatusOK, h.notificationSchemeBean(scheme, expanded, nil))
	case http.MethodPut:
		workspaceID, actorID, authErr := h.authWorkspaceAdmin(r)
		if authErr != nil {
			writeJerr(w, authErr)
			return
		}
		var request struct {
			Name        *string `json:"name"`
			Description *string `json:"description"`
		}
		if !decodeProjectRequest(w, r, &request) {
			return
		}
		if err = h.Store.UpdateNotificationScheme(r.Context(), workspaceID, actorID, schemeID, request.Name, request.Description); err != nil {
			notificationSchemeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case http.MethodDelete:
		workspaceID, actorID, authErr := h.authWorkspaceAdmin(r)
		if authErr != nil {
			writeJerr(w, authErr)
			return
		}
		if err = h.Store.DeleteNotificationScheme(r.Context(), workspaceID, actorID, schemeID); err != nil {
			notificationSchemeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (h *Handler) notificationSchemeCollection(w http.ResponseWriter, r *http.Request) {
	workspaceID, actorID, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	if r.Method == http.MethodPost {
		var request createNotificationSchemeRequest
		if !decodeProjectRequest(w, r, &request) {
			return
		}
		inputs, err := notificationInputs(request.NotificationSchemeEvents)
		if err != nil {
			notificationSchemeError(w, err)
			return
		}
		scheme, err := h.Store.CreateNotificationScheme(r.Context(), workspaceID, actorID, request.Name, request.Description, inputs)
		if err != nil {
			notificationSchemeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]string{"id": strconv.FormatInt(scheme.ID, 10)})
		return
	}
	if r.Method != http.MethodGet {
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	expanded, err := notificationSchemeExpand(r)
	if err != nil {
		notificationSchemeError(w, err)
		return
	}
	startAt, maxResults, err := notificationPage(r)
	if err != nil {
		notificationSchemeError(w, err)
		return
	}
	schemes, err := h.Store.NotificationSchemes(r.Context(), workspaceID, expanded)
	if err != nil {
		notificationSchemeError(w, err)
		return
	}
	idFilter, err := int64QuerySet(r.URL.Query()["id"])
	if err != nil {
		notificationSchemeError(w, err)
		return
	}
	projectFilter := stringQuerySet(r.URL.Query()["projectId"])
	onlyDefault := false
	switch r.URL.Query().Get("onlyDefault") {
	case "", "false":
	case "true":
		onlyDefault = true
	default:
		notificationSchemeError(w, store.ErrNotificationSchemeValidation)
		return
	}
	mappings, err := h.Store.NotificationSchemeMappings(r.Context(), workspaceID)
	if err != nil {
		notificationSchemeError(w, err)
		return
	}
	projectsByScheme := map[int64][]int64{}
	projectSchemes := map[string]bool{}
	for _, mapping := range mappings {
		if id, parseErr := strconv.ParseInt(mapping.ProjectID, 10, 64); parseErr == nil {
			projectsByScheme[mapping.SchemeID] = append(projectsByScheme[mapping.SchemeID], id)
		}
		if projectFilter[mapping.ProjectID] {
			projectSchemes[strconv.FormatInt(mapping.SchemeID, 10)] = true
		}
	}
	filtered := []*models.NotificationScheme{}
	for _, scheme := range schemes {
		if len(idFilter) > 0 && !idFilter[scheme.ID] {
			continue
		}
		if onlyDefault && !scheme.Default {
			continue
		}
		if len(projectFilter) > 0 && !projectSchemes[strconv.FormatInt(scheme.ID, 10)] {
			continue
		}
		filtered = append(filtered, scheme)
	}
	page := pageSlice(filtered, startAt, maxResults)
	values := make([]map[string]any, 0, len(page))
	for _, scheme := range page {
		values = append(values, h.notificationSchemeBean(scheme, expanded, projectsByScheme[scheme.ID]))
	}
	response := map[string]any{"startAt": startAt, "maxResults": maxResults, "total": len(filtered), "isLast": startAt+len(page) >= len(filtered), "values": values}
	if startAt+len(page) < len(filtered) {
		query := r.URL.Query()
		query.Set("startAt", strconv.Itoa(startAt+len(page)))
		response["nextPage"] = h.BaseURL + "/rest/api/3/notificationscheme?" + query.Encode()
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) notificationEntryRoute(w http.ResponseWriter, r *http.Request, schemeID int64, rest []string) {
	workspaceID, actorID, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	if len(rest) == 0 && r.Method == http.MethodPut {
		var request struct {
			NotificationSchemeEvents []notificationEventRequest `json:"notificationSchemeEvents"`
		}
		if !decodeProjectRequest(w, r, &request) {
			return
		}
		inputs, err := notificationInputs(request.NotificationSchemeEvents)
		if err != nil {
			notificationSchemeError(w, err)
			return
		}
		if err = h.Store.AddNotificationEntries(r.Context(), workspaceID, actorID, schemeID, inputs); err != nil {
			notificationSchemeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if len(rest) == 1 && r.Method == http.MethodDelete {
		entryID, err := parsePositiveInt64(rest[0])
		if err != nil {
			notificationSchemeError(w, err)
			return
		}
		if err = h.Store.DeleteNotificationEntry(r.Context(), workspaceID, actorID, schemeID, entryID); err != nil {
			notificationSchemeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
}

func (h *Handler) notificationSchemeMappings(w http.ResponseWriter, r *http.Request) {
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
		notificationSchemeError(w, err)
		return
	}
	mappings, err := h.Store.NotificationSchemeMappings(r.Context(), workspaceID)
	if err != nil {
		notificationSchemeError(w, err)
		return
	}
	schemeFilter, err := int64QuerySet(r.URL.Query()["notificationSchemeId"])
	if err != nil {
		notificationSchemeError(w, err)
		return
	}
	projectFilter := stringQuerySet(r.URL.Query()["projectId"])
	filtered := []store.NotificationSchemeMapping{}
	for _, mapping := range mappings {
		if len(schemeFilter) > 0 && !schemeFilter[mapping.SchemeID] {
			continue
		}
		if len(projectFilter) > 0 && !projectFilter[mapping.ProjectID] {
			continue
		}
		filtered = append(filtered, mapping)
	}
	page := pageSlice(filtered, startAt, maxResults)
	values := make([]map[string]string, 0, len(page))
	for _, mapping := range page {
		values = append(values, map[string]string{"notificationSchemeId": strconv.FormatInt(mapping.SchemeID, 10), "projectId": mapping.ProjectID})
	}
	writeJSON(w, http.StatusOK, map[string]any{"startAt": startAt, "maxResults": maxResults, "total": len(filtered), "isLast": startAt+len(page) >= len(filtered), "values": values})
}

func (h *Handler) projectNotificationSchemeRoute(w http.ResponseWriter, r *http.Request, path string) {
	parts := strings.Split(strings.TrimPrefix(path, "/project/"), "/")
	if len(parts) != 2 || parts[1] != "notificationscheme" || r.Method != http.MethodGet {
		jiraError(w, http.StatusNotFound, "No resource found")
		return
	}
	workspaceID, actorID, authErr := h.authWorkspace(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	expanded, err := notificationSchemeExpand(r)
	if err != nil {
		notificationSchemeError(w, err)
		return
	}
	scheme, project, err := h.Store.AssignedNotificationScheme(r.Context(), workspaceID, parts[0], expanded)
	if err != nil {
		notificationSchemeError(w, err)
		return
	}
	allowed, err := h.Store.HasProjectPermission(r.Context(), workspaceID, actorID, project.ID, "", "ADMINISTER_PROJECTS")
	if err != nil {
		notificationSchemeError(w, err)
		return
	}
	if !allowed {
		jiraError(w, http.StatusNotFound, "The project was not found or the user is not an administrator.")
		return
	}
	projectID, _ := strconv.ParseInt(project.ID, 10, 64)
	writeJSON(w, http.StatusOK, h.notificationSchemeBean(scheme, expanded, []int64{projectID}))
}
