package confluence

import (
	"cmp"
	"net/http"
	"slices"
	"sort"
	"strconv"

	"github.com/e6qu/zzira/internal/models"
)

type folderWrite struct {
	SpaceID  string `json:"spaceId"`
	Title    string `json:"title"`
	ParentID string `json:"parentId"`
}

func boundedIntQuery(w http.ResponseWriter, r *http.Request, name string, fallback, minimum, maximum int) (int, bool) {
	value := fallback
	if raw := r.URL.Query().Get(name); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < minimum || parsed > maximum {
			failure(w, 400, name+" must be between "+strconv.Itoa(minimum)+" and "+strconv.Itoa(maximum)+".")
			return 0, false
		}
		value = parsed
	}
	return value, true
}

func boundedLimit(w http.ResponseWriter, r *http.Request, fallback, maximum int) (int, bool) {
	return boundedIntQuery(w, r, "limit", fallback, 1, maximum)
}

func (h *Handler) folderBean(content *models.WikiContent) map[string]any {
	return map[string]any{
		"id": content.ID, "type": content.Type, "status": content.Status,
		"title": content.Title, "parentId": content.ParentID, "parentType": content.ParentType,
		"position": content.Position, "authorId": content.AuthorID, "ownerId": content.OwnerID,
		"createdAt": content.CreatedAt, "spaceId": content.SpaceID, "version": content.Version,
		"_links": map[string]string{"base": h.BaseURL + "/wiki", "webui": "/wiki/spaces/" + content.SpaceID + "#folder-" + content.ID},
	}
}

func (h *Handler) createFolder(w http.ResponseWriter, r *http.Request, ws, actor string) {
	if !supportedQuery(w, r) {
		return
	}
	var input folderWrite
	if !decode(w, r, &input) {
		return
	}
	content, err := h.Commands.CreateWikiContent(r.Context(), ws, actor, models.WikiContent{Type: "folder", SpaceID: input.SpaceID, Title: input.Title, ParentID: input.ParentID})
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, h.folderBean(content))
}

func (h *Handler) folder(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !validPageID(w, id) || !supportedQuery(w, r, "include-collaborators", "include-direct-children", "include-operations", "include-properties") {
		return
	}
	content, err := h.Store.WikiContent(r.Context(), ws, actor, id, "folder")
	if err != nil {
		writeError(w, err)
		return
	}
	bean := h.folderBean(content)
	if r.URL.Query().Get("include-collaborators") == "true" {
		bean["collaborators"] = map[string]any{"results": []any{}}
	}
	if r.URL.Query().Get("include-direct-children") == "true" {
		relations, err := h.Store.WikiContentDescendants(r.Context(), ws, actor, id, "folder", 1)
		if err != nil {
			writeError(w, err)
			return
		}
		bean["directChildren"] = map[string]any{"results": contentChildren(relations)}
	}
	if r.URL.Query().Get("include-operations") == "true" {
		operations, err := h.folderOperationList(r, ws, actor, id)
		if err != nil {
			writeError(w, err)
			return
		}
		bean["operations"] = map[string]any{"results": operations}
	}
	if r.URL.Query().Get("include-properties") == "true" {
		properties, err := h.Store.WikiContentProperties(r.Context(), ws, actor, id, "folder", "")
		if err != nil {
			writeError(w, err)
			return
		}
		bean["properties"] = map[string]any{"results": properties}
	}
	respond(w, 200, bean)
}

func (h *Handler) deleteFolder(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !validPageID(w, id) || !supportedQuery(w, r) {
		return
	}
	if err := h.Commands.DeleteWikiContent(r.Context(), ws, actor, id, "folder"); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(204)
}

func (h *Handler) folderAncestors(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !validPageID(w, id) || !supportedQuery(w, r, "limit") {
		return
	}
	limit, ok := boundedLimit(w, r, 25, 250)
	if !ok {
		return
	}
	ancestors, err := h.Store.WikiContentAncestors(r.Context(), ws, actor, id, "folder")
	if err != nil {
		writeError(w, err)
		return
	}
	if len(ancestors) > limit {
		ancestors = ancestors[len(ancestors)-limit:]
	}
	respond(w, 200, map[string]any{"results": ancestors, "_links": map[string]string{"base": h.BaseURL + "/wiki"}})
}

func contentChildren(relations []models.WikiContentRelation) []any {
	values := make([]any, 0, len(relations))
	for _, relation := range relations {
		values = append(values, map[string]any{"id": relation.Content.ID, "status": relation.Content.Status, "title": relation.Content.Title, "type": relation.Content.Type, "spaceId": relation.Content.SpaceID, "childPosition": relation.ChildPosition})
	}
	return values
}

func (h *Handler) folderDescendants(w http.ResponseWriter, r *http.Request, ws, actor, id string, direct bool) {
	queries := []string{"cursor", "limit", "depth"}
	if direct {
		queries = []string{"cursor", "limit", "sort"}
	}
	if !validPageID(w, id) || !supportedQuery(w, r, queries...) {
		return
	}
	depth := 1
	if !direct {
		var ok bool
		depth, ok = boundedIntQuery(w, r, "depth", 2, 1, 10)
		if !ok {
			return
		}
	}
	relations, err := h.Store.WikiContentDescendants(r.Context(), ws, actor, id, "folder", depth)
	if err != nil {
		writeError(w, err)
		return
	}
	if direct {
		order := r.URL.Query().Get("sort")
		allowed := []string{"created-date", "-created-date", "id", "-id", "child-position", "-child-position", "modified-date", "-modified-date", "title", "-title"}
		if order != "" && !slices.Contains(allowed, order) {
			failure(w, 400, "Unsupported child content sort order.")
			return
		}
		sort.SliceStable(relations, func(i, j int) bool {
			left, right := relations[i].Content, relations[j].Content
			comparison := cmp.Compare(left.Position, right.Position)
			switch order {
			case "id", "-id":
				comparison = cmp.Compare(left.ID, right.ID)
			case "title", "-title":
				comparison = cmp.Compare(left.Title, right.Title)
			case "created-date", "-created-date":
				comparison = cmp.Compare(left.CreatedAt, right.CreatedAt)
			case "modified-date", "-modified-date":
				comparison = cmp.Compare(left.Version.CreatedAt, right.Version.CreatedAt)
			}
			if comparison == 0 {
				comparison = cmp.Compare(left.ID, right.ID)
			}
			if len(order) > 0 && order[0] == '-' {
				return comparison > 0
			}
			return comparison < 0
		})
		h.list(w, r, contentChildren(relations))
		return
	}
	values := make([]any, 0, len(relations))
	for _, relation := range relations {
		values = append(values, map[string]any{"id": relation.Content.ID, "status": relation.Content.Status, "title": relation.Content.Title, "type": relation.Content.Type, "parentId": relation.Content.ParentID, "depth": relation.Depth, "childPosition": relation.ChildPosition})
	}
	h.list(w, r, values)
}

func (h *Handler) folderOperationList(r *http.Request, ws, actor, id string) ([]any, error) {
	allowed, err := h.Store.CanUpdateWikiContent(r.Context(), ws, actor, id, "folder")
	if err != nil {
		return nil, err
	}
	operations := []any{map[string]string{"operation": "read", "targetType": "folder"}}
	if allowed {
		operations = append(operations, map[string]string{"operation": "update", "targetType": "folder"}, map[string]string{"operation": "delete", "targetType": "folder"})
	}
	return operations, nil
}

func (h *Handler) folderOperations(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !validPageID(w, id) || !supportedQuery(w, r) {
		return
	}
	operations, err := h.folderOperationList(r, ws, actor, id)
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, map[string]any{"operations": operations})
}

func (h *Handler) folderProperties(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !validPageID(w, id) || !supportedQuery(w, r, "key", "sort", "cursor", "limit") {
		return
	}
	properties, err := h.Store.WikiContentProperties(r.Context(), ws, actor, id, "folder", r.URL.Query().Get("key"))
	if err != nil {
		writeError(w, err)
		return
	}
	order := r.URL.Query().Get("sort")
	if order != "" && order != "key" && order != "-key" {
		failure(w, 400, "Unsupported content property sort order.")
		return
	}
	if order == "-key" {
		sort.SliceStable(properties, func(i, j int) bool { return properties[i].Key > properties[j].Key })
	}
	values := make([]any, len(properties))
	for i := range properties {
		values[i] = properties[i]
	}
	h.list(w, r, values)
}

func (h *Handler) folderProperty(w http.ResponseWriter, r *http.Request, ws, actor, id, propertyID string) {
	if !validPageID(w, id) || !validPageID(w, propertyID) || !supportedQuery(w, r) {
		return
	}
	property, err := h.Store.WikiContentProperty(r.Context(), ws, actor, id, "folder", propertyID)
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, property)
}

func (h *Handler) createFolderProperty(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !validPageID(w, id) || !supportedQuery(w, r) {
		return
	}
	var input attachmentPropertyWrite
	if !decode(w, r, &input) || !validAttachmentProperty(w, input, false) {
		return
	}
	property, err := h.Commands.CreateWikiContentProperty(r.Context(), ws, actor, id, "folder", input.Key, input.Value)
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, property)
}

func (h *Handler) updateFolderProperty(w http.ResponseWriter, r *http.Request, ws, actor, id, propertyID string) {
	if !validPageID(w, id) || !validPageID(w, propertyID) || !supportedQuery(w, r) {
		return
	}
	var input attachmentPropertyWrite
	if !decode(w, r, &input) || !validAttachmentProperty(w, input, true) {
		return
	}
	property, err := h.Commands.UpdateWikiContentProperty(r.Context(), ws, actor, id, "folder", propertyID, input.Key, input.Value, input.Version.Number, input.Version.Message)
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, property)
}

func (h *Handler) deleteFolderProperty(w http.ResponseWriter, r *http.Request, ws, actor, id, propertyID string) {
	if !validPageID(w, id) || !validPageID(w, propertyID) || !supportedQuery(w, r) {
		return
	}
	if err := h.Commands.DeleteWikiContentProperty(r.Context(), ws, actor, id, "folder", propertyID); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(204)
}
