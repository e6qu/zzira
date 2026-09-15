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
	// Cursor is where the editor's caret is in the text it sent.
	Cursor *store.WikiLiveSelection `json:"cursor"`
}

type wikiLiveResponse struct {
	store.WikiLiveDocument
	// Applied says the editor's changes landed, so the changes returned are
	// its own; otherwise they are other editors' changes to apply.
	Applied bool `json:"applied"`
}

// WikiPageLive exchanges an editor's changes with a page's live document.
func (h *Handler) WikiPageLive(w http.ResponseWriter, r *http.Request) {
	h.wikiLive(w, r, "page", r.PathValue("page"))
}

// WikiBlogPostLive exchanges an editor's changes with a blog post's live
// document.
func (h *Handler) WikiBlogPostLive(w http.ResponseWriter, r *http.Request) {
	h.wikiLive(w, r, "blogpost", r.PathValue("blogpost"))
}

func (h *Handler) wikiLive(w http.ResponseWriter, r *http.Request, kind, id string) {
	user, ws, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	var request wikiLiveRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&request); err != nil {
		http.Error(w, "Invalid live changes.", http.StatusBadRequest)
		return
	}
	spaceID := ""
	switch kind {
	case "page":
		page, err := h.Store.WikiPage(r.Context(), ws, user.ID, id)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		id, spaceID = page.ID, page.SpaceID
	default:
		post, err := h.Store.WikiBlogPost(r.Context(), ws, user.ID, id)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		id, spaceID = post.ID, post.SpaceID
	}
	if spaceID != r.PathValue("space") {
		http.NotFound(w, r)
		return
	}
	var err error
	response := wikiLiveResponse{}
	if len(request.Changes) == 0 {
		response.WikiLiveDocument, err = h.Store.WikiLiveDocument(r.Context(), ws, user.ID, kind, id, request.Session, request.Revision, request.Cursor)
	} else {
		response.WikiLiveDocument, err = h.Store.ApplyWikiLiveChanges(r.Context(), ws, user.ID, kind, id, request.Session, request.Revision, request.Changes, request.Cursor)
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
