package confluence

import (
	"cmp"
	"net/http"
	"slices"
	"sort"
	"strconv"

	"github.com/e6qu/zzira/internal/store"
)

func validPageID(w http.ResponseWriter, id string) bool {
	value, err := strconv.ParseInt(id, 10, 64)
	if err != nil || value < 1 {
		failure(w, 400, "Page id must be a positive integer.")
		return false
	}
	return true
}

func pageRelationSort(relations []store.WikiPageRelation, order string) {
	descending := len(order) > 0 && order[0] == '-'
	if descending {
		order = order[1:]
	}
	if order == "" {
		order = "child-position"
	}
	sort.SliceStable(relations, func(i, j int) bool {
		left, right := relations[i], relations[j]
		comparison := 0
		switch order {
		case "id":
			leftID, _ := strconv.ParseInt(left.Page.ID, 10, 64)
			rightID, _ := strconv.ParseInt(right.Page.ID, 10, 64)
			comparison = cmp.Compare(leftID, rightID)
		case "title":
			comparison = cmp.Compare(left.Page.Title, right.Page.Title)
		case "created-date":
			comparison = cmp.Compare(left.Page.CreatedAt, right.Page.CreatedAt)
		case "modified-date":
			comparison = cmp.Compare(left.Page.Version.CreatedAt, right.Page.Version.CreatedAt)
		default:
			if left.Page.ParentID == right.Page.ParentID {
				comparison = cmp.Compare(left.ChildPosition, right.ChildPosition)
			} else {
				comparison = cmp.Compare(left.Page.ParentID, right.Page.ParentID)
			}
		}
		if comparison == 0 {
			comparison = cmp.Compare(left.Page.ID, right.Page.ID)
		}
		if descending {
			return comparison > 0
		}
		return comparison < 0
	})
}

func pageChildBean(relation store.WikiPageRelation, content bool) map[string]any {
	bean := map[string]any{
		"id": relation.Page.ID, "status": relation.Page.Status, "title": relation.Page.Title,
		"spaceId": relation.Page.SpaceID, "childPosition": relation.ChildPosition,
	}
	if content {
		bean["type"] = "page"
	}
	return bean
}

func (h *Handler) pageChildren(w http.ResponseWriter, r *http.Request, ws, actor, id string, directContent bool) {
	if !validPageID(w, id) || !supportedQuery(w, r, "cursor", "limit", "sort") {
		return
	}
	allowedSorts := []string{"created-date", "-created-date", "id", "-id", "child-position", "-child-position", "modified-date", "-modified-date"}
	if directContent {
		allowedSorts = append(allowedSorts, "title", "-title")
	}
	order := r.URL.Query().Get("sort")
	if order != "" && !slices.Contains(allowedSorts, order) {
		failure(w, 400, "Unsupported child content sort order.")
		return
	}
	relations, err := h.Store.WikiPageDescendants(r.Context(), ws, actor, id, 1)
	if err != nil {
		writeError(w, err)
		return
	}
	pageRelationSort(relations, order)
	values := make([]any, 0, len(relations))
	for _, relation := range relations {
		values = append(values, pageChildBean(relation, directContent))
	}
	h.list(w, r, values)
}

func (h *Handler) pageAncestors(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !validPageID(w, id) || !supportedQuery(w, r, "limit") {
		return
	}
	limit := 25
	if raw := r.URL.Query().Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 250 {
			failure(w, 400, "limit must be between 1 and 250.")
			return
		}
		limit = value
	}
	relations, err := h.Store.WikiPageAncestors(r.Context(), ws, actor, id)
	if err != nil {
		writeError(w, err)
		return
	}
	if len(relations) > limit {
		relations = relations[:limit]
	}
	values := make([]any, 0, len(relations))
	for _, relation := range relations {
		values = append(values, map[string]string{"id": relation.Page.ID, "type": "page"})
	}
	respond(w, 200, map[string]any{"results": values, "_links": map[string]string{"base": h.BaseURL + "/wiki"}})
}

func (h *Handler) pageDescendants(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !validPageID(w, id) || !supportedQuery(w, r, "cursor", "limit", "depth") {
		return
	}
	depth := 2
	if raw := r.URL.Query().Get("depth"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 10 {
			failure(w, 400, "depth must be between 1 and 10.")
			return
		}
		depth = value
	}
	relations, err := h.Store.WikiPageDescendants(r.Context(), ws, actor, id, depth)
	if err != nil {
		writeError(w, err)
		return
	}
	values := make([]any, 0, len(relations))
	for _, relation := range relations {
		values = append(values, map[string]any{
			"id": relation.Page.ID, "status": relation.Page.Status, "title": relation.Page.Title,
			"type": "page", "parentId": relation.Page.ParentID, "depth": relation.Depth,
			"childPosition": relation.ChildPosition,
		})
	}
	h.list(w, r, values)
}
