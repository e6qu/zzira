package api3

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"
)

// NotificationsHandler serves GET /rest/zzira/1/notifications — the caller's
// synced notification list (newest first).
func (h *Handler) NotificationsHandler(w http.ResponseWriter, r *http.Request) {
	wsID, userID, e := h.authWorkspace(r)
	if e != nil {
		if e.status == http.StatusUnauthorized {
			w.Header().Set("WWW-Authenticate", `Basic realm="zzira"`)
		}
		writeJerr(w, e)
		return
	}
	maxResults := 50
	pageSize := r.URL.Query().Get("maxResults")
	if pageSize == "" {
		pageSize = r.URL.Query().Get("limit") // backwards-compatible custom alias
	}
	if pageSize != "" {
		parsed, err := strconv.Atoi(pageSize)
		if err != nil || parsed < 1 || parsed > 200 {
			jiraError(w, http.StatusBadRequest, "maxResults must be an integer between 1 and 200.")
			return
		}
		maxResults = parsed
	}
	startAt := 0
	if value := r.URL.Query().Get("startAt"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 0 || parsed > 10_000_000 {
			jiraError(w, http.StatusBadRequest, "startAt must be an integer between 0 and 10000000.")
			return
		}
		startAt = parsed
	}
	unreadOnly := false
	if value := r.URL.Query().Get("unreadOnly"); value != "" {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			jiraError(w, http.StatusBadRequest, "unreadOnly must be true or false.")
			return
		}
		unreadOnly = parsed
	}
	notifications, total, err := h.Store.NotificationsPageByUser(r.Context(), wsID, userID, unreadOnly, startAt, maxResults)
	if err != nil {
		log.Printf("notifications: %v", err)
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	unread, err := h.Store.UnreadNotificationCount(r.Context(), wsID, userID)
	if err != nil {
		log.Printf("notification count: %v", err)
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"notifications": notifications, "unreadCount": unread, "startAt": startAt,
		"maxResults": maxResults, "total": total, "isLast": startAt+len(notifications) >= total,
	})
}

// NotificationHandler serves PUT /rest/zzira/1/notifications/{id}. The
// notification lookup is scoped to the authenticated user to avoid leaking
// another member's private activity stream.
func (h *Handler) NotificationHandler(w http.ResponseWriter, r *http.Request, notificationID string) {
	wsID, userID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	var request struct {
		Read *bool `json:"read"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || request.Read == nil {
		jiraError(w, http.StatusBadRequest, "The request body must contain a boolean read value.")
		return
	}
	notification, _, err := h.Store.SetNotificationRead(r.Context(), wsID, userID, notificationID, *request.Read)
	if errors.Is(err, pgx.ErrNoRows) {
		jiraError(w, http.StatusNotFound, "Notification does not exist.")
		return
	}
	if err != nil {
		log.Printf("notification update: %v", err)
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, notification)
}

// MarkAllNotificationsReadHandler serves
// POST /rest/zzira/1/notifications/read-all.
func (h *Handler) MarkAllNotificationsReadHandler(w http.ResponseWriter, r *http.Request) {
	wsID, userID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	updated, err := h.Store.MarkAllNotificationsRead(r.Context(), wsID, userID)
	if err != nil {
		log.Printf("mark all notifications read: %v", err)
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"updated": updated, "unreadCount": 0})
}
