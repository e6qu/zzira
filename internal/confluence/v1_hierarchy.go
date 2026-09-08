package confluence

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/store"
)

func (h *V1Handler) v1PageContentBean(relation store.WikiPageRelation) map[string]any {
	page := relation.Page
	return map[string]any{
		"id": page.ID, "type": "page", "status": page.Status, "title": page.Title,
		"space":  map[string]string{"id": page.SpaceID},
		"_links": map[string]string{"webui": "/spaces/" + page.SpaceID + "/pages/" + page.ID, "self": h.BaseURL + "/wiki/rest/api/content/" + page.ID},
	}
}

func v1HierarchyDepth(w http.ResponseWriter, r *http.Request) (int, bool) {
	raw := r.URL.Query().Get("depth")
	if raw == "" || raw == "all" {
		return 100, true
	}
	if raw == "root" {
		return 1, true
	}
	depth, err := strconv.Atoi(raw)
	if err != nil || depth < 1 || depth > 100 {
		failure(w, 400, "depth must be all, root, or an integer between 1 and 100.")
		return 0, false
	}
	return depth, true
}

func (h *V1Handler) v1PageDescendantValues(relations []store.WikiPageRelation) []any {
	values := make([]any, 0, len(relations))
	for _, relation := range relations {
		values = append(values, h.v1PageContentBean(relation))
	}
	return values
}

func (h *V1Handler) v1ContentArray(w http.ResponseWriter, r *http.Request, values []any) {
	start, limit := 0, 25
	if raw := r.URL.Query().Get("start"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 {
			failure(w, 400, "start must be zero or greater.")
			return
		}
		start = value
	}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 || value > 1000 {
			failure(w, 400, "limit must be between 0 and 1000.")
			return
		}
		limit = value
	}
	start = min(start, len(values))
	end := min(start+limit, len(values))
	respond(w, 200, map[string]any{"results": values[start:end], "start": start, "limit": limit, "size": end - start, "_links": map[string]string{"base": h.BaseURL + "/wiki"}})
}

func (h *V1Handler) contentDescendants(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !supportedQuery(w, r, "expand") {
		return
	}
	relations, err := h.Store.WikiPageDescendants(r.Context(), ws, actor, id, 100)
	if err != nil {
		writeError(w, err)
		return
	}
	pageValues := h.v1PageDescendantValues(relations)
	empty := map[string]any{"results": []any{}, "start": 0, "limit": 0, "size": 0, "_links": map[string]string{"base": h.BaseURL + "/wiki"}}
	pages := map[string]any{"results": pageValues, "start": 0, "limit": len(pageValues), "size": len(pageValues), "_links": map[string]string{"base": h.BaseURL + "/wiki"}}
	respond(w, 200, map[string]any{
		"page": pages, "comment": empty, "attachment": empty,
		"_expandable": map[string]string{"whiteboard": "", "database": "", "embed": "", "folder": ""},
		"_links":      map[string]string{"base": h.BaseURL + "/wiki", "self": h.BaseURL + "/wiki/rest/api/content/" + id + "/descendant"},
	})
}

func (h *V1Handler) contentDescendantsByType(w http.ResponseWriter, r *http.Request, ws, actor, id, contentType string) {
	if !supportedQuery(w, r, "expand", "depth", "start", "limit") {
		return
	}
	if contentType != "page" && contentType != "comment" && contentType != "attachment" {
		failure(w, 400, "Descendant type must be page, comment, or attachment.")
		return
	}
	for _, raw := range r.URL.Query()["expand"] {
		for _, value := range strings.Split(raw, ",") {
			if value != "" && value != "attachment" && value != "comment" && value != "page" {
				failure(w, 400, "Unsupported descendant expansion.")
				return
			}
		}
	}
	depth, ok := v1HierarchyDepth(w, r)
	if !ok {
		return
	}
	values := []any{}
	if contentType == "page" {
		relations, err := h.Store.WikiPageDescendants(r.Context(), ws, actor, id, depth)
		if err != nil {
			writeError(w, err)
			return
		}
		values = h.v1PageDescendantValues(relations)
	} else if _, err := h.Store.WikiPage(r.Context(), ws, actor, id); err != nil {
		writeError(w, err)
		return
	}
	h.v1ContentArray(w, r, values)
}
