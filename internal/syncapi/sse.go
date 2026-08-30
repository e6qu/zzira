package syncapi

import (
	"io"
	"net/http"
	"strconv"

	"github.com/e6qu/zzira/internal/authn"
	"github.com/e6qu/zzira/internal/authz"
	"github.com/e6qu/zzira/internal/notifybus"
	"github.com/e6qu/zzira/internal/store"
)

// SSEHandler streams workspace head pokes (event: sync, data: <seq>). Any
// replica can serve any connection: the poke bus is Postgres LISTEN/NOTIFY.
type SSEHandler struct {
	Store *store.Store
	Bus   *notifybus.Bus
}

func (h *SSEHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	userID, err := authn.Identify(r.Context(), h.Store, r)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	wsSlug := r.URL.Query().Get("workspace")
	if wsSlug == "" {
		wsSlug = "zzira"
	}
	workspaceID, err := h.Store.WorkspaceBySlug(r.Context(), wsSlug)
	if err != nil {
		http.Error(w, "workspace not found", http.StatusNotFound)
		return
	}
	ok, err := authz.CanSeeWorkspace(r.Context(), h.Store, workspaceID, userID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	head, err := h.Store.Head(r.Context(), workspaceID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Send the current head immediately: the client reconciles by checkpoint.
	if _, err := io.WriteString(w, "event: sync\ndata: "+strconv.FormatInt(head, 10)+"\n\n"); err != nil {
		return
	}
	flusher.Flush()

	ch, cancel := h.Bus.Subscribe(workspaceID)
	defer cancel()

	notify := r.Context().Done()
	for {
		select {
		case <-notify:
			return
		case head, open := <-ch:
			if !open {
				return
			}
			if _, err := io.WriteString(w, "event: sync\ndata: "+strconv.FormatInt(head, 10)+"\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
