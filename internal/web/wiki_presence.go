package web

import (
	"encoding/json"
	"net/http"
)

// WikiPagePresence and WikiBlogPostPresence take an open page's report of who
// has it open and answer with its live state.
func (h *Handler) WikiPagePresence(w http.ResponseWriter, r *http.Request) {
	h.wikiPresence(w, r, "page", r.PathValue("page"))
}

func (h *Handler) WikiBlogPostPresence(w http.ResponseWriter, r *http.Request) {
	h.wikiPresence(w, r, "blogpost", r.PathValue("blogpost"))
}

func (h *Handler) wikiPresence(w http.ResponseWriter, r *http.Request, kind, id string) {
	user, ws, ok := h.pageContext(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	if r.PostFormValue("leave") == "true" {
		if err := h.Store.LeaveWikiContent(r.Context(), ws, user.ID, kind, id); err != nil {
			http.Error(w, "Could not update presence.", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	state, err := h.Store.WikiHeartbeat(r.Context(), ws, user.ID, kind, id, r.PostFormValue("editing") == "true")
	if err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(state)
}
