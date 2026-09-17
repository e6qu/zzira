package confluence

import (
	"net/http"
	"slices"
	"strconv"

	"github.com/jackc/pgx/v5"
)

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

// contentDescendants answers everything beneath a page. Each kind the caller
// expands is listed in full; the others are named under `_expandable`, with
// the read that lists them where Confluence has one.
func (h *V1Handler) contentDescendants(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !supportedQuery(w, r, "expand") {
		return
	}
	expand, ok := parseV1Expand(w, r, 0)
	if !ok {
		return
	}
	for key := range expand {
		if !slices.Contains(v1ChildKinds, key) {
			failure(w, 400, "Unsupported descendant expansion.")
			return
		}
	}
	page, err := h.Store.WikiPage(r.Context(), ws, actor, id)
	if err != nil {
		writeError(w, err)
		return
	}
	if page.Status != "current" {
		writeError(w, pgx.ErrNoRows)
		return
	}
	found, err := h.v1Descendants(r.Context(), ws, actor, page, 100)
	if err != nil {
		writeError(w, err)
		return
	}
	self := h.BaseURL + "/wiki/rest/api/content/" + id + "/descendant"
	result := map[string]any{"_links": map[string]string{"base": h.BaseURL + "/wiki", "self": self}}
	expandable := map[string]string{}
	for _, kind := range v1ChildKinds {
		switch {
		case expand[kind]:
			result[kind] = contentArrayBean(found[kind], self+"/"+kind)
		case kind == "page" || kind == "comment" || kind == "attachment":
			expandable[kind] = "/rest/api/content/" + id + "/descendant/" + kind
		default:
			expandable[kind] = ""
		}
	}
	result["_expandable"] = expandable
	respond(w, 200, result)
}

// contentDescendantsByType lists one kind beneath a page to a depth, each
// entry expanded as the caller asks.
func (h *V1Handler) contentDescendantsByType(w http.ResponseWriter, r *http.Request, ws, actor, id, contentType string) {
	if !supportedQuery(w, r, "expand", "depth", "start", "limit") {
		return
	}
	if contentType != "page" && contentType != "comment" && contentType != "attachment" {
		failure(w, 400, "Descendant type must be page, comment, or attachment.")
		return
	}
	expand, ok := parseV1Expand(w, r, 0)
	if !ok {
		return
	}
	depth, ok := v1HierarchyDepth(w, r)
	if !ok {
		return
	}
	page, err := h.Store.WikiPage(r.Context(), ws, actor, id)
	if err != nil {
		writeError(w, err)
		return
	}
	if page.Status != "current" {
		writeError(w, pgx.ErrNoRows)
		return
	}
	found, err := h.v1Descendants(r.Context(), ws, actor, page, depth)
	if err != nil {
		writeError(w, err)
		return
	}
	values := found[contentType]
	if contentType == "page" && len(expand) > 0 {
		relations, err := h.Store.WikiTreeDescendants(r.Context(), ws, actor, id, "page", depth)
		if err != nil {
			writeError(w, err)
			return
		}
		values = []any{}
		for _, relation := range relations {
			if relation.Page == nil {
				continue
			}
			bean, err := h.v1PageBean(r.Context(), ws, actor, relation.Page, expand)
			if err != nil {
				writeError(w, err)
				return
			}
			values = append(values, bean)
		}
	}
	h.v1ContentArray(w, r, values)
}
