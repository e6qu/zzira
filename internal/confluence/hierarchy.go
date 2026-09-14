package confluence

import (
	"cmp"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"

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

// Pages, folders, whiteboards, databases and Smart Links share one content
// tree, so every kind answers its children, descendants and ancestors from it.

var childSorts = []string{"created-date", "-created-date", "id", "-id", "child-position", "-child-position", "modified-date", "-modified-date"}

func numericID(id string) int64 {
	value, _ := strconv.ParseInt(id, 10, 64)
	return value
}

// treeSort orders content tree nodes the way Confluence's child sorts do.
func treeSort(relations []store.WikiTreeRelation, order string) {
	descending := strings.HasPrefix(order, "-")
	order = strings.TrimPrefix(order, "-")
	sort.SliceStable(relations, func(i, j int) bool {
		left, right := relations[i], relations[j]
		comparison := 0
		switch order {
		case "id":
			comparison = cmp.Compare(numericID(left.ID()), numericID(right.ID()))
		case "title":
			comparison = cmp.Compare(left.Title(), right.Title())
		case "created-date":
			comparison = cmp.Compare(left.CreatedAt(), right.CreatedAt())
		case "modified-date":
			comparison = cmp.Compare(left.ModifiedAt(), right.ModifiedAt())
		default:
			if left.ParentID() == right.ParentID() {
				comparison = cmp.Compare(left.ChildPosition, right.ChildPosition)
			} else {
				comparison = cmp.Compare(numericID(left.ParentID()), numericID(right.ParentID()))
			}
		}
		if comparison == 0 {
			comparison = cmp.Compare(numericID(left.ID()), numericID(right.ID()))
		}
		if descending {
			return comparison > 0
		}
		return comparison < 0
	})
}

func treeChildBean(relation store.WikiTreeRelation, withType bool) map[string]any {
	bean := map[string]any{
		"id": relation.ID(), "status": relation.Status(), "title": relation.Title(),
		"spaceId": relation.SpaceID(), "childPosition": relation.ChildPosition,
	}
	if withType {
		bean["type"] = relation.Type()
	}
	return bean
}

// treeChildren answers a direct-children read. A page's older `children` read
// lists its child pages only.
func (h *Handler) treeChildren(w http.ResponseWriter, r *http.Request, ws, actor, id, nodeType string, pagesOnly bool) {
	if !validPageID(w, id) || !supportedQuery(w, r, "cursor", "limit", "sort") {
		return
	}
	allowed := childSorts
	if !pagesOnly {
		allowed = append(slices.Clone(childSorts), "title", "-title")
	}
	order := r.URL.Query().Get("sort")
	if order != "" && !slices.Contains(allowed, order) {
		failure(w, 400, "Unsupported child content sort order.")
		return
	}
	relations, err := h.Store.WikiTreeDescendants(r.Context(), ws, actor, id, nodeType, 1)
	if err != nil {
		writeError(w, err)
		return
	}
	if pagesOnly {
		relations = slices.DeleteFunc(relations, func(relation store.WikiTreeRelation) bool { return relation.Page == nil })
	}
	treeSort(relations, order)
	values := make([]any, 0, len(relations))
	for _, relation := range relations {
		values = append(values, treeChildBean(relation, !pagesOnly))
	}
	h.list(w, r, values)
}

// treeAncestors answers from the top of the space down. With a limit, the
// nearest ancestors are the ones returned, so a caller continues from the
// highest one it received, as Confluence documents.
func (h *Handler) treeAncestors(w http.ResponseWriter, r *http.Request, ws, actor, id, nodeType string) {
	if !validPageID(w, id) || !supportedQuery(w, r, "limit") {
		return
	}
	limit, ok := boundedLimit(w, r, 25, 250)
	if !ok {
		return
	}
	relations, err := h.Store.WikiTreeAncestors(r.Context(), ws, actor, id, nodeType)
	if err != nil {
		writeError(w, err)
		return
	}
	if len(relations) > limit {
		relations = relations[len(relations)-limit:]
	}
	values := make([]any, 0, len(relations))
	for _, relation := range relations {
		values = append(values, map[string]string{"id": relation.ID(), "type": relation.Type()})
	}
	respond(w, 200, map[string]any{"results": values, "_links": map[string]string{"base": h.BaseURL + "/wiki"}})
}

func (h *Handler) treeDescendants(w http.ResponseWriter, r *http.Request, ws, actor, id, nodeType string) {
	if !validPageID(w, id) || !supportedQuery(w, r, "cursor", "limit", "depth") {
		return
	}
	depth, ok := boundedIntQuery(w, r, "depth", 2, 1, 10)
	if !ok {
		return
	}
	relations, err := h.Store.WikiTreeDescendants(r.Context(), ws, actor, id, nodeType, depth)
	if err != nil {
		writeError(w, err)
		return
	}
	values := make([]any, 0, len(relations))
	for _, relation := range relations {
		values = append(values, map[string]any{
			"id": relation.ID(), "status": relation.Status(), "title": relation.Title(),
			"type": relation.Type(), "parentId": relation.ParentID(), "depth": relation.Depth,
			"childPosition": relation.ChildPosition,
		})
	}
	h.list(w, r, values)
}

func (h *Handler) pageChildren(w http.ResponseWriter, r *http.Request, ws, actor, id string, directContent bool) {
	h.treeChildren(w, r, ws, actor, id, "page", !directContent)
}

func (h *Handler) pageAncestors(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	h.treeAncestors(w, r, ws, actor, id, "page")
}

func (h *Handler) pageDescendants(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	h.treeDescendants(w, r, ws, actor, id, "page")
}
