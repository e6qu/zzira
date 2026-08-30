package api3

import (
	"log"
	"net/http"
	"strconv"

	"github.com/e6qu/zzira/internal/authn"
)

// NotificationsHandler serves GET /rest/zzira/1/notifications — the caller's
// synced notification list (newest first).
func (h *Handler) NotificationsHandler(w http.ResponseWriter, r *http.Request) {
	userID, err := authn.Identify(r.Context(), h.Store, r)
	if err != nil {
		w.Header().Set("WWW-Authenticate", `Basic realm="zzira"`)
		jiraError(w, http.StatusUnauthorized, "You are not authenticated.")
		return
	}
	wsID, _, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		parsed, err := strconv.Atoi(v)
		if err != nil || parsed < 1 || parsed > 200 {
			jiraError(w, http.StatusBadRequest, "limit must be an integer between 1 and 200.")
			return
		}
		limit = parsed
	}
	notifications, err := h.Store.NotificationsByUser(r.Context(), wsID, userID, limit)
	if err != nil {
		log.Printf("notifications: %v", err)
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"notifications": notifications})
}
