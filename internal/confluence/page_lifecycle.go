package confluence

import (
	"cmp"
	"errors"
	"github.com/jackc/pgx/v5"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/models"
)

func pageOperationsFor(canUpdate, canDelete bool) []any {
	operations := []any{map[string]string{"operation": "read", "targetType": "page"}}
	if canUpdate {
		operations = append(operations, map[string]string{"operation": "update", "targetType": "page"})
	}
	if canDelete {
		operations = append(operations, map[string]string{"operation": "delete", "targetType": "page"})
	}
	return operations
}

func (h *Handler) pageByID(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !validPageID(w, id) || !supportedQuery(w, r, "body-format", "get-draft", "status", "version", "include-labels", "include-properties", "include-operations", "include-likes", "include-versions", "include-version", "include-favorited-by-current-user-status", "include-webresources", "include-collaborators", "include-direct-children") {
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
	bodyFormat, ok := pageBodyFormat(w, r, true)
	if !ok {
		return
	}
	for _, raw := range r.URL.Query()["status"] {
		for _, candidate := range strings.Split(raw, ",") {
			switch candidate {
			case "current", "archived", "trashed", "deleted", "historical", "draft":
			default:
				failure(w, 400, "Unsupported page status.")
				return
			}
		}
	}
	page, err := h.Store.WikiPage(r.Context(), ws, actor, id)
	if err != nil {
		writeError(w, err)
		return
	}
	// Deleted pages are for the space's administrators to see and restore.
	if page.Status == "deleted" && !h.canSeeDeleted(r, ws, actor, page.SpaceID, map[string]bool{}) {
		writeError(w, pgx.ErrNoRows)
		return
	}
	if raw := r.URL.Query().Get("version"); raw != "" {
		number, parseErr := strconv.Atoi(raw)
		if parseErr != nil || number < 1 {
			failure(w, 400, "Version number must be positive.")
			return
		}
		latest := page.Version.Number
		page, err = h.Store.WikiPageAtVersion(r.Context(), ws, actor, id, number)
		if err != nil {
			writeError(w, err)
			return
		}
		// An earlier version is history, which is the status Confluence gives it.
		if number < latest {
			page.Status = "historical"
		}
	}
	if flags["get-draft"] && page.Status != "draft" {
		draft, draftErr := h.Store.WikiContentDraft(r.Context(), ws, actor, "page", id)
		if errors.Is(draftErr, pgx.ErrNoRows) {
			failure(w, 404, "Page draft not found.")
			return
		}
		if draftErr != nil {
			writeError(w, draftErr)
			return
		}
		page = draftAsPage(page, draft)
	}
	if _, filtered := r.URL.Query()["status"]; filtered && !queryContains(r, "status", page.Status) {
		failure(w, 404, "Page not found with the requested status.")
		return
	} else if !filtered && !flags["get-draft"] && page.Status != "current" && page.Status != "historical" {
		failure(w, 404, "Page not found with the requested status.")
		return
	}
	bean, err := h.pageBeanWithFormat(page, bodyFormat)
	if err != nil {
		writeError(w, err)
		return
	}
	if flags["include-webresources"] {
		bean["webresources"] = pageWebResources(h.BaseURL)
	}
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
		canUpdate, loadErr := h.Store.CanUpdateWikiPage(r.Context(), ws, actor, id)
		if loadErr != nil {
			writeError(w, loadErr)
			return
		}
		canDelete, loadErr := h.Store.CanDeleteWikiPage(r.Context(), ws, actor, id)
		if loadErr != nil {
			writeError(w, loadErr)
			return
		}
		bean["operations"] = wrap(pageOperationsFor(canUpdate, canDelete))
	}
	if flags["include-direct-children"] {
		relations, loadErr := h.Store.WikiTreeDescendants(r.Context(), ws, actor, id, "page", 1)
		if loadErr != nil {
			writeError(w, loadErr)
			return
		}
		values := make([]any, len(relations))
		for i := range relations {
			values[i] = treeChildBean(relations[i], true)
		}
		bean["directChildren"] = wrap(values)
	}
	if flags["include-favorited-by-current-user-status"] {
		favourite, loadErr := h.Store.IsWikiPageFavourite(r.Context(), ws, actor, id)
		if loadErr != nil {
			writeError(w, loadErr)
			return
		}
		bean["isFavoritedByCurrentUser"] = favourite
	}
	if flags["include-collaborators"] {
		collaborators, loadErr := h.Store.WikiPageCollaborators(r.Context(), ws, actor, id)
		if loadErr != nil {
			writeError(w, loadErr)
			return
		}
		values := make([]any, len(collaborators))
		for i, accountID := range collaborators {
			values[i] = map[string]string{"accountId": accountID}
		}
		bean["collaborators"] = wrap(values)
	}
	if page.Status == "current" {
		h.recordView(r, ws, actor, "page", page.ID)
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
