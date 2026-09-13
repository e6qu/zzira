package confluence

import (
	"net/http"
	"sort"
)

// Comment properties follow the same contract as the other content property
// endpoints: key and value on create, the next version on update, 25 results a
// page by default, and a comment the caller may not see reported as missing.

func (h *Handler) commentProperties(w http.ResponseWriter, r *http.Request, ws, actor, commentID string) {
	if !validPageID(w, commentID) || !supportedQuery(w, r, "key", "sort", "cursor", "limit") {
		return
	}
	order := r.URL.Query().Get("sort")
	if order != "" && order != "key" && order != "-key" {
		failure(w, 400, "Unsupported content property sort order.")
		return
	}
	properties, err := h.Store.WikiCommentProperties(r.Context(), ws, actor, commentID, r.URL.Query().Get("key"))
	if err != nil {
		writeError(w, err)
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

func (h *Handler) commentProperty(w http.ResponseWriter, r *http.Request, ws, actor, commentID, propertyID string) {
	if !validPageID(w, commentID) || !validPageID(w, propertyID) || !supportedQuery(w, r) {
		return
	}
	property, err := h.Store.WikiCommentProperty(r.Context(), ws, actor, commentID, propertyID)
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, property)
}

func (h *Handler) createCommentProperty(w http.ResponseWriter, r *http.Request, ws, actor, commentID string) {
	if !validPageID(w, commentID) || !supportedQuery(w, r) {
		return
	}
	var input attachmentPropertyWrite
	if !decode(w, r, &input) || !validAttachmentProperty(w, input, false) {
		return
	}
	property, err := h.Commands.CreateWikiCommentProperty(r.Context(), ws, actor, commentID, input.Key, input.Value)
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, property)
}

func (h *Handler) updateCommentProperty(w http.ResponseWriter, r *http.Request, ws, actor, commentID, propertyID string) {
	if !validPageID(w, commentID) || !validPageID(w, propertyID) || !supportedQuery(w, r) {
		return
	}
	var input attachmentPropertyWrite
	if !decode(w, r, &input) || !validAttachmentProperty(w, input, true) {
		return
	}
	property, err := h.Commands.UpdateWikiCommentProperty(r.Context(), ws, actor, commentID, propertyID, input.Key, input.Value, input.Version.Number, input.Version.Message)
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, property)
}

func (h *Handler) deleteCommentProperty(w http.ResponseWriter, r *http.Request, ws, actor, commentID, propertyID string) {
	if !validPageID(w, commentID) || !validPageID(w, propertyID) || !supportedQuery(w, r) {
		return
	}
	if err := h.Commands.DeleteWikiCommentProperty(r.Context(), ws, actor, commentID, propertyID); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(204)
}
