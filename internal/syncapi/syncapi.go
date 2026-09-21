// Package syncapi serves the delta-sync read path: the ordered, permission-
// filtered action range after the caller's checkpoint. Deterministic in
// (workspace, since, head) — cacheable and differentially testable.
package syncapi

import (
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"strconv"
	"syscall"
	"time"

	"github.com/e6qu/zzira/internal/authn"
	"github.com/e6qu/zzira/internal/authz"
	"github.com/e6qu/zzira/internal/build"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

const defaultLimit = 500

type Handler struct {
	Store         *store.Store
	WorkspaceSlug string
}

// selectedWorkspaceSlug chooses the configured serving workspace when set.
// The unconfigured form is retained for the load-test harness, which measures
// several independently seeded workspaces through one in-process mux.
func selectedWorkspaceSlug(configured string, r *http.Request) (string, bool) {
	requested := r.URL.Query().Get("workspace")
	if configured != "" {
		return configured, requested == "" || requested == configured
	}
	if requested == "" {
		requested = "zzira"
	}
	return requested, true
}

// slowSync is how long a delta-sync read may take before it is worth knowing
// about. A replica's own client gives up after five seconds and tells its
// person the site is offline, so anything approaching that is a fault the
// server should be able to account for afterwards.
const slowSync = 2 * time.Second

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Only what this server itself worked out goes into the line: a request
	// carries whatever text its sender chose, and a log is not the place to
	// find out.
	started := time.Now()
	var reported struct{ since, limit, delivered int64 }
	defer func() {
		if elapsed := time.Since(started); elapsed > slowSync {
			log.Printf("syncapi: slow read: %s since=%d limit=%d delivered=%d",
				elapsed.Round(time.Millisecond), reported.since, reported.limit, reported.delivered)
		}
	}()
	userID, err := authn.Identify(r.Context(), h.Store, r)
	if err != nil {
		// A Basic challenge can suspend fetch() behind browser credential UI.
		// Replica workers need the bare 401 so they can purge local state;
		// ordinary API clients retain the Jira-compatible challenge.
		if r.Header.Get("X-Zzira-Replica") != "browser" {
			w.Header().Set("WWW-Authenticate", `Basic realm="zzira"`)
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	wsSlug, ok := selectedWorkspaceSlug(h.WorkspaceSlug, r)
	if !ok {
		http.Error(w, "workspace not found", http.StatusNotFound)
		return
	}
	workspaceID, err := h.Store.WorkspaceBySlug(r.Context(), wsSlug)
	if err != nil {
		http.Error(w, "workspace not found", http.StatusNotFound)
		return
	}
	member, err := authz.CanSeeWorkspace(r.Context(), h.Store, workspaceID, userID)
	if err != nil {
		log.Printf("%s: %v", "syncapi.go", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !member {
		http.Error(w, `{"errorMessages":["You do not have permission to view this workspace."]}`, http.StatusForbidden)
		return
	}

	since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	limit := int64(defaultLimit)
	if v, err := strconv.ParseInt(r.URL.Query().Get("limit"), 10, 64); err == nil && v > 0 && v <= 1000 {
		limit = v
	}

	head, err := h.Store.Head(r.Context(), workspaceID)
	if err != nil {
		log.Printf("%s: %v", "syncapi.go", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if since >= head {
		writeJSON(w, http.StatusNotModified, nil)
		return
	}
	reported.since, reported.limit = since, limit
	actions, to, err := h.Store.ActionPageSince(r.Context(), workspaceID, userID, since, limit)
	if err != nil {
		log.Printf("%s: %v", "syncapi.go", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// Actions may commit between the first head read and the page query. Refresh
	// the head so the response invariant from <= to <= head always holds.
	head, err = h.Store.Head(r.Context(), workspaceID)
	if err != nil {
		log.Printf("%s: %v", "syncapi.go", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	reported.delivered = int64(len(actions))
	resp := models.SyncResponse{
		Workspace:       wsSlug,
		From:            since,
		To:              to,
		Head:            head,
		RendererVersion: build.Renderer,
		Actions:         actions,
		Truncated:       to < head,
	}
	w.Header().Set("ETag", `"`+"w"+wsSlug+"-"+strconv.FormatInt(head, 10)+`"`)
	writeJSON(w, http.StatusOK, resp)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	if body == nil {
		w.WriteHeader(status)
		return
	}
	enc := json.NewEncoder(w)
	w.WriteHeader(status)
	if err := enc.Encode(body); err != nil && !clientGone(err) {
		log.Printf("syncapi: encode response: %v", err)
	}
}

// clientGone is a response nobody is listening to any more: a browser that
// navigated away mid-page, or a replica that gave up waiting. It is the
// ordinary end of a request, not a fault of this server's, and logging it as
// one buries the faults that are.
func clientGone(err error) bool {
	return errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, net.ErrClosed)
}
