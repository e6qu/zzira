package confluence

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/models"
)

// wikiNumericID reports Confluence's integer ids as numbers, and anything else
// unchanged, matching how the rest of this API renders an id.
func wikiNumericID(value string) any {
	if number, err := strconv.ParseInt(value, 10, 64); err == nil {
		return number
	}
	return value
}

func contentStateBean(state *models.WikiContentState) any {
	if state == nil {
		return nil
	}
	return map[string]any{
		"id": wikiNumericID(state.ID), "name": state.Name, "color": state.Color,
	}
}

func contentStateBeans(states []models.WikiContentState) []any {
	values := make([]any, 0, len(states))
	for i := range states {
		values = append(values, contentStateBean(&states[i]))
	}
	return values
}

// contentStatus reads Confluence's status query. The state operations act on
// one status of a page at a time, and default to the published one.
func contentStatus(w http.ResponseWriter, r *http.Request, required bool) (string, bool) {
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	if status == "" {
		if required {
			failure(w, 400, "status is required.")
			return "", false
		}
		return "current", true
	}
	switch status {
	case "current", "draft", "archived":
		return status, true
	default:
		failure(w, 400, "status must be current, draft or archived.")
		return "", false
	}
}

// v1CustomContentStates serves the states the caller has made for themselves.
func (h *V1Handler) v1CustomContentStates(w http.ResponseWriter, r *http.Request, ws, actor string) {
	if !supportedQuery(w, r) {
		return
	}
	states, err := h.Store.CustomContentStates(r.Context(), ws, actor, 0)
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, contentStateBeans(states))
}

func (h *V1Handler) v1ContentState(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !supportedQuery(w, r, "status") {
		return
	}
	switch r.Method {
	case http.MethodGet:
		status, ok := contentStatus(w, r, false)
		if !ok {
			return
		}
		state, updated, err := h.Store.WikiPageContentState(r.Context(), ws, actor, id, status)
		if err != nil {
			writeError(w, err)
			return
		}
		respond(w, 200, map[string]any{"contentState": contentStateBean(state), "lastUpdated": updated})
	case http.MethodPut:
		// Confluence requires the status here, because setting a state on a
		// draft and on the published page are different acts.
		status, ok := contentStatus(w, r, true)
		if !ok {
			return
		}
		var input struct {
			ID    any    `json:"id"`
			Name  string `json:"name"`
			Color string `json:"color"`
		}
		if !decode(w, r, &input) {
			return
		}
		state, updated, err := h.Store.SetWikiPageContentState(r.Context(), ws, actor, id, status,
			contentStateID(input.ID), input.Name, input.Color)
		if err != nil {
			writeError(w, err)
			return
		}
		respond(w, 200, map[string]any{"contentState": contentStateBean(state), "lastUpdated": updated})
	case http.MethodDelete:
		status, ok := contentStatus(w, r, false)
		if !ok {
			return
		}
		updated, err := h.Store.ClearWikiPageContentState(r.Context(), ws, actor, id, status)
		if err != nil {
			writeError(w, err)
			return
		}
		respond(w, 200, map[string]any{"contentState": nil, "lastUpdated": updated})
	default:
		failure(w, 405, "Method not allowed.")
	}
}

// contentStateID accepts Confluence's numeric id as well as a string one.
func contentStateID(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case float64:
		return strconv.FormatInt(int64(typed), 10)
	default:
		return ""
	}
}

// v1AvailableContentStates reports what the content could be set to: every
// state the space suggests, and the few custom ones the writer used last, which
// is the list Confluence's editor offers.
func (h *V1Handler) v1AvailableContentStates(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !supportedQuery(w, r) {
		return
	}
	page, err := h.Store.WikiPage(r.Context(), ws, actor, id)
	if err != nil {
		writeError(w, err)
		return
	}
	space, err := h.Store.WikiSpace(r.Context(), ws, actor, page.SpaceID)
	if err != nil {
		writeError(w, err)
		return
	}
	spaceStates, err := h.Store.SpaceContentStates(r.Context(), ws, actor, space.Key)
	if err != nil {
		writeError(w, err)
		return
	}
	custom, err := h.Store.CustomContentStates(r.Context(), ws, actor, 3)
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, map[string]any{
		"spaceContentStates":  contentStateBeans(spaceStates),
		"customContentStates": contentStateBeans(custom),
	})
}

func (h *V1Handler) v1SpaceContentStates(w http.ResponseWriter, r *http.Request, ws, actor, spaceKey string) {
	if !supportedQuery(w, r) {
		return
	}
	states, err := h.Store.SpaceContentStates(r.Context(), ws, actor, spaceKey)
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, contentStateBeans(states))
}

// v1SpaceContentStateSettings reports what the space allows. Nothing pinned
// writes these, so they describe the product's behavior rather than a
// configuration an administrator has chosen.
func (h *V1Handler) v1SpaceContentStateSettings(w http.ResponseWriter, r *http.Request, ws, actor, spaceKey string) {
	if !supportedQuery(w, r) {
		return
	}
	states, err := h.Store.SpaceContentStates(r.Context(), ws, actor, spaceKey)
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, map[string]any{
		"contentStatesAllowed": true, "customContentStatesAllowed": true,
		"spaceContentStatesAllowed": true, "spaceContentStates": contentStateBeans(states),
	})
}

func (h *V1Handler) v1SpaceContentStateContent(w http.ResponseWriter, r *http.Request, ws, actor, spaceKey string) {
	if !supportedQuery(w, r, "state-id", "expand", "limit", "start") {
		return
	}
	stateID := strings.TrimSpace(r.URL.Query().Get("state-id"))
	if stateID == "" {
		failure(w, 400, "state-id is required.")
		return
	}
	pages, err := h.Store.WikiPagesInContentState(r.Context(), ws, actor, spaceKey, stateID)
	if err != nil {
		writeError(w, err)
		return
	}
	start, limit := 0, 25
	if raw := r.URL.Query().Get("start"); raw != "" {
		parsed, convErr := strconv.Atoi(raw)
		if convErr != nil || parsed < 0 {
			failure(w, 400, "start must be zero or greater.")
			return
		}
		start = parsed
	}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, convErr := strconv.Atoi(raw)
		if convErr != nil || parsed < 1 || parsed > 100 {
			failure(w, 400, "limit must be between 1 and 100.")
			return
		}
		limit = parsed
	}
	results := []any{}
	for i := start; i < len(pages) && len(results) < limit; i++ {
		results = append(results, map[string]any{
			"id": pages[i].ID, "type": "page", "status": pages[i].Status, "title": pages[i].Title,
			"_links": map[string]string{"webui": "/wiki/spaces/" + spaceKey + "/pages/" + pages[i].ID},
		})
	}
	respond(w, 200, map[string]any{
		"results": results, "start": start, "limit": limit, "size": len(results),
		"_links": map[string]string{"base": h.BaseURL + "/wiki"},
	})
}
