package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/e6qu/zzira/internal/models"
)

type doraReportData struct {
	Project    *models.Project
	Report     models.DORAReport
	Actions    reportActions
	Compare    bool
	Comparison map[string]string
}

type appReportView struct {
	Module      models.AppModule
	Description string
	Category    string
	Thumbnail   string
}

type projectReportsData struct {
	Project *models.Project
	Reports []appReportView
}

type projectAppReportData struct {
	Project  *models.Project
	Report   appReportView
	FrameURL string
}

func (h *Handler) ProjectReports(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	project, ok := h.reportProject(w, r, workspaceID)
	if !ok {
		return
	}
	modules, err := h.Store.AppModulesByLocation(r.Context(), workspaceID, "jira.report")
	if err != nil {
		http.Error(w, "Could not load reports.", http.StatusInternalServerError)
		return
	}
	reports := make([]appReportView, 0, len(modules))
	for _, module := range modules {
		reports = append(reports, decodeAppReport(module))
	}
	h.writeWorkspacePage(w, r, "page_project_reports", user, workspaceID, projectReportsData{Project: project, Reports: reports}, "reports", project.ID)
}

func (h *Handler) ProjectAppReport(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	project, ok := h.reportProject(w, r, workspaceID)
	if !ok {
		return
	}
	module, err := h.Store.ActiveAppModule(r.Context(), workspaceID, r.PathValue("module"))
	if err != nil || module.Location != "jira.report" {
		http.NotFound(w, r)
		return
	}
	values := url.Values{"project.key": {project.Key}, "project.id": {project.ID}}
	h.writeWorkspacePage(w, r, "page_project_app_report", user, workspaceID, projectAppReportData{Project: project, Report: decodeAppReport(*module), FrameURL: remoteModuleFramePath(module, values)}, "reports", project.ID)
}

func decodeAppReport(module models.AppModule) appReportView {
	var meta struct {
		Description string `json:"description"`
		Category    string `json:"category"`
		Thumbnail   string `json:"thumbnailUrl"`
	}
	_ = json.Unmarshal([]byte(module.Body), &meta)
	thumbnail := ""
	if meta.Thumbnail != "" {
		thumbnail = appModuleThumbnailPath(module)
	}
	return appReportView{Module: module, Description: meta.Description, Category: meta.Category, Thumbnail: thumbnail}
}

// DORAReport renders the permission-filtered project delivery metrics journey.
func (h *Handler) DORAReport(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	project, ok := h.reportProject(w, r, workspaceID)
	if !ok {
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
	now := time.Now().UTC()
	report, err := h.Store.DORAReport(r.Context(), workspaceID, project.ID, user.ID, days, now)
	if err != nil {
		http.Error(w, "Could not calculate delivery metrics.", http.StatusInternalServerError)
		return
	}
	data := doraReportData{Project: project, Report: report, Compare: wantsComparison(r)}
	var previous models.DORAReport
	if data.Compare {
		if previous, err = h.Store.DORAReport(r.Context(), workspaceID, project.ID, user.ID, days, previousPeriod(now, days)); err != nil {
			http.Error(w, "Could not calculate delivery metrics.", http.StatusInternalServerError)
			return
		}
		data.Comparison = map[string]string{
			"deployments": compareCount(report.DeploymentFrequency, previous.DeploymentFrequency, days),
			"leadTime":    compareDuration(report.LeadTimeSeconds, previous.LeadTimeSeconds, days),
			"failureRate": compareRate(report.ChangeFailureRate, previous.ChangeFailureRate, previous.TotalChanges, days, "%"),
			"restore":     compareDuration(report.MTTRSeconds, previous.MTTRSeconds, days),
		}
	}
	if wantsCSV(r) {
		header, rows := []string{"Date", "Successful deployments", "Failed or rolled back"}, doraCSVRows(report)
		if data.Compare {
			header, rows = withPeriods(header, doraCSVRows(previous), rows)
		}
		writeReportCSV(w, project.Key+" DORA metrics", header, rows)
		return
	}
	actions, actionsErr := h.reportActions(r, workspaceID, user)
	if actionsErr != nil {
		http.Error(w, "Could not load report emails.", http.StatusInternalServerError)
		return
	}
	data.Actions = actions
	h.writeWorkspacePage(w, r, "page_dora_report", user, workspaceID, data, "reports", project.Key)
}

func doraCSVRows(report models.DORAReport) [][]string {
	rows := make([][]string, 0, len(report.Daily))
	for _, day := range report.Daily {
		rows = append(rows, []string{day.Date, strconv.Itoa(day.Deployments), strconv.Itoa(day.Failures)})
	}
	return rows
}
