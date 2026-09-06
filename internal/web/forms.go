package web

import (
	"net/http"
	"strings"
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
	if _, err := h.Store.AttachIssueForm(r.Context(), wsID, issue.ID, r.PostFormValue("template"), r.PostFormValue("name")); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
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
	case "submit":
		_, err = h.Store.SetIssueFormStatus(r.Context(), wsID, issue.ID, formID, true)
	case "reopen":
		_, err = h.Store.SetIssueFormStatus(r.Context(), wsID, issue.ID, formID, false)
	case "delete":
		err = h.Store.DeleteIssueForm(r.Context(), wsID, issue.ID, formID)
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.NotFound(w, r)
		return
	}
	h.serveIssue(w, r, user, wsID, key)
}
