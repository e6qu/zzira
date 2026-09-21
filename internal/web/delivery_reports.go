package web

import (
	"net/http"
	"strconv"
	"time"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

// Deployment frequency and cycle time each had a number on another page and
// no page of their own. These are those pages.

type deploymentFrequencyData struct {
	Project   *models.Project
	Report    store.DeploymentFrequencyReport
	Actions   reportActions
	Windows   []int
	Groupings []string
	// Max is the tallest bar, so the chart has something to scale against.
	Max  int
	Bars []deploymentBar
}

type deploymentBar struct {
	store.DeploymentPeriod
	SuccessHeight int
	FailureHeight int
}

var deploymentWindows = []int{7, 30, 90}

// DeploymentFrequencyReport shows how often a project ships, bucketed by day,
// week or month, through the mapping the project counts as production.
func (h *Handler) DeploymentFrequencyReport(w http.ResponseWriter, r *http.Request) {
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
		parsed, err := strconv.Atoi(value)
		if err != nil || (parsed != 7 && parsed != 30 && parsed != 90) {
			http.Error(w, "Choose a 7, 30, or 90 day window.", http.StatusBadRequest)
			return
		}
		days = parsed
	}
	grouping := r.URL.Query().Get("group")
	if grouping == "" {
		grouping = "day"
	}
	report, err := h.Store.DeploymentFrequencyReport(r.Context(), workspaceID, project.ID, user.ID, days, grouping, time.Now())
	if err != nil {
		http.Error(w, "Deployments are grouped by day, week or month over a 7, 30, or 90 day window.", http.StatusBadRequest)
		return
	}
	data := deploymentFrequencyData{Project: project, Report: report, Windows: deploymentWindows, Groupings: store.DeploymentFrequencyGroupings}
	for _, period := range report.Periods {
		if total := period.Successful + period.Failed; total > data.Max {
			data.Max = total
		}
	}
	if data.Max == 0 {
		data.Max = 1
	}
	for _, period := range report.Periods {
		data.Bars = append(data.Bars, deploymentBar{
			DeploymentPeriod: period,
			SuccessHeight:    period.Successful * 100 / data.Max,
			FailureHeight:    period.Failed * 100 / data.Max,
		})
	}
	if wantsCSV(r) {
		rows := make([][]string, 0, len(report.Periods))
		for _, period := range report.Periods {
			rows = append(rows, []string{period.Start, period.Label, strconv.Itoa(period.Successful), strconv.Itoa(period.Failed)})
		}
		writeReportCSV(w, project.Key+" deployment frequency", []string{"Start", "Period", "Successful", "Failed or rolled back"}, rows)
		return
	}
	actions, actionsErr := h.reportActions(r, workspaceID, user)
	if actionsErr != nil {
		http.Error(w, "Could not load report emails.", http.StatusInternalServerError)
		return
	}
	data.Actions = actions
	h.writeWorkspacePage(w, r, "page_deployment_frequency_report", user, workspaceID, data, "reports", project.Key)
}

type cycleTimeGroupView struct {
	store.CycleTimeGroup
	P50Display string
	P85Display string
	P95Display string
}

// cycleTimeSampleView is one completed work item with its cycle time already
// read as a duration, because a template cannot format one.
type cycleTimeSampleView struct {
	models.CycleSample
	Display string
}

type cycleTimeData struct {
	agileReportBoards
	Actions reportActions
	Days    int
	Windows []int
	// Comparable is what the shared board controls ask before offering the
	// previous period; cycle time percentiles are not compared that way.
	Comparable bool
	Report     store.CycleTimeReport
	Samples    []cycleTimeSampleView
	Groups     []cycleTimeGroupView
	P50        string
	P85        string
	P95        string
}

// CycleTimeReport shows how long a board's completed work took once it
// started, whole and per work type, as percentiles rather than an average --
// which is how a team answers "how long does this usually take".
func (h *Handler) CycleTimeReport(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, boards, ok := h.boardReportContext(w, r, false)
	if !ok {
		return
	}
	days, ok := reportWindow(r)
	if !ok {
		http.Error(w, "Choose a 14, 30, or 90 day window.", http.StatusBadRequest)
		return
	}
	data := cycleTimeData{agileReportBoards: boards, Days: days, Windows: flowWindows}
	if boards.Board != nil {
		report, err := h.Store.CycleTimeReport(r.Context(), boards.Board, user.ID, days, time.Now())
		if err != nil {
			http.Error(w, "Could not calculate cycle time.", http.StatusInternalServerError)
			return
		}
		data.Report = report
		data.P50, data.P85, data.P95 = cycleDuration(report.P50), cycleDuration(report.P85), cycleDuration(report.P95)
		for _, sample := range report.Samples {
			data.Samples = append(data.Samples, cycleTimeSampleView{CycleSample: sample, Display: cycleDuration(sample.CycleSeconds)})
		}
		for _, group := range report.Groups {
			data.Groups = append(data.Groups, cycleTimeGroupView{
				CycleTimeGroup: group,
				P50Display:     cycleDuration(group.P50), P85Display: cycleDuration(group.P85), P95Display: cycleDuration(group.P95),
			})
		}
	}
	if wantsCSV(r) {
		rows := make([][]string, 0, len(data.Report.Samples))
		for _, sample := range data.Report.Samples {
			rows = append(rows, []string{sample.Key, sample.IssueType, sample.Summary, sample.CompletedAt, strconv.FormatFloat(float64(sample.CycleSeconds)/3600, 'f', 2, 64)})
		}
		writeReportCSV(w, boards.Project.Key+" cycle time", []string{"Work item", "Work type", "Summary", "Completed", "Cycle time (hours)"}, rows)
		return
	}
	actions, actionsErr := h.reportActions(r, workspaceID, user)
	if actionsErr != nil {
		http.Error(w, "Could not load report emails.", http.StatusInternalServerError)
		return
	}
	data.Actions = actions
	h.writeWorkspacePage(w, r, "page_cycle_time_report", user, workspaceID, data, "reports", boards.Project.ID)
}
