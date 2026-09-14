package api3

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/authz"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

func isScreenPath(path string) bool {
	return path == "/screens" || strings.HasPrefix(path, "/screens/")
}

func screenError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrProjectPermission):
		jiraError(w, http.StatusForbidden, "Administrator privileges are required.")
	case errors.Is(err, store.ErrScreenConflict):
		jiraError(w, http.StatusConflict, err.Error())
	case errors.Is(err, store.ErrScreenValidation):
		jiraError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, store.ErrScreenNotFound):
		jiraError(w, http.StatusNotFound, "The screen, tab, or field does not exist.")
	default:
		jiraError(w, http.StatusInternalServerError, "Could not complete the screen operation.")
	}
}

func (h *Handler) screenBean(screen *models.Screen) map[string]any {
	return map[string]any{
		"id": wireNumericID(screen.ID), "name": screen.Name, "description": screen.Description,
	}
}

func (h *Handler) screenTabBean(tab models.ScreenTab) map[string]any {
	return map[string]any{"id": wireNumericID(tab.ID), "name": tab.Name}
}

func (h *Handler) screenFieldBean(field models.ScreenField) map[string]any {
	return map[string]any{"id": field.ID, "name": field.Name}
}

func (h *Handler) screenRoute(w http.ResponseWriter, r *http.Request, path string) {
	if path == "/screens" {
		h.screenCollection(w, r)
		return
	}
	rest := strings.Trim(strings.TrimPrefix(path, "/screens/"), "/")
	parts := strings.Split(rest, "/")
	switch {
	case len(parts) == 1 && parts[0] == "tabs":
		h.bulkScreenTabs(w, r)
		return
	case len(parts) == 2 && parts[0] == "addToDefault":
		h.addFieldToDefaultScreen(w, r, parts[1])
		return
	}
	if parts[0] == "" {
		jiraError(w, http.StatusNotFound, "No resource found")
		return
	}
	screenID := parts[0]
	switch {
	case len(parts) == 1:
		h.screenResource(w, r, screenID)
	case len(parts) == 2 && parts[1] == "availableFields":
		h.availableScreenFields(w, r, screenID)
	case len(parts) == 2 && parts[1] == "tabs":
		h.screenTabCollection(w, r, screenID)
	case len(parts) == 3 && parts[1] == "tabs":
		h.screenTabResource(w, r, screenID, parts[2])
	case len(parts) == 5 && parts[1] == "tabs" && parts[3] == "move":
		h.moveScreenTab(w, r, screenID, parts[2], parts[4])
	case len(parts) == 4 && parts[1] == "tabs" && parts[3] == "fields":
		h.screenTabFieldCollection(w, r, screenID, parts[2])
	case len(parts) == 5 && parts[1] == "tabs" && parts[3] == "fields":
		h.screenTabFieldResource(w, r, screenID, parts[2], parts[4])
	case len(parts) == 6 && parts[1] == "tabs" && parts[3] == "fields" && parts[5] == "move":
		h.moveScreenTabField(w, r, screenID, parts[2], parts[4])
	default:
		jiraError(w, http.StatusNotFound, "No resource found")
	}
}

func (h *Handler) screenCollection(w http.ResponseWriter, r *http.Request) {
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
		screens, err := h.Store.Screens(r.Context(), workspaceID, store.ScreenFilter{
			IDs: securityQueryValues(r, "id"), QueryString: r.URL.Query().Get("queryString")})
		if err != nil {
			screenError(w, err)
			return
		}
		// Every screen here is a global screen; team-managed project and
		// template screens are not created on this site.
		if scopes := commaQuerySet(r, "scope"); len(scopes) > 0 && !querySetContains(scopes, "GLOBAL") {
			screens = screens[:0]
		}
		if !orderByNameOrID(w, r.URL.Query().Get("orderBy"), screens, func(s *models.Screen) string { return s.Name }, func(s *models.Screen) string { return s.ID }) {
			return
		}
		page := pageSlice(screens, startAt, maxResults)
		values := make([]map[string]any, 0, len(page))
		for _, screen := range page {
			values = append(values, h.screenBean(screen))
		}
		writeJSON(w, http.StatusOK, h.securityPageBean(r, values, len(screens), startAt, maxResults))
	case http.MethodPost:
		var request struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		if !decodeProjectRequest(w, r, &request) {
			return
		}
		screen, err := h.Store.CreateScreen(r.Context(), workspaceID, actorID, request.Name, request.Description)
		if err != nil {
			screenError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, h.screenBean(screen))
	default:
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (h *Handler) screenResource(w http.ResponseWriter, r *http.Request, screenID string) {
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
		screen, err := h.Store.UpdateScreen(r.Context(), workspaceID, actorID, screenID, request.Name, request.Description)
		if err != nil {
			screenError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, h.screenBean(screen))
	case http.MethodDelete:
		if err := h.Store.DeleteScreen(r.Context(), workspaceID, actorID, screenID); err != nil {
			screenError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (h *Handler) addFieldToDefaultScreen(w http.ResponseWriter, r *http.Request, fieldID string) {
	if r.Method != http.MethodPost {
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	workspaceID, actorID, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	if err := h.Store.AddFieldToDefaultScreen(r.Context(), workspaceID, actorID, fieldID); err != nil {
		screenError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}

func (h *Handler) availableScreenFields(w http.ResponseWriter, r *http.Request, screenID string) {
	if r.Method != http.MethodGet {
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	workspaceID, _, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	fields, err := h.Store.AvailableScreenFields(r.Context(), workspaceID, screenID)
	if err != nil {
		screenError(w, err)
		return
	}
	values := make([]map[string]any, 0, len(fields))
	for _, field := range fields {
		values = append(values, h.screenFieldBean(field))
	}
	writeJSON(w, http.StatusOK, values)
}

func (h *Handler) bulkScreenTabs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	workspaceID, _, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	query := r.URL.Query()
	for _, name := range []string{"screenId", "tabId"} {
		for _, raw := range query[name] {
			if _, parseErr := strconv.ParseInt(strings.TrimSpace(raw), 10, 64); parseErr != nil {
				jiraError(w, http.StatusBadRequest, "The screen ID or the tab ID is empty or invalid.")
				return
			}
		}
	}
	startAt, maxResult := 0, 100
	for name, target := range map[string]*int{"startAt": &startAt, "maxResult": &maxResult} {
		if raw := query.Get(name); raw != "" {
			value, parseErr := strconv.Atoi(raw)
			if parseErr != nil || value < 0 {
				jiraError(w, http.StatusBadRequest, name+" must be a non-negative integer.")
				return
			}
			*target = value
		}
	}
	// Jira caps a page of tabs at 100.
	if maxResult > 100 {
		maxResult = 100
	}
	grouped, err := h.Store.BulkScreenTabs(r.Context(), workspaceID, securityQueryValues(r, "screenId"))
	if err != nil {
		screenError(w, err)
		return
	}
	tabIDs := map[string]bool{}
	for _, id := range securityQueryValues(r, "tabId") {
		tabIDs[id] = true
	}
	values := []map[string]any{}
	for screenID, tabs := range grouped {
		for _, tab := range tabs {
			bean := h.screenTabBean(tab)
			if len(tabIDs) > 0 && !tabIDs[fmt.Sprint(bean["id"])] {
				continue
			}
			bean["screenId"] = wireNumericID(screenID)
			values = append(values, bean)
		}
	}
	sortScreenTabBeans(values)
	writeJSON(w, http.StatusOK, values[min(startAt, len(values)):min(startAt+maxResult, len(values))])
}

// screenTabReader authorizes reading a screen's tabs: Jira administrators
// always, and a project's administrators when projectKey names their project
// and its screen schemes use the screen.
func (h *Handler) screenTabReader(r *http.Request, screenID string) (string, *jerr) {
	workspaceID, userID, authErr := h.authWorkspace(r)
	if authErr != nil {
		return "", authErr
	}
	denied := &jerr{http.StatusForbidden, "You do not have permission to perform this operation.", nil}
	admin, err := authz.IsWorkspaceAdmin(r.Context(), h.Store, workspaceID, userID)
	switch {
	case err != nil:
		return "", &jerr{http.StatusInternalServerError, "internal error", nil}
	case admin:
		return workspaceID, nil
	}
	projectKey := r.URL.Query().Get("projectKey")
	if projectKey == "" {
		return "", denied
	}
	project, err := h.Store.ProjectByIDOrKey(r.Context(), workspaceID, projectKey)
	if err != nil || project.Key != projectKey {
		return "", denied
	}
	allowed, err := h.hasProjectPermission(r.Context(), workspaceID, userID, project.ID, "", "ADMINISTER_PROJECTS")
	if err != nil {
		return "", &jerr{http.StatusInternalServerError, "internal error", nil}
	}
	if !allowed {
		return "", denied
	}
	used, err := h.Store.ScreenUsedByProject(r.Context(), workspaceID, project.ID, screenID)
	if err != nil {
		return "", &jerr{http.StatusInternalServerError, "internal error", nil}
	}
	if !used {
		return "", denied
	}
	return workspaceID, nil
}

func (h *Handler) screenTabCollection(w http.ResponseWriter, r *http.Request, screenID string) {
	if r.Method == http.MethodGet {
		workspaceID, authErr := h.screenTabReader(r, screenID)
		if authErr != nil {
			writeJerr(w, authErr)
			return
		}
		tabs, err := h.Store.ScreenTabs(r.Context(), workspaceID, screenID)
		if err != nil {
			screenError(w, err)
			return
		}
		values := make([]map[string]any, 0, len(tabs))
		for _, tab := range tabs {
			values = append(values, h.screenTabBean(tab))
		}
		writeJSON(w, http.StatusOK, values)
		return
	}
	workspaceID, actorID, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	switch r.Method {
	case http.MethodPost:
		var request struct {
			Name string `json:"name"`
		}
		if !decodeProjectRequest(w, r, &request) {
			return
		}
		tab, err := h.Store.AddScreenTab(r.Context(), workspaceID, actorID, screenID, request.Name)
		if err != nil {
			screenError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, h.screenTabBean(tab))
	default:
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (h *Handler) screenTabResource(w http.ResponseWriter, r *http.Request, screenID, tabID string) {
	workspaceID, actorID, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	switch r.Method {
	case http.MethodPut:
		var request struct {
			Name string `json:"name"`
		}
		if !decodeProjectRequest(w, r, &request) {
			return
		}
		tab, err := h.Store.RenameScreenTab(r.Context(), workspaceID, actorID, screenID, tabID, request.Name)
		if err != nil {
			screenError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, h.screenTabBean(tab))
	case http.MethodDelete:
		if err := h.Store.DeleteScreenTab(r.Context(), workspaceID, actorID, screenID, tabID); err != nil {
			screenError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (h *Handler) moveScreenTab(w http.ResponseWriter, r *http.Request, screenID, tabID, rawPosition string) {
	if r.Method != http.MethodPost {
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	workspaceID, actorID, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	position, err := strconv.Atoi(rawPosition)
	if err != nil {
		screenError(w, store.ErrScreenValidation)
		return
	}
	if err = h.Store.MoveScreenTab(r.Context(), workspaceID, actorID, screenID, tabID, position); err != nil {
		screenError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}

func (h *Handler) screenTabFieldCollection(w http.ResponseWriter, r *http.Request, screenID, tabID string) {
	workspaceID, actorID, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	switch r.Method {
	case http.MethodGet:
		fields, err := h.Store.ScreenTabFields(r.Context(), workspaceID, screenID, tabID)
		if err != nil {
			screenError(w, err)
			return
		}
		values := make([]map[string]any, 0, len(fields))
		for _, field := range fields {
			values = append(values, h.screenFieldBean(field))
		}
		writeJSON(w, http.StatusOK, values)
	case http.MethodPost:
		var request struct {
			FieldID string `json:"fieldId"`
		}
		if !decodeProjectRequest(w, r, &request) {
			return
		}
		field, err := h.Store.AddScreenTabField(r.Context(), workspaceID, actorID, screenID, tabID, request.FieldID)
		if err != nil {
			screenError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, h.screenFieldBean(field))
	default:
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (h *Handler) screenTabFieldResource(w http.ResponseWriter, r *http.Request, screenID, tabID, fieldID string) {
	if r.Method != http.MethodDelete {
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	workspaceID, actorID, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	if err := h.Store.RemoveScreenTabField(r.Context(), workspaceID, actorID, screenID, tabID, fieldID); err != nil {
		screenError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) moveScreenTabField(w http.ResponseWriter, r *http.Request, screenID, tabID, fieldID string) {
	if r.Method != http.MethodPost {
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	workspaceID, actorID, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	var request struct {
		After    string `json:"after"`
		Position string `json:"position"`
	}
	if !decodeProjectRequest(w, r, &request) {
		return
	}
	// Jira sends "after" as a field self link; accept the trailing field ID.
	after := request.After
	if index := strings.LastIndex(after, "/"); index >= 0 {
		after = after[index+1:]
	}
	if err := h.Store.MoveScreenTabField(r.Context(), workspaceID, actorID, screenID, tabID, fieldID, after, request.Position); err != nil {
		screenError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}

func (h *Handler) screensForField(w http.ResponseWriter, r *http.Request, fieldID string) {
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
	screens, err := h.Store.ScreensForField(r.Context(), workspaceID, fieldID)
	if err != nil {
		screenError(w, err)
		return
	}
	var tabs map[string]models.ScreenTab
	if querySetContains(commaQuerySet(r, "expand"), "tab") {
		if tabs, err = h.Store.ScreenTabsWithField(r.Context(), workspaceID, fieldID); err != nil {
			screenError(w, err)
			return
		}
	}
	page := pageSlice(screens, startAt, maxResults)
	values := make([]map[string]any, 0, len(page))
	for _, screen := range page {
		bean := h.screenBean(screen)
		if tab, ok := tabs[screen.ID]; ok {
			bean["tab"] = h.screenTabBean(tab)
		}
		values = append(values, bean)
	}
	writeJSON(w, http.StatusOK, h.securityPageBean(r, values, len(screens), startAt, maxResults))
}

func sortScreenTabBeans(values []map[string]any) {
	sort.Slice(values, func(i, j int) bool {
		left, right := beanSortKey(values[i]), beanSortKey(values[j])
		if left[0] != right[0] {
			return left[0] < right[0]
		}
		return left[1] < right[1]
	})
}

func beanSortKey(bean map[string]any) [2]int64 {
	key := [2]int64{}
	if value, ok := bean["screenId"].(int64); ok {
		key[0] = value
	}
	if value, ok := bean["id"].(int64); ok {
		key[1] = value
	}
	return key
}
