package api3

import (
	"net/http"
	"strings"
)

// ---- link types ----

// ---- issue links ----

// ---- labels ----

func (h *Handler) labelsEndpoint(w http.ResponseWriter, r *http.Request) {
	wsID, userID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	query := r.URL.Query().Get("query")
	total, labels, err := h.Store.Labels(r.Context(), wsID, userID, query)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"totalCount": total, "labels": labels})
}

// ---- metadata registries ----

func (h *Handler) statusesEndpoint(w http.ResponseWriter, r *http.Request) {
	workspaceID, _, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	statuses, err := h.Store.StatusesForWorkspace(r.Context(), workspaceID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	out := make([]map[string]any, 0, len(statuses))
	for _, st := range statuses {
		out = append(out, h.statusBean(st))
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) statusCategoryEndpoint(w http.ResponseWriter, r *http.Request) {
	if _, _, e := h.authWorkspace(r); e != nil {
		writeJerr(w, e)
		return
	}
	writeJSON(w, http.StatusOK, []map[string]any{statusCategoryBean("new"), statusCategoryBean("indeterminate"), statusCategoryBean("done")})
}

func (h *Handler) statusCategoryDetailEndpoint(w http.ResponseWriter, r *http.Request, idOrKey string) {
	if _, _, e := h.authWorkspace(r); e != nil {
		writeJerr(w, e)
		return
	}
	category := map[string]string{"2": "new", "new": "new", "4": "indeterminate", "indeterminate": "indeterminate", "3": "done", "done": "done"}[strings.ToLower(idOrKey)]
	if category == "" {
		jiraError(w, http.StatusNotFound, "The status category does not exist.")
		return
	}
	writeJSON(w, http.StatusOK, statusCategoryBean(category))
}
