// Package syncapi serves the delta-sync read path: the ordered, permission-
// filtered action range after the caller's checkpoint. Deterministic in
// (workspace, since, head) — cacheable and differentially testable.
package syncapi

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/e6qu/zzira/internal/authn"
	"github.com/e6qu/zzira/internal/authz"
	"github.com/e6qu/zzira/internal/build"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

const defaultLimit = 500

type Handler struct {
	Store *store.Store
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	userID, err := authn.Identify(r.Context(), h.Store, r)
	if err != nil {
		w.Header().Set("WWW-Authenticate", `Basic realm="zzira"`)
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
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if since >= head {
		writeJSON(w, http.StatusNotModified, nil)
		return
	}
	actions, err := h.Store.ActionsSince(r.Context(), workspaceID, userID, since, limit)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	from := since
	to := since
	if len(actions) > 0 {
		to = actions[len(actions)-1].Seq
	}
	resp := models.SyncResponse{
		Workspace:       wsSlug,
		From:            from,
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
	_ = enc.Encode(body)
}
