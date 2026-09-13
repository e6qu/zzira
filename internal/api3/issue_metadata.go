package api3

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

// Issue types, priorities, resolutions, their schemes and issue type
// properties. Clients see Jira's numeric ids; the ids the product stores are
// never sent.

func issueMetadataError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrIssueMetadataValidation):
		jiraError(w, http.StatusBadRequest, trimErrorPrefix(err))
	case errors.Is(err, store.ErrIssueMetadataConflict):
		jiraError(w, http.StatusConflict, trimErrorPrefix(err))
	case errors.Is(err, store.ErrIssueMetadataNotFound):
		jiraError(w, http.StatusNotFound, trimErrorPrefix(err))
	case errors.Is(err, store.ErrAPITaskConflict):
		jiraError(w, http.StatusConflict, "A task to delete this item is already running.")
	default:
		jiraError(w, http.StatusInternalServerError, "internal error")
	}
}

// trimErrorPrefix drops the sentinel's own words, leaving the sentence that
// explains the problem.
func trimErrorPrefix(err error) string {
	message := err.Error()
	if _, rest, ok := strings.Cut(message, ": "); ok {
		message = rest
	}
	if message == "" {
		return "The request is not valid."
	}
	return strings.ToUpper(message[:1]) + message[1:] + "."
}

func jiraIDString(id int64) string { return strconv.FormatInt(id, 10) }

func (h *Handler) issueTypeIconURL(t models.IssueType) string {
	icon := t.Icon
	if icon == "" {
		icon = "task"
	}
	return h.BaseURL + "/static/img/issuetype-" + icon + ".svg"
}

func (h *Handler) issueTypeBean(t models.IssueType) map[string]any {
	bean := map[string]any{
		"id": jiraIDString(t.JiraID), "name": t.Name, "description": t.Description,
		"subtask": t.Subtask, "hierarchyLevel": t.HierarchyLevel,
		"iconUrl": h.issueTypeIconURL(t),
		"self":    h.BaseURL + "/rest/api/3/issuetype/" + jiraIDString(t.JiraID),
	}
	if t.AvatarID != 0 {
		bean["avatarId"] = t.AvatarID
	}
	return bean
}

func (h *Handler) priorityBean(p models.Priority) map[string]any {
	icon := p.IconURL
	if strings.HasPrefix(icon, "/") {
		icon = h.BaseURL + icon
	}
	bean := map[string]any{
		"id": jiraIDString(p.JiraID), "name": p.Name, "description": p.Description,
		"statusColor": p.StatusColor, "iconUrl": icon, "isDefault": p.IsDefault,
		"self": h.BaseURL + "/rest/api/3/priority/" + jiraIDString(p.JiraID),
	}
	if p.AvatarID != 0 {
		bean["avatarId"] = p.AvatarID
	}
	return bean
}

func (h *Handler) resolutionBean(r models.Resolution) map[string]any {
	return map[string]any{
		"id": jiraIDString(r.JiraID), "name": r.Name, "description": r.Description,
		"self": h.BaseURL + "/rest/api/3/resolution/" + jiraIDString(r.JiraID),
	}
}

// decodeMetadataRequest reads one JSON object and refuses unknown fields, so a
// misspelt field is an error rather than a silently ignored one.
func decodeMetadataRequest(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		jiraError(w, http.StatusBadRequest, "The request is not valid: "+err.Error())
		return false
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		jiraError(w, http.StatusBadRequest, "Expected one JSON object.")
		return false
	}
	return true
}

func issueMetadataPage(w http.ResponseWriter, r *http.Request) (int, int, bool) {
	startAt, maxResults := 0, 50
	if raw := r.URL.Query().Get("startAt"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 {
			jiraError(w, http.StatusBadRequest, "startAt must be zero or greater.")
			return 0, 0, false
		}
		startAt = value
	}
	if raw := r.URL.Query().Get("maxResults"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 {
			jiraError(w, http.StatusBadRequest, "maxResults must be at least 1.")
			return 0, 0, false
		}
		if value > 1000 {
			value = 1000
		}
		maxResults = value
	}
	return startAt, maxResults, true
}

// issueMetadataRoute dispatches every issue type, priority and resolution path.
func (h *Handler) issueMetadataRoute(w http.ResponseWriter, r *http.Request, path string) bool {
	switch {
	case path == "/issuetype":
		h.issueTypeCollection(w, r)
	case path == "/issuetype/project":
		h.issueTypesForProject(w, r)
	case strings.HasPrefix(path, "/issuetype/"):
		parts := strings.Split(strings.TrimPrefix(path, "/issuetype/"), "/")
		switch {
		case len(parts) == 1:
			h.issueTypeResource(w, r, parts[0])
		case len(parts) == 2 && parts[1] == "alternatives":
			h.issueTypeAlternatives(w, r, parts[0])
		case len(parts) == 2 && parts[1] == "avatar2":
			h.issueTypeAvatar(w, r, parts[0])
		case len(parts) == 2 && parts[1] == "properties":
			h.issueTypePropertyKeys(w, r, parts[0])
		case len(parts) == 3 && parts[1] == "properties":
			h.issueTypeProperty(w, r, parts[0], parts[2])
		default:
			return false
		}
	case path == "/priority":
		h.priorityCollection(w, r)
	case path == "/priority/default":
		h.priorityDefault(w, r)
	case path == "/priority/move":
		h.priorityMove(w, r)
	case path == "/priority/search":
		h.prioritySearch(w, r)
	case strings.HasPrefix(path, "/priority/"):
		h.priorityResource(w, r, strings.TrimPrefix(path, "/priority/"))
	case path == "/resolution":
		h.resolutionCollection(w, r)
	case path == "/resolution/default":
		h.resolutionDefault(w, r)
	case path == "/resolution/move":
		h.resolutionMove(w, r)
	case path == "/resolution/search":
		h.resolutionSearch(w, r)
	case strings.HasPrefix(path, "/resolution/"):
		h.resolutionResource(w, r, strings.TrimPrefix(path, "/resolution/"))
	case path == "/issuetypescheme" || strings.HasPrefix(path, "/issuetypescheme/"):
		h.issueTypeSchemeRoute(w, r, strings.TrimPrefix(path, "/issuetypescheme"))
	case path == "/priorityscheme" || strings.HasPrefix(path, "/priorityscheme/"):
		h.prioritySchemeRoute(w, r, strings.TrimPrefix(path, "/priorityscheme"))
	default:
		return false
	}
	return true
}

func methodNotAllowed(w http.ResponseWriter) {
	jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
}

// ---- Issue types ----

func (h *Handler) issueTypeCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		workspaceID, _, authErr := h.authWorkspace(r)
		if authErr != nil {
			writeJerr(w, authErr)
			return
		}
		types, err := h.Store.IssueTypesForWorkspace(r.Context(), workspaceID)
		if err != nil {
			issueMetadataError(w, err)
			return
		}
		out := make([]map[string]any, 0, len(types))
		for _, t := range types {
			out = append(out, h.issueTypeBean(t))
		}
		writeJSON(w, http.StatusOK, out)
	case http.MethodPost:
		workspaceID, _, authErr := h.authWorkspaceAdmin(r)
		if authErr != nil {
			writeJerr(w, authErr)
			return
		}
		var request struct {
			Name           string `json:"name"`
			Description    string `json:"description"`
			Type           string `json:"type"`
			HierarchyLevel *int   `json:"hierarchyLevel"`
		}
		if !decodeMetadataRequest(w, r, &request) {
			return
		}
		created, err := h.Store.CreateIssueType(r.Context(), workspaceID, request.Name, request.Description, request.Type, request.HierarchyLevel)
		if err != nil {
			issueMetadataError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, h.issueTypeBean(created))
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) issueTypesForProject(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	workspaceID, _, authErr := h.authWorkspace(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	project := strings.TrimSpace(r.URL.Query().Get("projectId"))
	if project == "" {
		jiraError(w, http.StatusBadRequest, "projectId is required.")
		return
	}
	var level *int
	if raw := r.URL.Query().Get("level"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil {
			jiraError(w, http.StatusBadRequest, "level must be a number.")
			return
		}
		level = &value
	}
	p, err := h.Store.ProjectByIDOrKey(r.Context(), workspaceID, project)
	if err != nil {
		jiraError(w, http.StatusNotFound, "The project is not found or you do not have permission to view it.")
		return
	}
	types, err := h.Store.ProjectIssueTypes(r.Context(), workspaceID, p.ID, level)
	if err != nil {
		issueMetadataError(w, err)
		return
	}
	out := make([]map[string]any, 0, len(types))
	for _, t := range types {
		out = append(out, h.issueTypeBean(t))
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) issueTypeResource(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		workspaceID, _, authErr := h.authWorkspace(r)
		if authErr != nil {
			writeJerr(w, authErr)
			return
		}
		t, err := h.Store.IssueTypeInWorkspace(r.Context(), workspaceID, id)
		if err != nil {
			issueMetadataError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, h.issueTypeBean(t))
	case http.MethodPut:
		workspaceID, _, authErr := h.authWorkspaceAdmin(r)
		if authErr != nil {
			writeJerr(w, authErr)
			return
		}
		var request struct {
			Name        *string `json:"name"`
			Description *string `json:"description"`
			AvatarID    *int64  `json:"avatarId"`
		}
		if !decodeMetadataRequest(w, r, &request) {
			return
		}
		updated, err := h.Store.UpdateIssueType(r.Context(), workspaceID, id, request.Name, request.Description, request.AvatarID)
		if err != nil {
			issueMetadataError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, h.issueTypeBean(updated))
	case http.MethodDelete:
		workspaceID, _, authErr := h.authWorkspaceAdmin(r)
		if authErr != nil {
			writeJerr(w, authErr)
			return
		}
		if err := h.Store.DeleteIssueType(r.Context(), workspaceID, id, r.URL.Query().Get("alternativeIssueTypeId")); err != nil {
			issueMetadataError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) issueTypeAlternatives(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	workspaceID, _, authErr := h.authWorkspace(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	types, err := h.Store.AlternativeIssueTypes(r.Context(), workspaceID, id)
	if err != nil {
		issueMetadataError(w, err)
		return
	}
	out := make([]map[string]any, 0, len(types))
	for _, t := range types {
		out = append(out, h.issueTypeBean(t))
	}
	writeJSON(w, http.StatusOK, out)
}

// issueTypeAvatar loads an avatar image for an issue type. The image must be a
// JPEG, GIF or PNG and the XSRF header must be present, as Jira requires.
func (h *Handler) issueTypeAvatar(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	workspaceID, _, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	if !strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Atlassian-Token")), "no-check") {
		jiraError(w, http.StatusForbidden, "X-Atlassian-Token: no-check is required.")
		return
	}
	if r.URL.Query().Get("size") == "" {
		jiraError(w, http.StatusBadRequest, "size is required.")
		return
	}
	mediaType := strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]))
	switch mediaType {
	case "image/png", "image/jpeg", "image/jpg", "image/gif":
	default:
		jiraError(w, http.StatusBadRequest, "The image type is unsupported; use JPEG, GIF or PNG.")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil || len(body) == 0 {
		jiraError(w, http.StatusBadRequest, "An image is required.")
		return
	}
	t, err := h.Store.IssueTypeInWorkspace(r.Context(), workspaceID, id)
	if err != nil {
		issueMetadataError(w, err)
		return
	}
	avatarID, err := h.Store.SaveIssueTypeAvatar(r.Context(), workspaceID, t.ID, mediaType, body)
	if err != nil {
		issueMetadataError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": strconv.FormatInt(avatarID, 10), "isDeletable": true, "isSelected": true, "isSystemAvatar": false,
		"owner": jiraIDString(t.JiraID),
		"urls": map[string]string{
			"16x16": h.BaseURL + "/rest/api/3/universal_avatar/view/type/issuetype/avatar/" + strconv.FormatInt(avatarID, 10) + "?size=xsmall",
			"48x48": h.BaseURL + "/rest/api/3/universal_avatar/view/type/issuetype/avatar/" + strconv.FormatInt(avatarID, 10),
		},
	})
}

func (h *Handler) issueTypePropertyKeys(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	workspaceID, _, authErr := h.authWorkspace(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	t, keys, err := h.Store.IssueTypePropertyKeys(r.Context(), workspaceID, id)
	if err != nil {
		issueMetadataError(w, err)
		return
	}
	out := make([]map[string]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, map[string]string{"key": key, "self": h.BaseURL + "/rest/api/3/issuetype/" + jiraIDString(t.JiraID) + "/properties/" + key})
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": out})
}

func (h *Handler) issueTypeProperty(w http.ResponseWriter, r *http.Request, id, key string) {
	switch r.Method {
	case http.MethodGet:
		workspaceID, _, authErr := h.authWorkspace(r)
		if authErr != nil {
			writeJerr(w, authErr)
			return
		}
		value, err := h.Store.IssueTypeProperty(r.Context(), workspaceID, id, key)
		if err != nil {
			issueMetadataError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"key": key, "value": value})
	case http.MethodPut:
		workspaceID, _, authErr := h.authWorkspaceAdmin(r)
		if authErr != nil {
			writeJerr(w, authErr)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			jiraError(w, http.StatusBadRequest, "A property value is required.")
			return
		}
		created, err := h.Store.SetIssueTypeProperty(r.Context(), workspaceID, id, key, body)
		if err != nil {
			issueMetadataError(w, err)
			return
		}
		if created {
			w.WriteHeader(http.StatusCreated)
			return
		}
		w.WriteHeader(http.StatusOK)
	case http.MethodDelete:
		workspaceID, _, authErr := h.authWorkspaceAdmin(r)
		if authErr != nil {
			writeJerr(w, authErr)
			return
		}
		if err := h.Store.DeleteIssueTypeProperty(r.Context(), workspaceID, id, key); err != nil {
			issueMetadataError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		methodNotAllowed(w)
	}
}

// ---- Priorities ----

type priorityRequest struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	StatusColor *string `json:"statusColor"`
	IconURL     *string `json:"iconUrl"`
	AvatarID    *int64  `json:"avatarId"`
}

func (in priorityRequest) input() store.PriorityInput {
	return store.PriorityInput{Name: in.Name, Description: in.Description, StatusColor: in.StatusColor, IconURL: in.IconURL, AvatarID: in.AvatarID}
}

func (h *Handler) priorityCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		workspaceID, _, authErr := h.authWorkspace(r)
		if authErr != nil {
			writeJerr(w, authErr)
			return
		}
		priorities, err := h.Store.PrioritiesForWorkspace(r.Context(), workspaceID)
		if err != nil {
			issueMetadataError(w, err)
			return
		}
		out := make([]map[string]any, 0, len(priorities))
		for _, p := range priorities {
			out = append(out, h.priorityBean(p))
		}
		writeJSON(w, http.StatusOK, out)
	case http.MethodPost:
		workspaceID, _, authErr := h.authWorkspaceAdmin(r)
		if authErr != nil {
			writeJerr(w, authErr)
			return
		}
		var request priorityRequest
		if !decodeMetadataRequest(w, r, &request) {
			return
		}
		created, err := h.Store.CreatePriority(r.Context(), workspaceID, request.input())
		if err != nil {
			issueMetadataError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]string{"id": jiraIDString(created.JiraID)})
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) priorityResource(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		workspaceID, _, authErr := h.authWorkspace(r)
		if authErr != nil {
			writeJerr(w, authErr)
			return
		}
		p, err := h.Store.PriorityInWorkspace(r.Context(), workspaceID, id)
		if err != nil {
			issueMetadataError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, h.priorityBean(p))
	case http.MethodPut:
		workspaceID, _, authErr := h.authWorkspaceAdmin(r)
		if authErr != nil {
			writeJerr(w, authErr)
			return
		}
		var request priorityRequest
		if !decodeMetadataRequest(w, r, &request) {
			return
		}
		if err := h.Store.UpdatePriority(r.Context(), workspaceID, id, request.input()); err != nil {
			issueMetadataError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case http.MethodDelete:
		workspaceID, actorID, authErr := h.authWorkspaceAdmin(r)
		if authErr != nil {
			writeJerr(w, authErr)
			return
		}
		task, err := h.Store.EnqueuePriorityDeletion(r.Context(), workspaceID, actorID, id)
		if err != nil {
			issueMetadataError(w, err)
			return
		}
		w.Header().Set("Location", h.BaseURL+"/rest/api/3/task/"+task.WireID())
		writeJSON(w, http.StatusSeeOther, h.apiTaskBean(task))
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) priorityDefault(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		methodNotAllowed(w)
		return
	}
	workspaceID, _, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	var request struct {
		ID *string `json:"id"`
	}
	if !decodeMetadataRequest(w, r, &request) {
		return
	}
	if request.ID == nil || strings.TrimSpace(*request.ID) == "" {
		jiraError(w, http.StatusBadRequest, "id is required.")
		return
	}
	if err := h.Store.SetDefaultPriority(r.Context(), workspaceID, *request.ID); err != nil {
		issueMetadataError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type moveRequest struct {
	IDs      []string `json:"ids"`
	After    string   `json:"after"`
	Position string   `json:"position"`
}

func (h *Handler) priorityMove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		methodNotAllowed(w)
		return
	}
	workspaceID, _, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	var request moveRequest
	if !decodeMetadataRequest(w, r, &request) {
		return
	}
	if err := h.Store.MovePriorities(r.Context(), workspaceID, request.IDs, request.After, request.Position); err != nil {
		issueMetadataError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) prioritySearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	workspaceID, _, authErr := h.authWorkspace(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	startAt, maxResults, ok := issueMetadataPage(w, r)
	if !ok {
		return
	}
	priorities, err := h.Store.PrioritiesForWorkspace(r.Context(), workspaceID)
	if err != nil {
		issueMetadataError(w, err)
		return
	}
	ids := stringQuerySet(securityQueryValues(r, "id"))
	name := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("priorityName")))
	onlyDefault := strings.EqualFold(r.URL.Query().Get("onlyDefault"), "true")
	// A project offers the priorities of its priority scheme; asking by project
	// narrows to the union of those.
	allowed := map[string]bool{}
	projects := securityQueryValues(r, "projectId")
	for _, project := range projects {
		p, err := h.Store.ProjectByIDOrKey(r.Context(), workspaceID, project)
		if err != nil {
			continue
		}
		scheme, err := h.Store.ProjectPriorityScheme(r.Context(), workspaceID, p.ID)
		if err != nil {
			continue
		}
		for _, id := range scheme.PriorityIDs {
			allowed[id] = true
		}
	}
	values := []map[string]any{}
	for _, p := range priorities {
		if len(ids) > 0 && !ids[jiraIDString(p.JiraID)] {
			continue
		}
		if name != "" && !strings.Contains(strings.ToLower(p.Name), name) {
			continue
		}
		if onlyDefault && !p.IsDefault {
			continue
		}
		if len(projects) > 0 && !allowed[p.ID] {
			continue
		}
		values = append(values, h.priorityBean(p))
	}
	page := pageSlice(values, startAt, maxResults)
	writeJSON(w, http.StatusOK, h.securityPageBean(r, page, len(values), startAt, maxResults))
}

// ---- Resolutions ----

func (h *Handler) resolutionCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		workspaceID, _, authErr := h.authWorkspace(r)
		if authErr != nil {
			writeJerr(w, authErr)
			return
		}
		resolutions, err := h.Store.ResolutionsForWorkspace(r.Context(), workspaceID)
		if err != nil {
			issueMetadataError(w, err)
			return
		}
		out := make([]map[string]any, 0, len(resolutions))
		for _, res := range resolutions {
			out = append(out, h.resolutionBean(res))
		}
		writeJSON(w, http.StatusOK, out)
	case http.MethodPost:
		workspaceID, _, authErr := h.authWorkspaceAdmin(r)
		if authErr != nil {
			writeJerr(w, authErr)
			return
		}
		var request struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		if !decodeMetadataRequest(w, r, &request) {
			return
		}
		created, err := h.Store.CreateResolution(r.Context(), workspaceID, request.Name, request.Description)
		if err != nil {
			issueMetadataError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]string{"id": jiraIDString(created.JiraID)})
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) resolutionResource(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		workspaceID, _, authErr := h.authWorkspace(r)
		if authErr != nil {
			writeJerr(w, authErr)
			return
		}
		res, err := h.Store.ResolutionInWorkspace(r.Context(), workspaceID, id)
		if err != nil {
			issueMetadataError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, h.resolutionBean(res))
	case http.MethodPut:
		workspaceID, _, authErr := h.authWorkspaceAdmin(r)
		if authErr != nil {
			writeJerr(w, authErr)
			return
		}
		var request struct {
			Name        string  `json:"name"`
			Description *string `json:"description"`
		}
		if !decodeMetadataRequest(w, r, &request) {
			return
		}
		description := ""
		if request.Description != nil {
			description = *request.Description
		}
		if err := h.Store.UpdateResolution(r.Context(), workspaceID, id, request.Name, description, request.Description != nil); err != nil {
			issueMetadataError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case http.MethodDelete:
		workspaceID, actorID, authErr := h.authWorkspaceAdmin(r)
		if authErr != nil {
			writeJerr(w, authErr)
			return
		}
		replaceWith := strings.TrimSpace(r.URL.Query().Get("replaceWith"))
		if replaceWith == "" {
			jiraError(w, http.StatusBadRequest, "replaceWith is required.")
			return
		}
		task, err := h.Store.EnqueueResolutionDeletion(r.Context(), workspaceID, actorID, id, replaceWith)
		if err != nil {
			issueMetadataError(w, err)
			return
		}
		w.Header().Set("Location", h.BaseURL+"/rest/api/3/task/"+task.WireID())
		writeJSON(w, http.StatusSeeOther, h.apiTaskBean(task))
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) resolutionDefault(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		methodNotAllowed(w)
		return
	}
	workspaceID, _, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	var request struct {
		ID *string `json:"id"`
	}
	if !decodeMetadataRequest(w, r, &request) {
		return
	}
	if request.ID == nil || strings.TrimSpace(*request.ID) == "" {
		jiraError(w, http.StatusBadRequest, "id is required.")
		return
	}
	if err := h.Store.SetDefaultResolution(r.Context(), workspaceID, *request.ID); err != nil {
		issueMetadataError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) resolutionMove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		methodNotAllowed(w)
		return
	}
	workspaceID, _, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	var request moveRequest
	if !decodeMetadataRequest(w, r, &request) {
		return
	}
	if err := h.Store.MoveResolutions(r.Context(), workspaceID, request.IDs, request.After, request.Position); err != nil {
		issueMetadataError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) resolutionSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	workspaceID, _, authErr := h.authWorkspace(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	startAt, maxResults, ok := issueMetadataPage(w, r)
	if !ok {
		return
	}
	resolutions, err := h.Store.ResolutionsForWorkspace(r.Context(), workspaceID)
	if err != nil {
		issueMetadataError(w, err)
		return
	}
	ids := stringQuerySet(securityQueryValues(r, "id"))
	onlyDefault := strings.EqualFold(r.URL.Query().Get("onlyDefault"), "true")
	values := []map[string]any{}
	for _, res := range resolutions {
		if len(ids) > 0 && !ids[jiraIDString(res.JiraID)] {
			continue
		}
		if onlyDefault && !res.IsDefault {
			continue
		}
		bean := h.resolutionBean(res)
		bean["default"] = res.IsDefault
		values = append(values, bean)
	}
	page := pageSlice(values, startAt, maxResults)
	writeJSON(w, http.StatusOK, h.securityPageBean(r, page, len(values), startAt, maxResults))
}

// ---- Issue type schemes ----

func (h *Handler) issueTypeSchemeRoute(w http.ResponseWriter, r *http.Request, rest string) {
	workspaceID, _, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	parts := []string{}
	if trimmed := strings.Trim(rest, "/"); trimmed != "" {
		parts = strings.Split(trimmed, "/")
	}
	switch {
	case len(parts) == 0 && r.Method == http.MethodGet:
		h.listIssueTypeSchemes(w, r, workspaceID)
	case len(parts) == 0 && r.Method == http.MethodPost:
		var request struct {
			Name               string   `json:"name"`
			Description        string   `json:"description"`
			DefaultIssueTypeID string   `json:"defaultIssueTypeId"`
			IssueTypeIDs       []string `json:"issueTypeIds"`
		}
		if !decodeMetadataRequest(w, r, &request) {
			return
		}
		id, err := h.Store.CreateIssueTypeScheme(r.Context(), workspaceID, request.Name, request.Description, request.DefaultIssueTypeID, request.IssueTypeIDs)
		if err != nil {
			issueMetadataError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]string{"issueTypeSchemeId": id})
	case len(parts) == 1 && parts[0] == "mapping" && r.Method == http.MethodGet:
		h.issueTypeSchemeMappingsList(w, r, workspaceID)
	case len(parts) == 1 && parts[0] == "project" && r.Method == http.MethodGet:
		h.issueTypeSchemeProjectsList(w, r, workspaceID)
	case len(parts) == 1 && parts[0] == "project" && r.Method == http.MethodPut:
		var request struct {
			IssueTypeSchemeID string `json:"issueTypeSchemeId"`
			ProjectID         string `json:"projectId"`
		}
		if !decodeMetadataRequest(w, r, &request) {
			return
		}
		if request.IssueTypeSchemeID == "" || request.ProjectID == "" {
			jiraError(w, http.StatusBadRequest, "issueTypeSchemeId and projectId are required.")
			return
		}
		p, err := h.Store.ProjectByIDOrKey(r.Context(), workspaceID, request.ProjectID)
		if err != nil {
			jiraError(w, http.StatusNotFound, "The project is not found.")
			return
		}
		if err := h.Store.AssignIssueTypeScheme(r.Context(), workspaceID, request.IssueTypeSchemeID, p.ID); err != nil {
			issueMetadataError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case len(parts) == 1 && r.Method == http.MethodPut:
		var request struct {
			Name               *string `json:"name"`
			Description        *string `json:"description"`
			DefaultIssueTypeID *string `json:"defaultIssueTypeId"`
		}
		if !decodeMetadataRequest(w, r, &request) {
			return
		}
		if err := h.Store.UpdateIssueTypeScheme(r.Context(), workspaceID, parts[0], request.Name, request.Description, request.DefaultIssueTypeID); err != nil {
			issueMetadataError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case len(parts) == 1 && r.Method == http.MethodDelete:
		if err := h.Store.DeleteIssueTypeScheme(r.Context(), workspaceID, parts[0]); err != nil {
			issueMetadataError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case len(parts) == 2 && parts[1] == "issuetype" && r.Method == http.MethodPut:
		var request struct {
			IssueTypeIDs []string `json:"issueTypeIds"`
		}
		if !decodeMetadataRequest(w, r, &request) {
			return
		}
		if err := h.Store.AddIssueTypesToScheme(r.Context(), workspaceID, parts[0], request.IssueTypeIDs); err != nil {
			issueMetadataError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case len(parts) == 3 && parts[1] == "issuetype" && parts[2] == "move" && r.Method == http.MethodPut:
		var request struct {
			IssueTypeIDs []string `json:"issueTypeIds"`
			After        string   `json:"after"`
			Position     string   `json:"position"`
		}
		if !decodeMetadataRequest(w, r, &request) {
			return
		}
		if err := h.Store.MoveIssueTypesInScheme(r.Context(), workspaceID, parts[0], request.IssueTypeIDs, request.After, request.Position); err != nil {
			issueMetadataError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case len(parts) == 3 && parts[1] == "issuetype" && r.Method == http.MethodDelete:
		if err := h.Store.RemoveIssueTypeFromScheme(r.Context(), workspaceID, parts[0], parts[2]); err != nil {
			issueMetadataError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		jiraError(w, http.StatusNotFound, "No resource found for path /rest/api/3/issuetypescheme"+rest)
	}
}

func (h *Handler) issueTypeSchemeBean(r *http.Request, workspaceID string, sc store.IssueTypeScheme, typeJira map[string]string) map[string]any {
	bean := map[string]any{"id": sc.ID, "name": sc.Name, "description": sc.Description, "isDefault": sc.IsDefault}
	if sc.DefaultIssueTypeID != "" {
		bean["defaultIssueTypeId"] = typeJira[sc.DefaultIssueTypeID]
	}
	return bean
}

// issueTypeJiraIDs maps internal issue type ids to the numeric ids clients see.
func (h *Handler) issueTypeJiraIDs(r *http.Request, workspaceID string) (map[string]string, error) {
	types, err := h.Store.IssueTypesForWorkspace(r.Context(), workspaceID)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, t := range types {
		out[t.ID] = jiraIDString(t.JiraID)
	}
	return out, nil
}

func (h *Handler) listIssueTypeSchemes(w http.ResponseWriter, r *http.Request, workspaceID string) {
	startAt, maxResults, ok := issueMetadataPage(w, r)
	if !ok {
		return
	}
	schemes, err := h.Store.IssueTypeSchemes(r.Context(), workspaceID)
	if err != nil {
		issueMetadataError(w, err)
		return
	}
	typeJira, err := h.issueTypeJiraIDs(r, workspaceID)
	if err != nil {
		issueMetadataError(w, err)
		return
	}
	ids := stringQuerySet(securityQueryValues(r, "id"))
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("queryString")))
	filtered := []store.IssueTypeScheme{}
	for _, sc := range schemes {
		if len(ids) > 0 && !ids[sc.ID] {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(sc.Name), query) {
			continue
		}
		filtered = append(filtered, sc)
	}
	switch orderBy := r.URL.Query().Get("orderBy"); orderBy {
	case "", "id", "+id":
		sort.SliceStable(filtered, func(i, j int) bool { return schemeIDLess(filtered[i].ID, filtered[j].ID) })
	case "-id":
		sort.SliceStable(filtered, func(i, j int) bool { return schemeIDLess(filtered[j].ID, filtered[i].ID) })
	case "name", "+name":
		sort.SliceStable(filtered, func(i, j int) bool { return strings.ToLower(filtered[i].Name) < strings.ToLower(filtered[j].Name) })
	case "-name":
		sort.SliceStable(filtered, func(i, j int) bool { return strings.ToLower(filtered[i].Name) > strings.ToLower(filtered[j].Name) })
	default:
		jiraError(w, http.StatusBadRequest, "orderBy is name, -name, +name, id, -id or +id.")
		return
	}
	expand := strings.Split(r.URL.Query().Get("expand"), ",")
	values := []map[string]any{}
	for _, sc := range filtered {
		bean := h.issueTypeSchemeBean(r, workspaceID, sc, typeJira)
		for _, e := range expand {
			switch strings.TrimSpace(e) {
			case "issueTypes":
				types := []string{}
				for _, id := range sc.IssueTypeIDs {
					types = append(types, typeJira[id])
				}
				bean["issueTypes"] = map[string]any{"values": types}
			case "projects":
				bean["projects"] = map[string]any{"values": sc.ProjectIDs}
			}
		}
		values = append(values, bean)
	}
	page := pageSlice(values, startAt, maxResults)
	writeJSON(w, http.StatusOK, h.securityPageBean(r, page, len(values), startAt, maxResults))
}

func schemeIDLess(a, b string) bool {
	x, errA := strconv.ParseInt(a, 10, 64)
	y, errB := strconv.ParseInt(b, 10, 64)
	if errA == nil && errB == nil {
		return x < y
	}
	return a < b
}

func (h *Handler) issueTypeSchemeMappingsList(w http.ResponseWriter, r *http.Request, workspaceID string) {
	startAt, maxResults, ok := issueMetadataPage(w, r)
	if !ok {
		return
	}
	schemes, err := h.Store.IssueTypeSchemes(r.Context(), workspaceID)
	if err != nil {
		issueMetadataError(w, err)
		return
	}
	typeJira, err := h.issueTypeJiraIDs(r, workspaceID)
	if err != nil {
		issueMetadataError(w, err)
		return
	}
	filter := stringQuerySet(securityQueryValues(r, "issueTypeSchemeId"))
	values := []map[string]any{}
	for _, sc := range schemes {
		if len(filter) > 0 && !filter[sc.ID] {
			continue
		}
		for _, id := range sc.IssueTypeIDs {
			values = append(values, map[string]any{"issueTypeSchemeId": sc.ID, "issueTypeId": typeJira[id]})
		}
	}
	page := pageSlice(values, startAt, maxResults)
	writeJSON(w, http.StatusOK, h.securityPageBean(r, page, len(values), startAt, maxResults))
}

func (h *Handler) issueTypeSchemeProjectsList(w http.ResponseWriter, r *http.Request, workspaceID string) {
	startAt, maxResults, ok := issueMetadataPage(w, r)
	if !ok {
		return
	}
	requested := securityQueryValues(r, "projectId")
	if len(requested) == 0 {
		jiraError(w, http.StatusBadRequest, "projectId is required.")
		return
	}
	schemes, err := h.Store.IssueTypeSchemes(r.Context(), workspaceID)
	if err != nil {
		issueMetadataError(w, err)
		return
	}
	typeJira, err := h.issueTypeJiraIDs(r, workspaceID)
	if err != nil {
		issueMetadataError(w, err)
		return
	}
	grouped := map[string][]string{}
	order := []string{}
	for _, project := range requested {
		p, err := h.Store.ProjectByIDOrKey(r.Context(), workspaceID, project)
		if err != nil {
			continue
		}
		sc, err := h.Store.ProjectIssueTypeScheme(r.Context(), workspaceID, p.ID)
		if err != nil {
			continue
		}
		if _, seen := grouped[sc.ID]; !seen {
			order = append(order, sc.ID)
		}
		grouped[sc.ID] = append(grouped[sc.ID], p.ID)
	}
	values := []map[string]any{}
	for _, id := range order {
		for _, sc := range schemes {
			if sc.ID == id {
				values = append(values, map[string]any{"issueTypeScheme": h.issueTypeSchemeBean(r, workspaceID, sc, typeJira), "projectIds": grouped[id]})
			}
		}
	}
	page := pageSlice(values, startAt, maxResults)
	writeJSON(w, http.StatusOK, h.securityPageBean(r, page, len(values), startAt, maxResults))
}

// ---- Priority schemes ----

func (h *Handler) prioritySchemeRoute(w http.ResponseWriter, r *http.Request, rest string) {
	workspaceID, _, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	parts := []string{}
	if trimmed := strings.Trim(rest, "/"); trimmed != "" {
		parts = strings.Split(trimmed, "/")
	}
	switch {
	case len(parts) == 0 && r.Method == http.MethodGet:
		h.listPrioritySchemes(w, r, workspaceID)
	case len(parts) == 0 && r.Method == http.MethodPost:
		in, ok := decodePrioritySchemeRequest(w, r, true)
		if !ok {
			return
		}
		id, err := h.Store.CreatePriorityScheme(r.Context(), workspaceID, in)
		if err != nil {
			issueMetadataError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"id": id})
	case len(parts) == 1 && parts[0] == "mappings" && r.Method == http.MethodPost:
		h.prioritySchemeMappingSuggestions(w, r, workspaceID)
	case len(parts) == 2 && parts[0] == "priorities" && parts[1] == "available" && r.Method == http.MethodGet:
		h.availablePrioritiesForScheme(w, r, workspaceID)
	case len(parts) == 1 && r.Method == http.MethodPut:
		in, ok := decodePrioritySchemeRequest(w, r, false)
		if !ok {
			return
		}
		updated, err := h.Store.UpdatePriorityScheme(r.Context(), workspaceID, parts[0], in)
		if err != nil {
			issueMetadataError(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"priorityScheme": h.prioritySchemeBean(r, workspaceID, updated)})
	case len(parts) == 1 && r.Method == http.MethodDelete:
		if err := h.Store.DeletePriorityScheme(r.Context(), workspaceID, parts[0]); err != nil {
			issueMetadataError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case len(parts) == 2 && parts[1] == "priorities" && r.Method == http.MethodGet:
		h.prioritiesInScheme(w, r, workspaceID, parts[0])
	case len(parts) == 2 && parts[1] == "projects" && r.Method == http.MethodGet:
		h.projectsInPriorityScheme(w, r, workspaceID, parts[0])
	default:
		jiraError(w, http.StatusNotFound, "No resource found for path /rest/api/3/priorityscheme"+rest)
	}
}

// decodePrioritySchemeRequest reads a create or update. Jira sends priority
// and project ids as numbers and, on update, as add/remove lists; both shapes
// are read into the complete lists the store works with.
func decodePrioritySchemeRequest(w http.ResponseWriter, r *http.Request, creating bool) (store.PrioritySchemeInput, bool) {
	var request struct {
		Name              *string           `json:"name"`
		Description       *string           `json:"description"`
		DefaultPriorityID json.RawMessage   `json:"defaultPriorityId"`
		PriorityIDs       []json.RawMessage `json:"priorityIds"`
		ProjectIDs        []json.RawMessage `json:"projectIds"`
		Priorities        *struct {
			Add *struct {
				IDs []json.RawMessage `json:"ids"`
			} `json:"add"`
			Remove *struct {
				IDs []json.RawMessage `json:"ids"`
			} `json:"remove"`
		} `json:"priorities"`
		Projects *struct {
			Add *struct {
				IDs []json.RawMessage `json:"ids"`
			} `json:"add"`
			Remove *struct {
				IDs []json.RawMessage `json:"ids"`
			} `json:"remove"`
		} `json:"projects"`
		Mappings *struct {
			In  map[string]json.RawMessage `json:"in"`
			Out map[string]json.RawMessage `json:"out"`
		} `json:"mappings"`
	}
	if !decodeMetadataRequest(w, r, &request) {
		return store.PrioritySchemeInput{}, false
	}
	idText := func(raw json.RawMessage) string {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s
		}
		return strings.TrimSpace(string(raw))
	}
	in := store.PrioritySchemeInput{Name: request.Name, Description: request.Description}
	if len(request.DefaultPriorityID) > 0 && string(request.DefaultPriorityID) != "null" {
		v := idText(request.DefaultPriorityID)
		in.DefaultPriority = &v
	}
	if request.PriorityIDs != nil {
		ids := []string{}
		for _, raw := range request.PriorityIDs {
			ids = append(ids, idText(raw))
		}
		in.PriorityIDs = &ids
	}
	if request.ProjectIDs != nil {
		ids := []string{}
		for _, raw := range request.ProjectIDs {
			ids = append(ids, idText(raw))
		}
		in.ProjectIDs = &ids
	}
	if request.Mappings != nil {
		for from, to := range request.Mappings.In {
			in.Mappings = append(in.Mappings, store.PrioritySchemeMapping{From: from, To: idText(to)})
		}
		for from, to := range request.Mappings.Out {
			in.Mappings = append(in.Mappings, store.PrioritySchemeMapping{From: from, To: idText(to)})
		}
	}
	if !creating && (request.Priorities != nil || request.Projects != nil) {
		in.AddRemove = &store.PrioritySchemeAddRemove{}
		collect := func(list *struct {
			IDs []json.RawMessage `json:"ids"`
		}) []string {
			out := []string{}
			if list != nil {
				for _, raw := range list.IDs {
					out = append(out, idText(raw))
				}
			}
			return out
		}
		if request.Priorities != nil {
			in.AddRemove.AddPriorities, in.AddRemove.RemovePriorities = collect(request.Priorities.Add), collect(request.Priorities.Remove)
		}
		if request.Projects != nil {
			in.AddRemove.AddProjects, in.AddRemove.RemoveProjects = collect(request.Projects.Add), collect(request.Projects.Remove)
		}
	}
	return in, true
}

func (h *Handler) prioritySchemeBean(r *http.Request, workspaceID string, sc store.PriorityScheme) map[string]any {
	bean := map[string]any{
		"id": sc.ID, "name": sc.Name, "description": sc.Description,
		"isDefault": sc.IsDefault, "default": sc.IsDefault,
		"self": h.BaseURL + "/rest/api/3/priorityscheme/" + sc.ID,
	}
	if p, err := h.Store.PriorityInWorkspace(r.Context(), workspaceID, sc.DefaultPriorityID); err == nil {
		bean["defaultPriorityId"] = jiraIDString(p.JiraID)
	}
	return bean
}

func (h *Handler) listPrioritySchemes(w http.ResponseWriter, r *http.Request, workspaceID string) {
	startAt, maxResults, ok := issueMetadataPage(w, r)
	if !ok {
		return
	}
	schemes, err := h.Store.PrioritySchemes(r.Context(), workspaceID)
	if err != nil {
		issueMetadataError(w, err)
		return
	}
	priorities, err := h.Store.PrioritiesForWorkspace(r.Context(), workspaceID)
	if err != nil {
		issueMetadataError(w, err)
		return
	}
	byJira := map[string]string{}
	for _, p := range priorities {
		byJira[jiraIDString(p.JiraID)] = p.ID
	}
	schemeIDs := stringQuerySet(securityQueryValues(r, "schemeId"))
	priorityFilter := securityQueryValues(r, "priorityId")
	name := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("schemeName")))
	onlyDefault := strings.EqualFold(r.URL.Query().Get("onlyDefault"), "true")
	filtered := []store.PriorityScheme{}
	for _, sc := range schemes {
		if len(schemeIDs) > 0 && !schemeIDs[sc.ID] {
			continue
		}
		if name != "" && !strings.Contains(strings.ToLower(sc.Name), name) {
			continue
		}
		if onlyDefault && !sc.IsDefault {
			continue
		}
		if len(priorityFilter) > 0 {
			match := false
			for _, wire := range priorityFilter {
				if id, ok := byJira[wire]; ok && containsText(sc.PriorityIDs, id) {
					match = true
				}
			}
			if !match {
				continue
			}
		}
		filtered = append(filtered, sc)
	}
	switch orderBy := r.URL.Query().Get("orderBy"); orderBy {
	case "", "name", "+name":
		sort.SliceStable(filtered, func(i, j int) bool { return strings.ToLower(filtered[i].Name) < strings.ToLower(filtered[j].Name) })
	case "-name":
		sort.SliceStable(filtered, func(i, j int) bool { return strings.ToLower(filtered[i].Name) > strings.ToLower(filtered[j].Name) })
	default:
		jiraError(w, http.StatusBadRequest, "orderBy is name, +name or -name.")
		return
	}
	expand := r.URL.Query().Get("expand")
	values := []map[string]any{}
	for _, sc := range filtered {
		bean := h.prioritySchemeBean(r, workspaceID, sc)
		if strings.Contains(expand, "priorities") {
			bean["priorities"] = h.priorityPageForIDs(r, workspaceID, sc.PriorityIDs, 0, 50)
		}
		if strings.Contains(expand, "projects") {
			bean["projects"] = map[string]any{"startAt": 0, "maxResults": 50, "total": len(sc.ProjectIDs), "isLast": true, "values": sc.ProjectIDs}
		}
		values = append(values, bean)
	}
	page := pageSlice(values, startAt, maxResults)
	writeJSON(w, http.StatusOK, h.securityPageBean(r, page, len(values), startAt, maxResults))
}

func containsText(values []string, target string) bool {
	for _, v := range values {
		if v == target {
			return true
		}
	}
	return false
}

// schemePriorityBean is a priority as a scheme lists it, with the place it has
// in that scheme.
func (h *Handler) schemePriorityBean(p models.Priority, sequence int) map[string]any {
	bean := h.priorityBean(p)
	bean["sequence"] = strconv.Itoa(sequence)
	return bean
}

func (h *Handler) priorityPageForIDs(r *http.Request, workspaceID string, ids []string, startAt, maxResults int) map[string]any {
	values := []map[string]any{}
	for i, id := range ids {
		if p, err := h.Store.PriorityInWorkspace(r.Context(), workspaceID, id); err == nil {
			values = append(values, h.schemePriorityBean(p, i))
		}
	}
	page := pageSlice(values, startAt, maxResults)
	return map[string]any{"startAt": startAt, "maxResults": maxResults, "total": len(values), "isLast": startAt+len(page) >= len(values), "values": page}
}

func (h *Handler) prioritiesInScheme(w http.ResponseWriter, r *http.Request, workspaceID, schemeID string) {
	startAt, maxResults, ok := issueMetadataPage(w, r)
	if !ok {
		return
	}
	sc, err := h.Store.PrioritySchemeByID(r.Context(), workspaceID, schemeID)
	if err != nil {
		issueMetadataError(w, err)
		return
	}
	values := []map[string]any{}
	for i, id := range sc.PriorityIDs {
		if p, err := h.Store.PriorityInWorkspace(r.Context(), workspaceID, id); err == nil {
			values = append(values, h.schemePriorityBean(p, i))
		}
	}
	page := pageSlice(values, startAt, maxResults)
	writeJSON(w, http.StatusOK, h.securityPageBean(r, page, len(values), startAt, maxResults))
}

func (h *Handler) projectsInPriorityScheme(w http.ResponseWriter, r *http.Request, workspaceID, schemeID string) {
	startAt, maxResults, ok := issueMetadataPage(w, r)
	if !ok {
		return
	}
	sc, err := h.Store.PrioritySchemeByID(r.Context(), workspaceID, schemeID)
	if err != nil {
		issueMetadataError(w, err)
		return
	}
	projectIDs, err := h.Store.ProjectsUsingPriorityScheme(r.Context(), workspaceID, sc)
	if err != nil {
		issueMetadataError(w, err)
		return
	}
	filter := stringQuerySet(securityQueryValues(r, "projectId"))
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("query")))
	values := []map[string]any{}
	for _, id := range projectIDs {
		p, err := h.Store.ProjectByIDOrKey(r.Context(), workspaceID, id)
		if err != nil {
			continue
		}
		if len(filter) > 0 && !filter[p.ID] {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(p.Name), query) && !strings.Contains(strings.ToLower(p.Key), query) {
			continue
		}
		values = append(values, map[string]any{"id": p.ID, "key": p.Key, "name": p.Name, "self": h.BaseURL + "/rest/api/3/project/" + p.ID})
	}
	page := pageSlice(values, startAt, maxResults)
	writeJSON(w, http.StatusOK, h.securityPageBean(r, page, len(values), startAt, maxResults))
}

func (h *Handler) availablePrioritiesForScheme(w http.ResponseWriter, r *http.Request, workspaceID string) {
	startAt, maxResults, ok := issueMetadataPage(w, r)
	if !ok {
		return
	}
	schemeID := strings.TrimSpace(r.URL.Query().Get("schemeId"))
	if schemeID == "" {
		jiraError(w, http.StatusBadRequest, "schemeId is required.")
		return
	}
	sc, err := h.Store.PrioritySchemeByID(r.Context(), workspaceID, schemeID)
	if err != nil {
		issueMetadataError(w, err)
		return
	}
	priorities, err := h.Store.PrioritiesForWorkspace(r.Context(), workspaceID)
	if err != nil {
		issueMetadataError(w, err)
		return
	}
	exclude := stringQuerySet(securityQueryValues(r, "exclude"))
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("query")))
	values := []map[string]any{}
	for i, p := range priorities {
		if containsText(sc.PriorityIDs, p.ID) || exclude[jiraIDString(p.JiraID)] {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(p.Name), query) {
			continue
		}
		values = append(values, h.schemePriorityBean(p, i))
	}
	page := pageSlice(values, startAt, maxResults)
	writeJSON(w, http.StatusOK, h.securityPageBean(r, page, len(values), startAt, maxResults))
}

// prioritySchemeMappingSuggestions reports which priorities would need mapping
// if a scheme's priorities or projects changed as described.
func (h *Handler) prioritySchemeMappingSuggestions(w http.ResponseWriter, r *http.Request, workspaceID string) {
	var request struct {
		SchemeID   json.RawMessage `json:"schemeId"`
		StartAt    int             `json:"startAt"`
		MaxResults int             `json:"maxResults"`
		Priorities *struct {
			Add    []json.RawMessage `json:"add"`
			Remove []json.RawMessage `json:"remove"`
		} `json:"priorities"`
		Projects *struct {
			Add []json.RawMessage `json:"add"`
		} `json:"projects"`
	}
	if !decodeMetadataRequest(w, r, &request) {
		return
	}
	idText := func(raw json.RawMessage) string {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s
		}
		return strings.TrimSpace(string(raw))
	}
	if request.MaxResults <= 0 {
		request.MaxResults = 50
	}
	sc, err := h.Store.PrioritySchemeByID(r.Context(), workspaceID, idText(request.SchemeID))
	if err != nil {
		issueMetadataError(w, err)
		return
	}
	remaining := append([]string{}, sc.PriorityIDs...)
	if request.Priorities != nil {
		for _, raw := range request.Priorities.Remove {
			if p, err := h.Store.PriorityInWorkspace(r.Context(), workspaceID, idText(raw)); err == nil {
				filtered := []string{}
				for _, id := range remaining {
					if id != p.ID {
						filtered = append(filtered, id)
					}
				}
				remaining = filtered
			}
		}
		for _, raw := range request.Priorities.Add {
			if p, err := h.Store.PriorityInWorkspace(r.Context(), workspaceID, idText(raw)); err == nil && !containsText(remaining, p.ID) {
				remaining = append(remaining, p.ID)
			}
		}
	}
	projects, err := h.Store.ProjectsUsingPriorityScheme(r.Context(), workspaceID, sc)
	if err != nil {
		issueMetadataError(w, err)
		return
	}
	if request.Projects != nil {
		for _, raw := range request.Projects.Add {
			if p, err := h.Store.ProjectByIDOrKey(r.Context(), workspaceID, idText(raw)); err == nil {
				projects = append(projects, p.ID)
			}
		}
	}
	needing, err := h.Store.PrioritiesNeedingMapping(r.Context(), workspaceID, projects, remaining)
	if err != nil {
		issueMetadataError(w, err)
		return
	}
	values := []map[string]any{}
	for i, id := range needing {
		if p, err := h.Store.PriorityInWorkspace(r.Context(), workspaceID, id); err == nil {
			values = append(values, h.schemePriorityBean(p, i))
		}
	}
	page := pageSlice(values, request.StartAt, request.MaxResults)
	writeJSON(w, http.StatusOK, h.securityPageBean(r, page, len(values), request.StartAt, request.MaxResults))
}
