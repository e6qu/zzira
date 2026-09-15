package web

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/e6qu/zzira/internal/store"
)

type wikiLiveRequest struct {
	Session  string                 `json:"session"`
	Revision int64                  `json:"revision"`
	Changes  []store.WikiLiveChange `json:"changes"`
}

type wikiLiveResponse struct {
	store.WikiLiveDocument
	// Applied says the editor's changes landed, so the changes returned are
	// its own; otherwise they are other editors' changes to apply.
	Applied bool `json:"applied"`
}

// WikiPageLive exchanges an editor's changes with the page's live document.
func (h *Handler) WikiPageLive(w http.ResponseWriter, r *http.Request) {
	user, ws, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	var request wikiLiveRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&request); err != nil {
		http.Error(w, "Invalid live changes.", http.StatusBadRequest)
		return
	}
	page, err := h.Store.WikiPage(r.Context(), ws, user.ID, r.PathValue("page"))
	if err != nil || page.SpaceID != r.PathValue("space") {
		http.NotFound(w, r)
		return
	}
	response := wikiLiveResponse{}
	if len(request.Changes) == 0 {
		response.WikiLiveDocument, err = h.Store.WikiLiveDocument(r.Context(), ws, user.ID, page.ID, request.Session, request.Revision)
	} else {
		response.WikiLiveDocument, err = h.Store.ApplyWikiLiveChanges(r.Context(), ws, user.ID, page.ID, request.Session, request.Revision, request.Changes)
		if errors.Is(err, store.ErrWikiLiveStale) {
			err = nil
		} else if err == nil {
			response.Applied = true
		}
	}
	if err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(response)
}
