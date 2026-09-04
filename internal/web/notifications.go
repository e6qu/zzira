package web

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

type notificationItemView struct {
	Notification *models.Notification
	KindLabel    string
	CreatedLabel string
}

type notificationsPageData struct {
	Items       []notificationItemView
	View        string
	UnreadCount int
	Total       int
	ResultStart int
	ResultEnd   int
	Page        int
	PageCount   int
	PreviousURL string
	NextURL     string
}

const notificationsPageSize = 30

func notificationsPageURL(view string, page int) string {
	values := url.Values{}
	if view == "unread" {
		values.Set("view", "unread")
	}
	if page > 1 {
		values.Set("page", strconv.Itoa(page))
	}
	if query := values.Encode(); query != "" {
		return "/notifications?" + query
	}
	return "/notifications"
}

func notificationDestination(notification *models.Notification) string {
	if notification.EntityType == models.EntityIssue && notification.EntityID != "" {
		return "/browse/" + url.PathEscape(notification.EntityID)
	}
	return "/notifications"
}

func notificationKindLabel(kind string) string {
	switch kind {
	case "assigned":
		return "Assignment"
	case "mentioned":
		return "Mention"
	case "watched":
		return "Watched work"
	default:
		return "Update"
	}
}

func notificationTimeLabel(created string, now time.Time) string {
	createdAt, err := time.Parse(time.RFC3339, created)
	if err != nil {
		return created
	}
	age := now.Sub(createdAt)
	if age < time.Minute {
		return "Just now"
	}
	if age < time.Hour {
		return fmt.Sprintf("%dm ago", int(age.Minutes()))
	}
	if age < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(age.Hours()))
	}
	if age < 48*time.Hour {
		return "Yesterday"
	}
	if createdAt.Year() == now.Year() {
		return createdAt.Format("Jan 2")
	}
	return createdAt.Format("Jan 2, 2006")
}

// NotificationsPage renders the caller's private inbox. Paging and unread
// filtering stay inside the user-scoped store query, so no cross-user metadata
// can affect counts or empty states.
func (h *Handler) NotificationsPage(w http.ResponseWriter, r *http.Request) {
	user, wsID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	view := r.URL.Query().Get("view")
	if view != "unread" {
		view = "all"
	}
	page := 1
	if requested, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && requested > 1 {
		page = min(requested, 1_000_000)
	}
	startAt := (page - 1) * notificationsPageSize
	notifications, total, err := h.Store.NotificationsPageByUser(r.Context(), wsID, user.ID, view == "unread", startAt, notificationsPageSize)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	unread, err := h.Store.UnreadNotificationCount(r.Context(), wsID, user.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	pageCount := max(1, (total+notificationsPageSize-1)/notificationsPageSize)
	if total > 0 && page > pageCount {
		http.Redirect(w, r, notificationsPageURL(view, pageCount), http.StatusSeeOther)
		return
	}
	data := notificationsPageData{
		View: view, UnreadCount: unread, Total: total, Page: page, PageCount: pageCount,
		ResultEnd: startAt + len(notifications), Items: make([]notificationItemView, 0, len(notifications)),
	}
	now := time.Now().UTC()
	if len(notifications) > 0 {
		data.ResultStart = startAt + 1
	}
	if page > 1 {
		data.PreviousURL = notificationsPageURL(view, page-1)
	}
	if page < pageCount {
		data.NextURL = notificationsPageURL(view, page+1)
	}
	for _, notification := range notifications {
		data.Items = append(data.Items, notificationItemView{
			Notification: notification,
			KindLabel:    notificationKindLabel(notification.Kind),
			CreatedLabel: notificationTimeLabel(notification.Created, now),
		})
	}
	writePage(w, "page_notifications", pageData{User: user, Data: data, Active: "notifications"})
}

func notificationReturnURL(r *http.Request) string {
	if r.FormValue("view") == "unread" {
		return "/notifications?view=unread"
	}
	return "/notifications"
}

// SetNotificationReadPage handles the explicit read/unread action while
// preserving the inbox tab the person is currently using.
func (h *Handler) SetNotificationReadPage(w http.ResponseWriter, r *http.Request, notificationID string) {
	user, wsID, ok := h.pageContext(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	readValue := r.PostFormValue("read")
	if readValue != "true" && readValue != "false" {
		http.Error(w, "read must be true or false", http.StatusBadRequest)
		return
	}
	if _, _, err := h.Store.SetNotificationRead(r.Context(), wsID, user.ID, notificationID, readValue == "true"); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, notificationReturnURL(r), http.StatusSeeOther)
}

// OpenNotification marks a private notification read before taking the user
// to its destination. Visibility is checked again by the destination page.
func (h *Handler) OpenNotification(w http.ResponseWriter, r *http.Request, notificationID string) {
	user, wsID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	notification, _, err := h.Store.SetNotificationRead(r.Context(), wsID, user.ID, notificationID, true)
	if errors.Is(err, pgx.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// notificationDestination always returns a same-origin fixed route and
	// escapes the only stored path segment before it reaches Location.
	w.Header().Set("Location", notificationDestination(notification))
	w.WriteHeader(http.StatusSeeOther)
}

func (h *Handler) MarkAllNotificationsReadPage(w http.ResponseWriter, r *http.Request) {
	user, wsID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	if _, err := h.Store.MarkAllNotificationsRead(r.Context(), wsID, user.ID); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/notifications", http.StatusSeeOther)
}
