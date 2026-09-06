package web

import (
	"net/http"
	"strconv"
	"time"

	"github.com/e6qu/zzira/internal/models"
)

type doraReportData struct {
	Project *models.Project
	Report  models.DORAReport
}

// DORAReport renders the permission-filtered project delivery metrics journey.
func (h *Handler) DORAReport(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	project, err := h.Store.ProjectByIDOrKey(r.Context(), workspaceID, r.PathValue("key"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	days := 30
	if value := r.URL.Query().Get("window"); value != "" {
		parsed, parseErr := strconv.Atoi(value)
		if parseErr != nil || (parsed != 7 && parsed != 30 && parsed != 90) {
			http.Error(w, "Choose a 7, 30, or 90 day window.", http.StatusBadRequest)
			return
		}
		days = parsed
	}
	report, err := h.Store.DORAReport(r.Context(), workspaceID, project.ID, user.ID, days, time.Now())
	if err != nil {
		http.Error(w, "Could not calculate delivery metrics.", http.StatusInternalServerError)
		return
	}
	h.writeWorkspacePage(w, r, "page_dora_report", user, workspaceID, doraReportData{Project: project, Report: report}, "reports", project.Key)
}
