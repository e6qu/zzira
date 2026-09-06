package web

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/e6qu/zzira/internal/store"
)

type productActivityRequest struct {
	ProductKey string `json:"productKey"`
}

// RecordProductActivity accepts the browser's signal only after the page has
// remained visible for two seconds. The durable timestamp then backs the
// organization administration last-active API.
func (h *Handler) RecordProductActivity(w http.ResponseWriter, r *http.Request) {
	user := h.currentUser(r)
	if user == nil {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	workspaceID, ok := h.memberWorkspace(r, user)
	if !ok {
		http.Error(w, "workspace access required", http.StatusForbidden)
		return
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var input productActivityRequest
	if err := decoder.Decode(&input); err != nil {
		http.Error(w, "invalid activity body", http.StatusBadRequest)
		return
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		http.Error(w, "activity body must contain one JSON value", http.StatusBadRequest)
		return
	}
	if input.ProductKey != "jira-software" && input.ProductKey != "jira-service-management" && input.ProductKey != "confluence" {
		http.Error(w, "unsupported product key", http.StatusBadRequest)
		return
	}
	if err := h.Store.RecordProductUserActivity(r.Context(), workspaceID, user.ID, input.ProductKey); err != nil {
		if errors.Is(err, store.ErrAdminNotFound) {
			http.Error(w, "product access required", http.StatusForbidden)
			return
		}
		http.Error(w, "could not record product activity", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
