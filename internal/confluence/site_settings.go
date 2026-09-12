package confluence

import (
	"net/http"
	"strings"

	"github.com/e6qu/zzira/internal/store"
)

// lookAndFeelBean answers the whole structure Confluence returns: the global
// settings, the custom ones, the theme's, and which of them is showing.
func (h *Handler) lookAndFeelBean(settings store.LookAndFeel) map[string]any {
	custom := settings.Custom
	if custom == nil {
		custom = store.DefaultLookAndFeel()
	}
	bean := map[string]any{
		"selected": settings.Selected,
		"global":   store.DefaultLookAndFeel(),
		"custom":   custom,
		"theme":    store.DefaultLookAndFeel(),
		"_links":   map[string]string{"base": h.BaseURL + "/wiki"},
	}
	if settings.SpaceKey != "" {
		bean["spaceKey"] = settings.SpaceKey
	}
	return bean
}

func (h *V1Handler) v1LookAndFeel(w http.ResponseWriter, r *http.Request, ws, actor string) {
	switch r.Method {
	case http.MethodGet:
		if !supportedQuery(w, r, "spaceKey") {
			return
		}
		settings, err := h.Store.WikiLookAndFeel(r.Context(), ws, actor, r.URL.Query().Get("spaceKey"))
		if err != nil {
			writeError(w, err)
			return
		}
		respond(w, 200, h.lookAndFeelBean(settings))
	case http.MethodPut:
		if !supportedQuery(w, r) {
			return
		}
		var input struct {
			SpaceKey        string `json:"spaceKey"`
			LookAndFeelType string `json:"lookAndFeelType"`
		}
		if !decode(w, r, &input) {
			return
		}
		settings, err := h.Store.SetWikiLookAndFeelSelection(r.Context(), ws, actor,
			strings.TrimSpace(input.SpaceKey), strings.TrimSpace(input.LookAndFeelType))
		if err != nil {
			writeError(w, err)
			return
		}
		respond(w, 200, map[string]any{
			"spaceKey": settings.SpaceKey, "lookAndFeelType": settings.Selected,
		})
	default:
		failure(w, 405, "Method not allowed.")
	}
}

func (h *V1Handler) v1CustomLookAndFeel(w http.ResponseWriter, r *http.Request, ws, actor string) {
	if !supportedQuery(w, r, "spaceKey") {
		return
	}
	spaceKey := r.URL.Query().Get("spaceKey")
	switch r.Method {
	case http.MethodPost:
		var custom map[string]any
		if !decode(w, r, &custom) {
			return
		}
		settings, err := h.Store.UpsertWikiCustomLookAndFeel(r.Context(), ws, actor, spaceKey, custom)
		if err != nil {
			writeError(w, err)
			return
		}
		respond(w, 200, h.lookAndFeelBean(settings))
	case http.MethodDelete:
		if err := h.Store.ResetWikiCustomLookAndFeel(r.Context(), ws, actor, spaceKey); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(204)
	default:
		failure(w, 405, "Method not allowed.")
	}
}

func (h *V1Handler) v1SystemInfo(w http.ResponseWriter, r *http.Request, ws, actor string) {
	if !supportedQuery(w, r) {
		return
	}
	info, err := h.Store.WikiSystemInfo(r.Context(), ws, actor)
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, map[string]any{
		"cloudId": info.CloudID, "commitHash": "", "baseUrl": h.BaseURL + "/wiki",
		"fallbackBaseUrl": h.BaseURL + "/wiki", "edition": "standard",
		"siteTitle": info.SiteTitle, "defaultLocale": info.DefaultLocale,
		"defaultTimeZone": info.DefaultTimeZone, "microsPerimeter": "commercial",
	})
}

func (h *V1Handler) v1Themes(w http.ResponseWriter, r *http.Request, ws, actor string) {
	if !supportedQuery(w, r, "start", "limit") {
		return
	}
	themes, err := h.Store.WikiThemes(r.Context(), ws, actor)
	if err != nil {
		writeError(w, err)
		return
	}
	values := make([]any, 0, len(themes))
	for _, theme := range themes {
		values = append(values, h.themeBean(theme))
	}
	h.wikiPage(w, r, values, false)
}

func (h *V1Handler) v1SelectedTheme(w http.ResponseWriter, r *http.Request, ws, actor string) {
	if !supportedQuery(w, r) {
		return
	}
	theme, err := h.Store.WikiGlobalTheme(r.Context(), ws, actor)
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, h.themeBean(theme))
}

func (h *V1Handler) v1ThemeByKey(w http.ResponseWriter, r *http.Request, ws, actor, key string) {
	if !supportedQuery(w, r) {
		return
	}
	theme, err := h.Store.WikiThemeByKey(r.Context(), ws, actor, key)
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, h.themeBean(theme))
}
