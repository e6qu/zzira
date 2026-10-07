package web

import (
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/e6qu/zzira/internal/store"
)

func (h *Handler) AttachIssueForm(w http.ResponseWriter, r *http.Request, key string) {
	user := h.currentUser(r)
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	wsID, ok := h.memberWorkspace(r, user)
	if !ok {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if !parseForm(w, r) {
		return
	}
	issue, err := h.issueForUser(r, user, wsID, key)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if _, err := h.Commands.ChangeIssueForm(r.Context(), user.ID, wsID, issue.ID, "", store.IssueFormChange{
		Action: "attach", TemplateID: r.PostFormValue("template"), Name: r.PostFormValue("name"),
	}); err != nil {
		http.Error(w, err.Error(), commandErrorStatus(err))
		return
	}
	h.serveIssue(w, r, user, wsID, key)
}

func (h *Handler) UpdateIssueForm(w http.ResponseWriter, r *http.Request, key, formID, action string) {
	user := h.currentUser(r)
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	wsID, ok := h.memberWorkspace(r, user)
	if !ok {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if !parseForm(w, r) {
		return
	}
	issue, err := h.issueForUser(r, user, wsID, key)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	switch strings.ToLower(action) {
	case "submit", "reopen", "delete":
		_, err = h.Commands.ChangeIssueForm(r.Context(), user.ID, wsID, issue.ID, formID, store.IssueFormChange{Action: strings.ToLower(action)})
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.NotFound(w, r)
		} else {
			http.Error(w, err.Error(), commandErrorStatus(err))
		}
		return
	}
	h.serveIssue(w, r, user, wsID, key)
}
