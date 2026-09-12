package confluence

import (
	"net/http"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

// v1SpaceBean is the older surface's space shape, which carries the icon and
// the homepage the newer one leaves out.
func (h *Handler) v1SpaceBean(space *models.WikiSpace) map[string]any {
	bean := h.spaceBean(space, "plain", false)
	bean["icon"] = map[string]any{
		"path": "/wiki/images/logo/default-space-logo.svg", "width": 48, "height": 48, "isDefault": true,
	}
	bean["_expandable"] = map[string]any{"settings": "/rest/api/space/" + space.Key + "/settings"}
	return bean
}

type v1SpaceWrite struct {
	Name        string `json:"name"`
	Key         string `json:"key"`
	Alias       string `json:"alias"`
	Description struct {
		Plain struct {
			Value          string `json:"value"`
			Representation string `json:"representation"`
		} `json:"plain"`
	} `json:"description"`
}

// v1CreateSpace serves both the ordinary create and the private one, which
// Confluence describes as the same method with permissions set to the creator
// alone.
func (h *V1Handler) v1CreateSpace(w http.ResponseWriter, r *http.Request, ws, actor string, private bool) {
	if !supportedQuery(w, r) {
		return
	}
	var input v1SpaceWrite
	if !decode(w, r, &input) {
		return
	}
	space, err := h.Store.CreateWikiSpaceFull(r.Context(), ws, actor, store.CreateWikiSpaceInput{
		Key: input.Key, Alias: input.Alias, Name: input.Name,
		Description: input.Description.Plain.Value, Private: private,
		Personal: strings.HasPrefix(strings.TrimSpace(input.Key), "~"), OwnerID: actor,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, h.v1SpaceBean(space))
}

func (h *V1Handler) v1UpdateSpace(w http.ResponseWriter, r *http.Request, ws, actor, spaceKey string) {
	if !supportedQuery(w, r) {
		return
	}
	var input struct {
		Name        *string `json:"name"`
		Description *struct {
			Plain struct {
				Value string `json:"value"`
			} `json:"plain"`
		} `json:"description"`
		Homepage *struct {
			ID string `json:"id"`
		} `json:"homepage"`
		Type   *string `json:"type"`
		Status *string `json:"status"`
	}
	if !decode(w, r, &input) {
		return
	}
	update := store.UpdateWikiSpaceInput{Name: input.Name, Type: input.Type, Status: input.Status}
	if input.Description != nil {
		value := input.Description.Plain.Value
		update.Description = &value
	}
	if input.Homepage != nil {
		homepage := strings.TrimSpace(input.Homepage.ID)
		update.HomepageID = &homepage
	}
	space, err := h.Store.UpdateWikiSpace(r.Context(), ws, actor, spaceKey, update)
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, h.v1SpaceBean(space))
}

// v1DeleteSpace answers with the task that reports the removal, because a space
// can hold a great deal of content.
func (h *V1Handler) v1DeleteSpace(w http.ResponseWriter, r *http.Request, ws, actor, spaceKey string) {
	if !supportedQuery(w, r) {
		return
	}
	task, err := h.Store.EnqueueWikiSpaceDeletion(r.Context(), ws, actor, spaceKey)
	if err != nil {
		writeError(w, err)
		return
	}
	self := h.BaseURL + "/wiki/rest/api/longtask/" + task.ID
	w.Header().Set("Location", self)
	respond(w, 202, map[string]any{
		"ari": "ari:cloud:confluence::space/" + spaceKey, "id": task.ID,
		"links": map[string]string{"status": self},
	})
}

func (h *Handler) spaceSettingsBean(space *models.WikiSpace) map[string]any {
	contentMode := space.ContentMode
	if contentMode == "" {
		contentMode = "standard"
	}
	return map[string]any{
		"routeOverrideEnabled": space.RouteOverrideEnabled,
		"contentMode":          contentMode,
		"spaceKey":             space.Key,
		"editor": map[string]any{
			"page": "v2", "blogpost": "v2", "default": "v2",
		},
		"_links": map[string]string{"base": h.BaseURL + "/wiki"},
	}
}

func (h *V1Handler) v1SpaceSettings(w http.ResponseWriter, r *http.Request, ws, actor, spaceKey string) {
	if !supportedQuery(w, r) {
		return
	}
	switch r.Method {
	case http.MethodGet:
		// Reading the settings needs only the permission to view the space.
		space, err := h.Store.WikiSpaceByKey(r.Context(), ws, actor, spaceKey)
		if err != nil {
			writeError(w, err)
			return
		}
		respond(w, 200, h.spaceSettingsBean(space))
	case http.MethodPut:
		var input struct {
			RouteOverrideEnabled *bool   `json:"routeOverrideEnabled"`
			ContentMode          *string `json:"contentMode"`
		}
		if !decode(w, r, &input) {
			return
		}
		space, err := h.Store.SetWikiSpaceSettings(r.Context(), ws, actor, spaceKey, input.RouteOverrideEnabled, input.ContentMode)
		if err != nil {
			writeError(w, err)
			return
		}
		respond(w, 200, h.spaceSettingsBean(space))
	default:
		failure(w, 405, "Method not allowed.")
	}
}

func (h *Handler) themeBean(theme models.WikiTheme) map[string]any {
	return map[string]any{
		"themeKey": theme.Key, "name": theme.Name, "description": theme.Description,
		"icon": map[string]any{
			"path": "/wiki/images/themes/" + theme.Key + ".svg", "width": 48, "height": 48, "isDefault": true,
		},
		"_links": map[string]string{"base": h.BaseURL + "/wiki"},
	}
}

func (h *V1Handler) v1SpaceTheme(w http.ResponseWriter, r *http.Request, ws, actor, spaceKey string) {
	if !supportedQuery(w, r) {
		return
	}
	switch r.Method {
	case http.MethodGet:
		theme, err := h.Store.WikiSpaceTheme(r.Context(), ws, actor, spaceKey)
		if err != nil {
			writeError(w, err)
			return
		}
		respond(w, 200, h.themeBean(theme))
	case http.MethodPut:
		var input struct {
			ThemeKey string `json:"themeKey"`
		}
		if !decode(w, r, &input) {
			return
		}
		theme, err := h.Store.SetWikiSpaceTheme(r.Context(), ws, actor, spaceKey, input.ThemeKey)
		if err != nil {
			writeError(w, err)
			return
		}
		respond(w, 200, h.themeBean(theme))
	case http.MethodDelete:
		if err := h.Store.ResetWikiSpaceTheme(r.Context(), ws, actor, spaceKey); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(204)
	default:
		failure(w, 405, "Method not allowed.")
	}
}

// spaceDataPolicies reports whether a data policy blocks content access in each
// space.
func (h *Handler) spaceDataPolicies(w http.ResponseWriter, r *http.Request, ws, actor string) {
	if !supportedQuery(w, r, "ids", "keys", "cursor", "limit", "sort") {
		return
	}
	spaces, err := h.Store.WikiSpaceDataPolicies(r.Context(), ws, actor, splitQueryValues(r, "keys"))
	if err != nil {
		writeError(w, err)
		return
	}
	results := make([]any, 0, len(spaces))
	for _, space := range spaces {
		results = append(results, map[string]any{
			"id": space.ID, "status": map[string]any{"dataPolicy": map[string]any{"anyContentBlocked": false}},
		})
	}
	respond(w, 200, map[string]any{"results": results, "_links": map[string]string{"base": h.BaseURL + "/wiki"}})
}
