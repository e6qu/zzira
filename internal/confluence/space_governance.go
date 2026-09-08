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
	level, ok := classificationLevels[space.DefaultClassificationLevel]
	if !ok {
		failure(w, 404, "Space does not have a default classification level.")
		return
	}
	respond(w, 200, level)
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
	if classificationLevels[input.ID] == nil {
		failure(w, 400, "A supported classification level is required.")
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

func (h *Handler) spaceOperationValues(r *http.Request, ws, actor, id string) ([]any, error) {
	if _, err := h.Store.WikiSpace(r.Context(), ws, actor, id); err != nil {
		return nil, err
	}
	operations := []any{map[string]string{"operation": "read", "targetType": "space"}}
	admin, err := h.Store.CanAdministerWikiSpace(r.Context(), ws, actor, id)
	if err != nil {
		return nil, err
	}
	if admin {
		operations = append(operations,
			map[string]string{"operation": "update", "targetType": "space"},
			map[string]string{"operation": "delete", "targetType": "space"},
		)
	}
	return operations, nil
}
