package confluence

import (
	"cmp"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/models"
)

func pageOperationsFor(allowed bool) []any {
	operations := []any{map[string]string{"operation": "read", "targetType": "page"}}
	if allowed {
		operations = append(operations, map[string]string{"operation": "update", "targetType": "page"}, map[string]string{"operation": "delete", "targetType": "page"})
	}
	return operations
}

func (h *Handler) pageByID(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !validPageID(w, id) || !supportedQuery(w, r, "body-format", "get-draft", "status", "version", "include-labels", "include-properties", "include-operations", "include-likes", "include-versions", "include-version", "include-favorited-by-current-user-status", "include-webresources", "include-collaborators", "include-direct-children") || !storageFormat(w, r) {
		return
	}
	flags := map[string]bool{}
	for _, key := range []string{"get-draft", "include-labels", "include-properties", "include-operations", "include-likes", "include-versions", "include-favorited-by-current-user-status", "include-webresources", "include-collaborators", "include-direct-children"} {
		value, ok := queryBool(w, r, key)
		if !ok {
			return
		}
		flags[key] = value
	}
	includeVersion := true
	if _, present := r.URL.Query()["include-version"]; present {
		var ok bool
		includeVersion, ok = queryBool(w, r, "include-version")
		if !ok {
			return
		}
	}
	if flags["include-webresources"] {
		failure(w, 400, "Page web-resource expansion is not available.")
		return
	}
	page, err := h.Store.WikiPage(r.Context(), ws, actor, id)
	if err != nil {
		writeError(w, err)
		return
	}
	if raw := r.URL.Query().Get("version"); raw != "" {
		number, parseErr := strconv.Atoi(raw)
		if parseErr != nil || number < 1 {
			failure(w, 400, "Version number must be positive.")
			return
		}
		page, err = h.Store.WikiPageAtVersion(r.Context(), ws, actor, id, number)
		if err != nil {
			writeError(w, err)
			return
		}
	}
	if flags["get-draft"] && page.Status != "draft" {
		failure(w, 404, "Page draft not found.")
		return
	}
	if _, filtered := r.URL.Query()["status"]; filtered && !queryContains(r, "status", page.Status) {
		failure(w, 404, "Page not found with the requested status.")
		return
	} else if !filtered && !flags["get-draft"] && page.Status != "current" {
		failure(w, 404, "Page not found with the requested status.")
		return
	}
	bean := h.pageBean(page, r.URL.Query().Get("body-format") != "")
	if !includeVersion {
		delete(bean, "version")
	}
	wrap := func(results []any) map[string]any {
		return map[string]any{"results": results, "meta": map[string]any{"hasMore": false}, "_links": map[string]any{}}
	}
	if flags["include-labels"] {
		labels, loadErr := h.Store.WikiPageLabels(r.Context(), ws, actor, id)
		if loadErr != nil {
			writeError(w, loadErr)
			return
		}
		values := make([]any, len(labels))
		for i := range labels {
			values[i] = labels[i]
		}
		bean["labels"] = wrap(values)
	}
	if flags["include-properties"] {
		properties, loadErr := h.Store.WikiPageProperties(r.Context(), ws, actor, id, "")
		if loadErr != nil {
			writeError(w, loadErr)
			return
		}
		values := make([]any, len(properties))
		for i := range properties {
			values[i] = properties[i]
		}
		bean["properties"] = wrap(values)
	}
	if flags["include-likes"] {
		likes, loadErr := h.Store.WikiPageLikes(r.Context(), ws, actor, id)
		if loadErr != nil {
			writeError(w, loadErr)
			return
		}
		values := make([]any, len(likes))
		for i := range likes {
			values[i] = map[string]string{"accountId": likes[i]}
		}
		bean["likes"] = wrap(values)
	}
	if flags["include-versions"] {
		versions, loadErr := h.Store.WikiVersionsSorted(r.Context(), ws, actor, id, "")
		if loadErr != nil {
			writeError(w, loadErr)
			return
		}
		values := make([]any, len(versions))
		for i := range versions {
			values[i] = versions[i]
		}
		bean["versions"] = wrap(values)
	}
	if flags["include-operations"] {
		allowed, loadErr := h.Store.CanUpdateWikiPage(r.Context(), ws, actor, id)
		if loadErr != nil {
			writeError(w, loadErr)
			return
		}
		bean["operations"] = wrap(pageOperationsFor(allowed))
	}
	if flags["include-direct-children"] {
		relations, loadErr := h.Store.WikiPageDescendants(r.Context(), ws, actor, id, 1)
		if loadErr != nil {
			writeError(w, loadErr)
			return
		}
		values := make([]any, len(relations))
		for i := range relations {
			values[i] = pageChildBean(relations[i], true)
		}
		bean["directChildren"] = wrap(values)
	}
	if flags["include-favorited-by-current-user-status"] {
		bean["isFavoritedByCurrentUser"] = false
	}
	if flags["include-collaborators"] {
		bean["collaborators"] = []string{}
	}
	respond(w, 200, bean)
}

func sortPages(pages []*models.WikiPage, order string) bool {
	allowed := map[string]bool{"": true, "id": true, "-id": true, "title": true, "-title": true, "created-date": true, "-created-date": true, "modified-date": true, "-modified-date": true}
	if !allowed[order] {
		return false
	}
	if order == "" || order == "id" {
		return true
	}
	desc := strings.HasPrefix(order, "-")
	field := strings.TrimPrefix(order, "-")
	sort.SliceStable(pages, func(i, j int) bool {
		left, right := pages[i].ID, pages[j].ID
		switch field {
		case "title":
			left, right = pages[i].Title, pages[j].Title
		case "created-date":
			left, right = pages[i].CreatedAt, pages[j].CreatedAt
		case "modified-date":
			left, right = pages[i].Version.CreatedAt, pages[j].Version.CreatedAt
		}
		comparison := cmp.Compare(left, right)
		if desc {
			return comparison > 0
		}
		return comparison < 0
	})
	return true
}
