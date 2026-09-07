package confluence

import (
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/e6qu/zzira/internal/store"
)

func (h *Handler) classificationLevels(w http.ResponseWriter, r *http.Request) {
	if !supportedQuery(w, r) {
		return
	}
	levels := make([]map[string]any, 0, len(classificationLevels))
	for _, level := range classificationLevels {
		levels = append(levels, level)
	}
	sort.SliceStable(levels, func(i, j int) bool { return levels[i]["order"].(int) < levels[j]["order"].(int) })
	respond(w, 200, levels)
}

func (h *Handler) pageClassification(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !validPageID(w, id) || !supportedQuery(w, r, "status") {
		return
	}
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "current"
	}
	if status != "current" && status != "draft" && status != "archived" {
		failure(w, 400, "Unsupported page status.")
		return
	}
	page, err := h.Store.WikiPage(r.Context(), ws, actor, id)
	if err != nil {
		writeError(w, err)
		return
	}
	if page.Status != status {
		failure(w, 404, "Page not found with the requested status.")
		return
	}
	level, ok := classificationLevels[page.ClassificationLevel]
	if !ok {
		failure(w, 404, "Page does not have a classification level.")
		return
	}
	respond(w, 200, level)
}

func (h *Handler) setPageClassification(w http.ResponseWriter, r *http.Request, ws, actor, id string, reset bool) {
	if !validPageID(w, id) || !supportedQuery(w, r) {
		return
	}
	var input classificationWrite
	if !decode(w, r, &input) {
		return
	}
	if input.Status != "current" || (!reset && classificationLevels[input.ID] == nil) || (reset && input.ID != "") {
		failure(w, 400, "A current, supported classification level is required.")
		return
	}
	levelID := input.ID
	if reset {
		levelID = ""
	}
	if _, err := h.Commands.SetWikiPageClassification(r.Context(), ws, actor, id, levelID); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(204)
}

func (h *Handler) pageLikeCount(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !validPageID(w, id) || !supportedQuery(w, r) {
		return
	}
	likes, err := h.Store.WikiPageLikes(r.Context(), ws, actor, id)
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, map[string]int{"count": len(likes)})
}

func (h *Handler) pageLikeUsers(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !validPageID(w, id) || !supportedQuery(w, r, "cursor", "limit") {
		return
	}
	likes, err := h.Store.WikiPageLikes(r.Context(), ws, actor, id)
	if err != nil {
		writeError(w, err)
		return
	}
	values := make([]any, 0, len(likes))
	for _, accountID := range likes {
		values = append(values, map[string]string{"accountId": accountID})
	}
	h.list(w, r, values)
}

func (h *Handler) pageOperations(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !validPageID(w, id) || !supportedQuery(w, r) {
		return
	}
	if _, err := h.Store.WikiPage(r.Context(), ws, actor, id); err != nil {
		writeError(w, err)
		return
	}
	allowed, err := h.Store.CanUpdateWikiPage(r.Context(), ws, actor, id)
	if err != nil {
		writeError(w, err)
		return
	}
	operations := []any{map[string]string{"operation": "read", "targetType": "page"}}
	if allowed {
		operations = append(operations, map[string]string{"operation": "update", "targetType": "page"}, map[string]string{"operation": "delete", "targetType": "page"})
	}
	respond(w, 200, map[string]any{"operations": operations})
}

func (h *Handler) pageCustomContent(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !validPageID(w, id) || !supportedQuery(w, r, "type", "sort", "cursor", "limit", "body-format") {
		return
	}
	contentType := strings.TrimSpace(r.URL.Query().Get("type"))
	if contentType == "" {
		failure(w, 400, "Custom content type is required.")
		return
	}
	format := r.URL.Query().Get("body-format")
	if format != "" && format != "storage" && format != "raw" {
		failure(w, 400, "Only storage or raw custom-content bodies are supported.")
		return
	}
	contents, err := h.Store.WikiPageCustomContent(r.Context(), ws, actor, id, contentType, r.URL.Query().Get("sort"))
	if err != nil {
		writeError(w, err)
		return
	}
	values := make([]any, 0, len(contents))
	for _, content := range contents {
		bean := map[string]any{"id": content.ID, "type": content.Type, "status": content.Status, "title": content.Title, "spaceId": content.SpaceID, "pageId": content.PageID, "authorId": content.AuthorID, "createdAt": content.CreatedAt, "version": content.Version, "_links": map[string]string{"base": h.BaseURL + "/wiki", "webui": "/spaces/" + content.SpaceID + "/pages/" + content.PageID}}
		if format != "" {
			if format != content.BodyRepresentation {
				failure(w, 400, "The requested body format is unavailable for this custom content type.")
				return
			}
			bean["body"] = map[string]any{format: content.Body}
		}
		values = append(values, bean)
	}
	h.list(w, r, values)
}

func (h *Handler) redactPage(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !validPageID(w, id) || !supportedQuery(w, r) {
		return
	}
	var input blogRedactionWrite
	if !decode(w, r, &input) {
		return
	}
	_, title, body, err := h.Commands.RedactWikiPage(r.Context(), ws, actor, id, input.CreatedAt, input.VersionNumber, input.CleanHistory, input.Title.Redactions, input.Body.Redactions)
	if err != nil {
		if errors.Is(err, store.ErrWikiConflict) {
			failure(w, 400, "createdAt or versionNumber is out of date.")
			return
		}
		writeError(w, err)
		return
	}
	respond(w, 202, map[string]any{"title": map[string]any{"redactions": title}, "body": map[string]any{"redactions": body}})
}
