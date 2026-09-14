package confluence

import (
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

func (h *Handler) contentBean(content *models.WikiContent) map[string]any {
	bean := map[string]any{
		"id": content.ID, "type": content.Type, "status": content.Status,
		"title": content.Title, "parentId": content.ParentID, "parentType": content.ParentType,
		"position": content.Position, "authorId": content.AuthorID, "ownerId": content.OwnerID,
		"createdAt": content.CreatedAt, "spaceId": content.SpaceID, "version": content.Version,
		"_links": map[string]string{"base": h.BaseURL + "/wiki", "webui": "/wiki/spaces/" + content.SpaceID + "#" + content.Type + "-" + content.ID},
	}
	if content.EmbedURL != "" {
		bean["embedUrl"] = content.EmbedURL
	}
	if content.Type == "database" {
		bean["private"] = content.Private
	}
	if content.Type == "whiteboard" {
		bean["_links"].(map[string]string)["editui"] = "/wiki/spaces/" + content.SpaceID + "#whiteboard-" + content.ID
	}
	return bean
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
	respond(w, 200, h.contentBean(content))
}

func (h *Handler) hierarchicalContent(w http.ResponseWriter, r *http.Request, ws, actor, id, contentType string) {
	if !validPageID(w, id) || !supportedQuery(w, r, "include-collaborators", "include-direct-children", "include-operations", "include-properties") {
		return
	}
	content, err := h.Store.WikiTreeContent(r.Context(), ws, actor, id, contentType)
	if err != nil {
		writeError(w, err)
		return
	}
	bean := h.contentBean(content)
	if r.URL.Query().Get("include-collaborators") == "true" {
		bean["collaborators"] = map[string]any{"results": []any{}}
	}
	if r.URL.Query().Get("include-direct-children") == "true" {
		relations, err := h.Store.WikiTreeDescendants(r.Context(), ws, actor, id, contentType, 1)
		if err != nil {
			writeError(w, err)
			return
		}
		values := make([]any, 0, len(relations))
		for _, relation := range relations {
			values = append(values, treeChildBean(relation, true))
		}
		bean["directChildren"] = map[string]any{"results": values}
	}
	if r.URL.Query().Get("include-operations") == "true" {
		operations, err := h.contentOperationList(r, ws, actor, id, contentType)
		if err != nil {
			writeError(w, err)
			return
		}
		bean["operations"] = map[string]any{"results": operations}
	}
	if r.URL.Query().Get("include-properties") == "true" {
		properties, err := h.Store.WikiContentProperties(r.Context(), ws, actor, id, contentType, "")
		if err != nil {
			writeError(w, err)
			return
		}
		bean["properties"] = map[string]any{"results": properties}
	}
	respond(w, 200, bean)
}

func (h *Handler) deleteHierarchicalContent(w http.ResponseWriter, r *http.Request, ws, actor, id, contentType string) {
	if !validPageID(w, id) || !supportedQuery(w, r) {
		return
	}
	if err := h.Commands.DeleteWikiContent(r.Context(), ws, actor, id, contentType); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(204)
}

func (h *Handler) contentAncestors(w http.ResponseWriter, r *http.Request, ws, actor, id, contentType string) {
	h.treeAncestors(w, r, ws, actor, id, contentType)
}

func (h *Handler) contentDescendants(w http.ResponseWriter, r *http.Request, ws, actor, id, contentType string, direct bool) {
	if contentType == "custom" {
		h.customContentChildren(w, r, ws, actor, id)
		return
	}
	if direct {
		h.treeChildren(w, r, ws, actor, id, contentType, false)
		return
	}
	h.treeDescendants(w, r, ws, actor, id, contentType)
}

func (h *Handler) customContentChildren(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !validPageID(w, id) || !supportedQuery(w, r, "cursor", "limit", "sort") {
		return
	}
	if order := r.URL.Query().Get("sort"); order != "" && order != "title" && order != "-title" && !slices.Contains(childSorts, order) {
		failure(w, 400, "Unsupported child content sort order.")
		return
	}
	children, err := h.Store.WikiCustomContentChildren(r.Context(), ws, actor, id)
	if err != nil {
		writeError(w, err)
		return
	}
	values := make([]any, 0, len(children))
	for i, child := range children {
		values = append(values, map[string]any{"id": child.ID, "status": child.Status, "title": child.Title, "type": child.Type, "spaceId": child.SpaceID, "childPosition": i})
	}
	h.list(w, r, values)
}

func (h *Handler) contentOperations(w http.ResponseWriter, r *http.Request, ws, actor, id, contentType string) {
	if !validPageID(w, id) || !supportedQuery(w, r) {
		return
	}
	operations, err := h.contentOperationList(r, ws, actor, id, contentType)
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, map[string]any{"operations": operations})
}

func (h *Handler) contentProperties(w http.ResponseWriter, r *http.Request, ws, actor, id, contentType string) {
	if !validPageID(w, id) || !supportedQuery(w, r, "key", "sort", "cursor", "limit") {
		return
	}
	properties, err := h.Store.WikiContentProperties(r.Context(), ws, actor, id, contentType, r.URL.Query().Get("key"))
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

func (h *Handler) contentProperty(w http.ResponseWriter, r *http.Request, ws, actor, id, propertyID, contentType string) {
	if !validPageID(w, id) || !validPageID(w, propertyID) || !supportedQuery(w, r) {
		return
	}
	property, err := h.Store.WikiContentProperty(r.Context(), ws, actor, id, contentType, propertyID)
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, property)
}

func (h *Handler) createContentProperty(w http.ResponseWriter, r *http.Request, ws, actor, id, contentType string) {
	if !validPageID(w, id) || !supportedQuery(w, r) {
		return
	}
	var input attachmentPropertyWrite
	if !decode(w, r, &input) || !validAttachmentProperty(w, input, false) {
		return
	}
	property, err := h.Commands.CreateWikiContentProperty(r.Context(), ws, actor, id, contentType, input.Key, input.Value)
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, property)
}

func (h *Handler) updateContentProperty(w http.ResponseWriter, r *http.Request, ws, actor, id, propertyID, contentType string) {
	if !validPageID(w, id) || !validPageID(w, propertyID) || !supportedQuery(w, r) {
		return
	}
	var input attachmentPropertyWrite
	if !decode(w, r, &input) || !validAttachmentProperty(w, input, true) {
		return
	}
	property, err := h.Commands.UpdateWikiContentProperty(r.Context(), ws, actor, id, contentType, propertyID, input.Key, input.Value, input.Version.Number, input.Version.Message)
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, property)
}

func (h *Handler) deleteContentProperty(w http.ResponseWriter, r *http.Request, ws, actor, id, propertyID, contentType string) {
	if !validPageID(w, id) || !validPageID(w, propertyID) || !supportedQuery(w, r) {
		return
	}
	if err := h.Commands.DeleteWikiContentProperty(r.Context(), ws, actor, id, contentType, propertyID); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(204)
}

func (h *Handler) folder(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	h.hierarchicalContent(w, r, ws, actor, id, "folder")
}
func (h *Handler) deleteFolder(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	h.deleteHierarchicalContent(w, r, ws, actor, id, "folder")
}
func (h *Handler) folderAncestors(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	h.contentAncestors(w, r, ws, actor, id, "folder")
}
func (h *Handler) folderDescendants(w http.ResponseWriter, r *http.Request, ws, actor, id string, direct bool) {
	h.contentDescendants(w, r, ws, actor, id, "folder", direct)
}
func (h *Handler) folderOperations(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	h.contentOperations(w, r, ws, actor, id, "folder")
}
func (h *Handler) folderProperties(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	h.contentProperties(w, r, ws, actor, id, "folder")
}
func (h *Handler) folderProperty(w http.ResponseWriter, r *http.Request, ws, actor, id, propertyID string) {
	h.contentProperty(w, r, ws, actor, id, propertyID, "folder")
}
func (h *Handler) createFolderProperty(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	h.createContentProperty(w, r, ws, actor, id, "folder")
}
func (h *Handler) updateFolderProperty(w http.ResponseWriter, r *http.Request, ws, actor, id, propertyID string) {
	h.updateContentProperty(w, r, ws, actor, id, propertyID, "folder")
}
func (h *Handler) deleteFolderProperty(w http.ResponseWriter, r *http.Request, ws, actor, id, propertyID string) {
	h.deleteContentProperty(w, r, ws, actor, id, propertyID, "folder")
}
