package confluence

import "net/http"

func (h *Handler) spaceDefaultClassification(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !validPageID(w, id) || !supportedQuery(w, r) {
		return
	}
	space, err := h.Store.WikiSpace(r.Context(), ws, actor, id)
	if err != nil {
		writeError(w, err)
		return
	}
	if space.DefaultClassificationLevel == "" {
		failure(w, 404, "Space does not have a default classification level.")
		return
	}
	level, err := h.Store.DataClassificationLevel(r.Context(), ws, space.DefaultClassificationLevel)
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, classificationLevelBean(level))
}

func (h *Handler) setSpaceDefaultClassification(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !validPageID(w, id) || !supportedQuery(w, r) {
		return
	}
	var input struct {
		ID string `json:"id"`
	}
	if !decode(w, r, &input) {
		return
	}
	if input.ID == "" || h.Store.PublishedDataClassificationLevel(r.Context(), ws, input.ID) != nil {
		failure(w, 400, "A published classification level is required.")
		return
	}
	if _, err := h.Commands.SetWikiSpaceDefaultClassification(r.Context(), ws, actor, id, input.ID); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(204)
}

func (h *Handler) deleteSpaceDefaultClassification(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !validPageID(w, id) || !supportedQuery(w, r) {
		return
	}
	if _, err := h.Commands.SetWikiSpaceDefaultClassification(r.Context(), ws, actor, id, ""); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(204)
}

func (h *Handler) spaceOperations(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !validPageID(w, id) || !supportedQuery(w, r) {
		return
	}
	operations, err := h.spaceOperationValues(r, ws, actor, id)
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, map[string]any{"operations": operations})
}
