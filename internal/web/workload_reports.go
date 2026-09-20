package web

import (
	"net/http"
	"strconv"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

// workloadRowView is one row of a workload report with its time already
// written the way the site writes time.
type workloadRowView struct {
	models.WorkloadRow
	Remaining string
}

type workloadReportData struct {
	Project   *models.Project
	Kind      string
	Title     string
	Rows      []workloadRowView
	Types     []workloadRowView
	Issues    int
	Remaining string
	Estimated int
	// Choices and Selected are the versions a version report offers, empty
	// for the report that covers the whole project.
	Choices  []progressChoice
	Selected string
	Empty    string
	Actions  reportActions
}

type timeTrackingRowView struct {
	models.TimeTrackingRow
	Original, Remaining, Spent, Accuracy string
	Over                                 bool
}

type timeTrackingReportData struct {
	Project                              *models.Project
	Rows                                 []timeTrackingRowView
	Original, Remaining, Spent, Accuracy string
	Over                                 bool
	Choices                              []progressChoice
	Selected                             string
	Actions                              reportActions
}

type groupByReportData struct {
	Project *models.Project
	Field   string
	Fields  []progressChoice
	Report  models.GroupByReport
	Widest  int
	Actions reportActions
}

// groupByFieldNames are the words the page uses for the fields it groups by.
var groupByFieldNames = map[string]string{
	"assignee": "Assignee", "issuetype": "Work type", "status": "Status",
	"priority": "Priority", "resolution": "Resolution", "reporter": "Reporter",
}

// UserWorkloadReport shows who holds the project's unresolved work.
func (h *Handler) UserWorkloadReport(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	project, ok := h.reportProject(w, r, workspaceID)
	if !ok {
		return
	}
	report, err := h.Store.UserWorkload(r.Context(), workspaceID, user.ID, project.ID)
	if err != nil {
		http.Error(w, "Could not read the project's workload.", http.StatusInternalServerError)
		return
	}
	data := workloadReportData{
		Project: project, Kind: "user-workload", Title: "User workload",
		Issues: report.Issues, Estimated: report.Estimated,
		Empty: "Nobody is holding unresolved work in this project.",
	}
	tracking := h.timeTracking(r, workspaceID)
	data.Remaining = models.FormatJiraDuration(report.RemainingSeconds, tracking)
	for _, row := range report.Rows {
		data.Rows = append(data.Rows, workloadRowView{WorkloadRow: row, Remaining: models.FormatJiraDuration(row.RemainingSeconds, tracking)})
	}
	if wantsCSV(r) {
		rows := make([][]string, 0, len(data.Rows))
		for _, row := range data.Rows {
			rows = append(rows, []string{row.Name, strconv.Itoa(row.Issues), row.Remaining})
		}
		writeReportCSV(w, project.Key+" user workload", []string{"Assignee", "Unresolved work items", "Remaining estimate"}, rows)
		return
	}
	actions, err := h.reportActions(r, workspaceID, user)
	if err != nil {
		http.Error(w, "Could not load report emails.", http.StatusInternalServerError)
		return
	}
	data.Actions = actions
	h.writeWorkspacePage(w, r, "page_workload_report", user, workspaceID, data, "reports", project.ID)
}

// VersionWorkloadReport shows who holds one version's unresolved work, and
// what kind of work it is.
func (h *Handler) VersionWorkloadReport(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	project, ok := h.reportProject(w, r, workspaceID)
	if !ok {
		return
	}
	version, choices, ok := h.reportVersion(w, r, project)
	if !ok {
		return
	}
	data := workloadReportData{
		Project: project, Kind: "version-workload", Title: "Version workload",
		Choices: choices, Empty: "Create a version to see the work left in it.",
	}
	tracking := h.timeTracking(r, workspaceID)
	if version != nil {
		data.Selected = version.ID
		report, err := h.Store.VersionWorkload(r.Context(), workspaceID, user.ID, project.ID, version.ID)
		if err != nil {
			http.Error(w, "Could not read the version's workload.", http.StatusInternalServerError)
			return
		}
		data.Issues, data.Estimated = report.Issues, report.Estimated
		data.Remaining = models.FormatJiraDuration(report.RemainingSeconds, tracking)
		for _, row := range report.Rows {
			data.Rows = append(data.Rows, workloadRowView{WorkloadRow: row, Remaining: models.FormatJiraDuration(row.RemainingSeconds, tracking)})
		}
		for _, row := range report.Types {
			data.Types = append(data.Types, workloadRowView{WorkloadRow: row, Remaining: models.FormatJiraDuration(row.RemainingSeconds, tracking)})
		}
	}
	if wantsCSV(r) {
		rows := make([][]string, 0, len(data.Rows)+len(data.Types))
		for _, row := range data.Rows {
			rows = append(rows, []string{"Assignee", row.Name, strconv.Itoa(row.Issues), row.Remaining})
		}
		for _, row := range data.Types {
			rows = append(rows, []string{"Work type", row.Name, strconv.Itoa(row.Issues), row.Remaining})
		}
		writeReportCSV(w, project.Key+" version workload "+data.Selected, []string{"Grouped by", "Group", "Unresolved work items", "Remaining estimate"}, rows)
		return
	}
	actions, err := h.reportActions(r, workspaceID, user)
	if err != nil {
		http.Error(w, "Could not load report emails.", http.StatusInternalServerError)
		return
	}
	data.Actions = actions
	h.writeWorkspacePage(w, r, "page_workload_report", user, workspaceID, data, "reports", project.ID)
}

// TimeTrackingReport compares what the unresolved work was estimated to take
// with what it has cost so far.
func (h *Handler) TimeTrackingReport(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	project, ok := h.reportProject(w, r, workspaceID)
	if !ok {
		return
	}
	version, choices, ok := h.reportVersion(w, r, project)
	if !ok {
		return
	}
	// The whole project is the report's default, and a version narrows it.
	selected := ""
	if r.URL.Query().Get("version") != "" && version != nil {
		selected = version.ID
	}
	report, err := h.Store.TimeTracking(r.Context(), workspaceID, user.ID, project.ID, selected)
	if err != nil {
		http.Error(w, "Could not read the project's time tracking.", http.StatusInternalServerError)
		return
	}
	tracking := h.timeTracking(r, workspaceID)
	data := timeTrackingReportData{
		Project: project, Choices: choices, Selected: selected,
		Original:  models.FormatJiraDuration(report.OriginalSeconds, tracking),
		Remaining: models.FormatJiraDuration(report.RemainingSeconds, tracking),
		Spent:     models.FormatJiraDuration(report.SpentSeconds, tracking),
		Accuracy:  models.FormatJiraDuration(absSeconds(report.AccuracySeconds), tracking),
		Over:      report.AccuracySeconds < 0,
	}
	for _, row := range report.Rows {
		data.Rows = append(data.Rows, timeTrackingRowView{
			TimeTrackingRow: row,
			Original:        models.FormatJiraDuration(row.OriginalSeconds, tracking),
			Remaining:       models.FormatJiraDuration(row.RemainingSeconds, tracking),
			Spent:           models.FormatJiraDuration(row.SpentSeconds, tracking),
			Accuracy:        models.FormatJiraDuration(absSeconds(row.AccuracySeconds), tracking),
			Over:            row.AccuracySeconds < 0,
		})
	}
	if wantsCSV(r) {
		rows := make([][]string, 0, len(data.Rows))
		for _, row := range data.Rows {
			accuracy := row.Accuracy
			if row.Over {
				accuracy = "-" + accuracy
			}
			rows = append(rows, []string{row.Key, row.Summary, row.Original, row.Remaining, row.Spent, accuracy})
		}
		writeReportCSV(w, project.Key+" time tracking "+selected,
			[]string{"Key", "Summary", "Original estimate", "Remaining estimate", "Time spent", "Accuracy"}, rows)
		return
	}
	actions, err := h.reportActions(r, workspaceID, user)
	if err != nil {
		http.Error(w, "Could not load report emails.", http.StatusInternalServerError)
		return
	}
	data.Actions = actions
	h.writeWorkspacePage(w, r, "page_time_tracking_report", user, workspaceID, data, "reports", project.ID)
}

// SingleLevelGroupByReport counts a project's work by one field.
func (h *Handler) SingleLevelGroupByReport(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	project, ok := h.reportProject(w, r, workspaceID)
	if !ok {
		return
	}
	field := r.URL.Query().Get("field")
	if field == "" {
		field = store.GroupByFields[0]
	}
	if _, known := groupByFieldNames[field]; !known {
		http.Error(w, "Choose a field this report groups by.", http.StatusBadRequest)
		return
	}
	report, err := h.Store.GroupBy(r.Context(), workspaceID, user.ID, project.ID, field)
	if err != nil {
		http.Error(w, "Could not count the project's work.", http.StatusInternalServerError)
		return
	}
	data := groupByReportData{Project: project, Field: field, Report: report}
	for _, offered := range store.GroupByFields {
		data.Fields = append(data.Fields, progressChoice{Value: offered, Name: groupByFieldNames[offered]})
	}
	for _, row := range report.Rows {
		if row.Issues > data.Widest {
			data.Widest = row.Issues
		}
	}
	if wantsCSV(r) {
		rows := make([][]string, 0, len(report.Rows))
		for _, row := range report.Rows {
			rows = append(rows, []string{row.Name, strconv.Itoa(row.Issues), strconv.Itoa(row.Percent) + "%"})
		}
		writeReportCSV(w, project.Key+" work by "+field, []string{groupByFieldNames[field], "Work items", "Share"}, rows)
		return
	}
	actions, err := h.reportActions(r, workspaceID, user)
	if err != nil {
		http.Error(w, "Could not load report emails.", http.StatusInternalServerError)
		return
	}
	data.Actions = actions
	h.writeWorkspacePage(w, r, "page_group_by_report", user, workspaceID, data, "reports", project.ID)
}

// reportVersion picks the version a report is asked for, defaulting to the
// first unarchived one, and lists the versions the page offers.
func (h *Handler) reportVersion(w http.ResponseWriter, r *http.Request, project *models.Project) (*models.Version, []progressChoice, bool) {
	versions, err := h.Store.ProjectVersions(r.Context(), project.ID)
	if err != nil {
		http.Error(w, "Could not load versions.", http.StatusInternalServerError)
		return nil, nil, false
	}
	wanted := r.URL.Query().Get("version")
	choices := []progressChoice{}
	var selected *models.Version
	for _, candidate := range versions {
		if candidate.Archived {
			continue
		}
		choices = append(choices, progressChoice{Value: candidate.ID, Name: candidate.Name + " (" + candidate.State() + ")"})
		if selected == nil && (wanted == "" || candidate.ID == wanted) {
			selected = candidate
		}
	}
	if wanted != "" && selected == nil {
		http.NotFound(w, r)
		return nil, nil, false
	}
	return selected, choices, true
}

// timeTracking is the site's own working day and week, which is how every
// estimate on these pages is written.
func (h *Handler) timeTracking(r *http.Request, workspaceID string) models.TimeTrackingConfiguration {
	configuration, err := h.Store.JiraSiteConfiguration(r.Context(), workspaceID)
	if err != nil {
		return models.TimeTrackingConfiguration{DefaultUnit: "minute", WorkingHoursPerDay: 8, WorkingDaysPerWeek: 5}
	}
	return configuration.TimeTracking
}

func absSeconds(seconds int64) int64 {
	if seconds < 0 {
		return -seconds
	}
	return seconds
}
