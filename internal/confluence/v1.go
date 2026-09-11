package confluence

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/authn"
	"github.com/e6qu/zzira/internal/models"
)

// V1Handler exposes the legacy Confluence routes still used for label writes.
// It shares the same command and visibility boundaries as the v2 handler.
type V1Handler struct{ *Handler }

func (h *V1Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	actor, err := authn.Identify(r.Context(), h.Store, r)
	if err != nil {
		w.Header().Set("WWW-Authenticate", `Basic realm="zzira"`)
		failure(w, 401, "Authentication required.")
		return
	}
	ws, err := h.Store.WorkspaceBySlug(r.Context(), h.WorkspaceSlug)
	if err != nil {
		writeError(w, err)
		return
	}
	member, err := h.Store.IsMember(r.Context(), ws, actor)
	if err != nil {
		writeError(w, err)
		return
	}
	if !member {
		failure(w, 403, "Workspace membership required.")
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/wiki/rest/api/"), "/"), "/")
	switch {
	case len(parts) == 1 && parts[0] == "longtask" && r.Method == "GET":
		h.v1LongTasks(w, r, ws, actor, "")
	case len(parts) == 2 && parts[0] == "longtask" && r.Method == "GET":
		h.v1LongTasks(w, r, ws, actor, parts[1])
	case len(parts) == 2 && parts[0] == "content" && parts[1] == "archive" && r.Method == "POST":
		h.v1ArchivePages(w, r, ws, actor)
	case len(parts) == 3 && parts[0] == "content" && parts[2] == "copy" && r.Method == "POST":
		h.v1CopyPage(w, r, ws, actor, parts[1])
	case len(parts) == 4 && parts[0] == "content" && parts[2] == "pagehierarchy" && parts[3] == "copy" && r.Method == "POST":
		h.v1CopyPageHierarchy(w, r, ws, actor, parts[1])
	case len(parts) == 3 && parts[0] == "content" && parts[2] == "pageTree" && r.Method == "DELETE":
		h.v1TrashPageTree(w, r, ws, actor, parts[1])
	case len(parts) == 5 && parts[0] == "content" && parts[2] == "move" && r.Method == "PUT":
		h.v1MovePage(w, r, ws, actor, parts[1], parts[3], parts[4])
	case len(parts) == 1 && parts[0] == "content-states" && r.Method == "GET":
		h.v1CustomContentStates(w, r, ws, actor)
	case len(parts) == 3 && parts[0] == "content" && parts[2] == "state":
		h.v1ContentState(w, r, ws, actor, parts[1])
	case len(parts) == 4 && parts[0] == "content" && parts[2] == "state" && parts[3] == "available" && r.Method == "GET":
		h.v1AvailableContentStates(w, r, ws, actor, parts[1])
	case len(parts) == 3 && parts[0] == "space" && parts[2] == "state" && r.Method == "GET":
		h.v1SpaceContentStates(w, r, ws, actor, parts[1])
	case len(parts) == 4 && parts[0] == "space" && parts[2] == "state" && parts[3] == "settings" && r.Method == "GET":
		h.v1SpaceContentStateSettings(w, r, ws, actor, parts[1])
	case len(parts) == 4 && parts[0] == "space" && parts[2] == "state" && parts[3] == "content" && r.Method == "GET":
		h.v1SpaceContentStateContent(w, r, ws, actor, parts[1])
	case len(parts) == 4 && parts[0] == "content" && parts[2] == "child" && parts[3] == "attachment":
		h.v1SaveAttachments(w, r, ws, actor, parts[1])
	case len(parts) == 4 && parts[0] == "content" && parts[2] == "notification" && (parts[3] == "child-created" || parts[3] == "created") && r.Method == "GET":
		h.v1ContentWatches(w, r, ws, actor, parts[1], parts[3])
	case len(parts) == 3 && parts[0] == "content" && parts[2] == "descendant" && r.Method == "GET":
		h.contentDescendants(w, r, ws, actor, parts[1])
	case len(parts) == 4 && parts[0] == "content" && parts[2] == "descendant" && r.Method == "GET":
		h.contentDescendantsByType(w, r, ws, actor, parts[1], parts[3])
	case len(parts) == 5 && parts[0] == "content" && parts[2] == "child" && parts[3] == "attachment" && r.Method == "PUT":
		h.v1UpdateAttachmentProperties(w, r, ws, actor, parts[1], parts[4])
	case len(parts) == 6 && parts[0] == "content" && parts[2] == "child" && parts[3] == "attachment" && parts[5] == "data" && r.Method == "POST":
		h.v1UpdateAttachmentData(w, r, ws, actor, parts[1], parts[4])
	case len(parts) == 6 && parts[0] == "content" && parts[2] == "child" && parts[3] == "attachment" && parts[5] == "download" && r.Method == "GET":
		h.v1DownloadAttachment(w, r, ws, actor, parts[1], parts[4])
	case len(parts) == 3 && parts[0] == "content" && parts[2] == "restriction":
		h.contentRestrictions(w, r, ws, actor, parts[1])
	case len(parts) == 4 && parts[0] == "content" && parts[2] == "restriction" && parts[3] == "byOperation" && r.Method == "GET":
		h.contentRestrictionsByOperation(w, r, ws, actor, parts[1])
	case len(parts) == 5 && parts[0] == "content" && parts[2] == "restriction" && parts[3] == "byOperation" && r.Method == "GET":
		h.contentRestrictionOperation(w, r, ws, actor, parts[1], parts[4])
	case len(parts) == 7 && parts[0] == "content" && parts[2] == "restriction" && parts[3] == "byOperation" && parts[5] == "byGroupId":
		h.contentRestrictionGroup(w, r, ws, actor, parts[1], parts[4], parts[6])
	case len(parts) == 6 && parts[0] == "content" && parts[2] == "restriction" && parts[3] == "byOperation" && parts[5] == "user":
		h.contentRestrictionUser(w, r, ws, actor, parts[1], parts[4])
	case len(parts) == 3 && parts[0] == "content" && parts[2] == "label" && r.Method == "POST":
		h.addContentLabels(w, r, ws, actor, parts[1])
	case len(parts) == 3 && parts[0] == "content" && parts[2] == "label" && r.Method == "DELETE":
		h.removeContentLabel(w, r, ws, actor, parts[1], r.URL.Query().Get("name"))
	case len(parts) == 4 && parts[0] == "content" && parts[2] == "label" && r.Method == "DELETE":
		h.removeContentLabel(w, r, ws, actor, parts[1], parts[3])
	case len(parts) == 1 && parts[0] == "label" && r.Method == "GET":
		h.labelContent(w, r, ws, actor)
	case len(parts) == 3 && parts[0] == "space" && parts[2] == "label" && r.Method == "GET":
		h.spaceLabels(w, r, ws, actor, parts[1])
	case len(parts) == 3 && parts[0] == "space" && parts[2] == "label" && r.Method == "POST":
		h.addSpaceLabels(w, r, ws, actor, parts[1])
	case len(parts) == 3 && parts[0] == "space" && parts[2] == "label" && r.Method == "DELETE":
		h.removeSpaceLabel(w, r, ws, actor, parts[1])
	case len(parts) == 3 && parts[0] == "space" && parts[2] == "watch" && r.Method == "GET":
		h.v1SpaceWatchers(w, r, ws, actor, parts[1])
	case len(parts) == 4 && parts[0] == "user" && parts[1] == "watch":
		h.v1UserWatch(w, r, ws, actor, parts[2], parts[3])
	case len(parts) == 1 && parts[0] == "group":
		h.v1Groups(w, r, ws, actor)
	case len(parts) == 2 && parts[0] == "group" && parts[1] == "by-id":
		h.v1GroupByID(w, r, ws, actor)
	case len(parts) == 2 && parts[0] == "group" && parts[1] == "picker" && r.Method == "GET":
		h.v1GroupPicker(w, r, ws, actor)
	case len(parts) == 2 && parts[0] == "group" && parts[1] == "userByGroupId":
		h.v1GroupMembership(w, r, ws, actor)
	case len(parts) == 3 && parts[0] == "group" && parts[2] == "membersByGroupId" && r.Method == "GET":
		h.v1GroupMembers(w, r, ws, actor, parts[1])
	case len(parts) == 1 && parts[0] == "user" && r.Method == "GET":
		h.v1User(w, r, ws, actor)
	case len(parts) == 2 && parts[0] == "user" && parts[1] == "current" && r.Method == "GET":
		h.v1CurrentUser(w, r, ws, actor)
	case len(parts) == 2 && parts[0] == "user" && parts[1] == "anonymous" && r.Method == "GET":
		h.v1AnonymousUser(w, r)
	case len(parts) == 2 && parts[0] == "user" && parts[1] == "bulk" && r.Method == "GET":
		h.v1BulkUsers(w, r, ws, actor, false)
	case len(parts) == 2 && parts[0] == "user" && parts[1] == "email" && r.Method == "GET":
		h.v1UserEmail(w, r, ws, actor)
	case len(parts) == 3 && parts[0] == "user" && parts[1] == "email" && parts[2] == "bulk" && r.Method == "GET":
		h.v1BulkUsers(w, r, ws, actor, true)
	case len(parts) == 2 && parts[0] == "user" && parts[1] == "memberof" && r.Method == "GET":
		h.v1UserGroups(w, r, ws, actor)
	case len(parts) == 2 && parts[0] == "search" && parts[1] == "user" && r.Method == "GET":
		h.v1SearchUsers(w, r, ws, actor)
	case len(parts) == 3 && parts[0] == "user" && parts[2] == "property" && r.Method == "GET":
		h.v1UserProperties(w, r, ws, actor, parts[1])
	case len(parts) == 4 && parts[0] == "user" && parts[2] == "property":
		h.v1UserProperty(w, r, ws, actor, parts[1], parts[3])
	default:
		failure(w, 404, "This Confluence v1 resource is not implemented.")
	}
}

func decodeV1Labels(w http.ResponseWriter, r *http.Request) ([]models.WikiLabel, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		failure(w, 400, "Invalid label request.")
		return nil, false
	}
	var labels []models.WikiLabel
	if err := json.Unmarshal(raw, &labels); err != nil {
		var label models.WikiLabel
		if objectErr := json.Unmarshal(raw, &label); objectErr != nil {
			failure(w, 400, "Expected a label object or array.")
			return nil, false
		}
		labels = []models.WikiLabel{label}
	}
	return labels, true
}

func v1LabelBean(label models.WikiLabel) map[string]string {
	return map[string]string{"id": label.ID, "name": label.Name, "prefix": label.Prefix, "label": label.Prefix + ":" + label.Name}
}

func (h *V1Handler) v1LabelList(w http.ResponseWriter, r *http.Request, labels []models.WikiLabel) {
	start, limit := 0, 200
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
	start = min(start, len(labels))
	end := min(start+limit, len(labels))
	results := make([]any, 0, end-start)
	for _, label := range labels[start:end] {
		results = append(results, v1LabelBean(label))
	}
	respond(w, 200, map[string]any{"results": results, "start": start, "limit": limit, "size": len(results), "_links": map[string]string{"base": h.BaseURL + "/wiki"}})
}

func (h *V1Handler) addContentLabels(w http.ResponseWriter, r *http.Request, ws, actor, pageID string) {
	if !supportedQuery(w, r) {
		return
	}
	labels, ok := decodeV1Labels(w, r)
	if !ok {
		return
	}
	labels, err := h.Commands.AddWikiPageLabels(r.Context(), ws, actor, pageID, labels)
	if err != nil {
		writeError(w, err)
		return
	}
	h.v1LabelList(w, r, labels)
}

func (h *V1Handler) removeContentLabel(w http.ResponseWriter, r *http.Request, ws, actor, pageID, name string) {
	if name == "" {
		failure(w, 400, "Label name is required.")
		return
	}
	if err := h.Commands.RemoveWikiPageLabel(r.Context(), ws, actor, pageID, "global", name); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(204)
}

func (h *V1Handler) labelContent(w http.ResponseWriter, r *http.Request, ws, actor string) {
	if !supportedQuery(w, r, "name", "type", "start", "limit") {
		return
	}
	name := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("name")))
	if name == "" {
		failure(w, 400, "Label name is required.")
		return
	}
	if contentType := r.URL.Query().Get("type"); contentType != "" && contentType != "page" {
		failure(w, 400, "Only page label content is currently supported.")
		return
	}
	labels, err := h.Store.WikiLabels(r.Context(), ws, actor)
	if err != nil {
		writeError(w, err)
		return
	}
	var label *models.WikiLabel
	for i := range labels {
		if labels[i].Name == name && (label == nil || labels[i].Prefix == "global") {
			copy := labels[i]
			label = &copy
		}
	}
	if label == nil {
		failure(w, 404, "Label not found.")
		return
	}
	pages, err := h.Store.WikiPagesByLabel(r.Context(), ws, actor, label.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	results := make([]any, 0, len(pages))
	for _, page := range pages {
		results = append(results, map[string]any{"id": page.ID, "type": "page", "status": page.Status, "title": page.Title, "_links": map[string]string{"webui": "/spaces/" + page.SpaceID + "/pages/" + page.ID}})
	}
	respond(w, 200, map[string]any{"label": v1LabelBean(*label), "associatedContents": map[string]any{"results": results, "start": 0, "limit": len(results), "size": len(results)}})
}

func (h *V1Handler) spaceLabels(w http.ResponseWriter, r *http.Request, ws, actor, key string) {
	if !supportedQuery(w, r, "prefix", "start", "limit") {
		return
	}
	if prefix := r.URL.Query().Get("prefix"); prefix != "" && prefix != "global" && prefix != "my" && prefix != "team" {
		failure(w, 400, "Unsupported label prefix.")
		return
	}
	space, err := h.Store.WikiSpaceByKey(r.Context(), ws, actor, key)
	if err != nil {
		writeError(w, err)
		return
	}
	labels, err := h.Store.WikiSpaceLabels(r.Context(), ws, actor, space.ID, false)
	if err != nil {
		writeError(w, err)
		return
	}
	if prefix := r.URL.Query().Get("prefix"); prefix != "" {
		filtered := labels[:0]
		for _, label := range labels {
			if label.Prefix == prefix {
				filtered = append(filtered, label)
			}
		}
		labels = filtered
	}
	h.v1LabelList(w, r, labels)
}

func (h *V1Handler) addSpaceLabels(w http.ResponseWriter, r *http.Request, ws, actor, key string) {
	if !supportedQuery(w, r) {
		return
	}
	space, err := h.Store.WikiSpaceByKey(r.Context(), ws, actor, key)
	if err != nil {
		writeError(w, err)
		return
	}
	labels, ok := decodeV1Labels(w, r)
	if !ok {
		return
	}
	labels, err = h.Commands.AddWikiSpaceLabels(r.Context(), ws, actor, space.ID, labels)
	if err != nil {
		writeError(w, err)
		return
	}
	h.v1LabelList(w, r, labels)
}

func (h *V1Handler) removeSpaceLabel(w http.ResponseWriter, r *http.Request, ws, actor, key string) {
	if !supportedQuery(w, r, "name", "prefix") {
		return
	}
	space, err := h.Store.WikiSpaceByKey(r.Context(), ws, actor, key)
	if err != nil {
		writeError(w, err)
		return
	}
	name, prefix := r.URL.Query().Get("name"), r.URL.Query().Get("prefix")
	if prefix == "" {
		prefix = "global"
	}
	if err := h.Commands.RemoveWikiSpaceLabel(r.Context(), ws, actor, space.ID, prefix, name); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(204)
}
