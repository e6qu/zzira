package web

import (
	"net/http"
)

// DashboardPage serves GET /dashboard — status counts, my open issues,
// recent activity (V6 dashboards, minimal).
func (h *Handler) DashboardPage(w http.ResponseWriter, r *http.Request) {
	user := h.currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	wsID, ok := h.memberWorkspace(r, user)
	if !ok {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	stats, err := h.Store.DashboardData(r.Context(), wsID, user.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writePage(w, "page_dashboard", pageData{User: user, Data: stats})
}
