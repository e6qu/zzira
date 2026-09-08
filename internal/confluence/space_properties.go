package confluence

import (
	"net/http"
	"sort"
)

func (h *Handler) spaceProperties(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !validPageID(w, id) || !supportedQuery(w, r, "key", "sort", "cursor", "limit") {
		return
	}
	properties, err := h.Store.WikiSpaceProperties(r.Context(), ws, actor, id, r.URL.Query().Get("key"))
	if err != nil {
		writeError(w, err)
		return
	}
	order := r.URL.Query().Get("sort")
	if order != "" && order != "key" && order != "-key" {
		failure(w, 400, "Unsupported space property sort order.")
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

func (h *Handler) spaceProperty(w http.ResponseWriter, r *http.Request, ws, actor, spaceID, propertyID string) {
	if !validPageID(w, spaceID) || !validPageID(w, propertyID) || !supportedQuery(w, r) {
		return
	}
	property, err := h.Store.WikiSpaceProperty(r.Context(), ws, actor, spaceID, propertyID)
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, property)
}

func (h *Handler) createSpaceProperty(w http.ResponseWriter, r *http.Request, ws, actor, spaceID string) {
	if !validPageID(w, spaceID) || !supportedQuery(w, r) {
		return
	}
	var input attachmentPropertyWrite
	if !decode(w, r, &input) || !validAttachmentProperty(w, input, false) {
		return
	}
	property, err := h.Commands.CreateWikiSpaceProperty(r.Context(), ws, actor, spaceID, input.Key, input.Value)
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, property)
}

func (h *Handler) updateSpaceProperty(w http.ResponseWriter, r *http.Request, ws, actor, spaceID, propertyID string) {
	if !validPageID(w, spaceID) || !validPageID(w, propertyID) || !supportedQuery(w, r) {
		return
	}
	var input attachmentPropertyWrite
	if !decode(w, r, &input) || !validAttachmentProperty(w, input, true) {
		return
	}
	property, err := h.Commands.UpdateWikiSpaceProperty(r.Context(), ws, actor, spaceID, propertyID, input.Key, input.Value, input.Version.Number, input.Version.Message)
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, property)
}

func (h *Handler) deleteSpaceProperty(w http.ResponseWriter, r *http.Request, ws, actor, spaceID, propertyID string) {
	if !validPageID(w, spaceID) || !validPageID(w, propertyID) || !supportedQuery(w, r) {
		return
	}
	if err := h.Commands.DeleteWikiSpaceProperty(r.Context(), ws, actor, spaceID, propertyID); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(204)
}
