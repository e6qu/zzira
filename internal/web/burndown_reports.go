package web

import (
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/e6qu/zzira/internal/models"
)

// burndownBar is one sprint drawn in a burndown: what was finished, what
// arrived, and where the remaining work stood when the sprint ended.
type burndownBar struct {
	Name                        string
	Added, Completed, Remaining float64
	AddedText, CompletedText    string
	RemainingText               string
	CompletedY, CompletedHeight float64
	AddedY, AddedHeight         float64
	X, PointX, PointY           float64
}

// burndownView draws a burndown: a bar per sprint and the line the
// remaining work follows through them.
type burndownView struct {
	models.BurndownReport
	Width, Height, Left, Right, Bottom, TickX, BarWidth float64
	Ticks                                               []chartTick
	Bars                                                []burndownBar
	Line                                                string
	RemainingText                                       string
}

type burndownReportData struct {
	agileReportBoards
	Actions                       reportActions
	Kind, Title, Parameter, Empty string
	Choices                       []progressChoice
	// ChoiceLabel names what the chooser picks. A site that plans above the
	// epic says so, because the list holds those too.
	ChoiceLabel string
	Selected    string
	View        *burndownView
}

// EpicBurndownReport charts an epic's work burning down sprint by sprint.
func (h *Handler) EpicBurndownReport(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, boards, ok := h.boardReportContext(w, r, false)
	if !ok {
		return
	}
	data := burndownReportData{agileReportBoards: boards, Kind: "epic-burndown", Title: "Epic burndown", Parameter: "epic",
		Empty: "Create an epic to watch its work burn down."}
	// Everything work is planned under, epics and whatever a site put above
	// them, so a burndown can follow an initiative as well as an epic.
	epics, err := h.Store.ParentWorkInProjects(r.Context(), workspaceID, user.ID, []string{boards.Project.ID})
	if err != nil {
		http.Error(w, "Could not load epics.", http.StatusInternalServerError)
		return
	}
	wanted := r.URL.Query().Get("epic")
	var epic *models.Issue
	for _, candidate := range epics {
		name := candidate.Key + " " + candidate.Summary
		if candidate.IssueType.Name != "" {
			name = candidate.IssueType.Name + " · " + name
		}
		data.Choices = append(data.Choices, progressChoice{Value: candidate.Key, Name: name})
		if epic == nil && (wanted == "" || strings.EqualFold(candidate.Key, wanted)) {
			epic = candidate
		}
	}
	// The chooser holds whatever a project plans work under, so it says what
	// that is: a site with a level above the epic picks from both.
	data.ChoiceLabel = "Epic"
	for _, candidate := range epics {
		if candidate.IssueType.HierarchyLevel > 1 {
			data.ChoiceLabel = "Epic or the level above it"
			break
		}
	}
	if wanted != "" && epic == nil {
		http.NotFound(w, r)
		return
	}
	if boards.Board != nil && epic != nil {
		data.Selected = epic.Key
		children, err := h.Store.WorkBeneath(r.Context(), workspaceID, user.ID, []string{epic.ID})
		if err != nil {
			http.Error(w, "Could not load the work in the epic.", http.StatusInternalServerError)
			return
		}
		report, err := h.Store.BurndownReport(r.Context(), workspaceID, boards.Board, children, time.Now())
		if err != nil {
			http.Error(w, "Could not calculate the epic burndown.", http.StatusInternalServerError)
			return
		}
		data.View = newBurndownView(report)
	}
	h.writeBurndown(w, r, user, workspaceID, data)
}

// ReleaseBurndownReport charts a version's work burning down sprint by
// sprint.
func (h *Handler) ReleaseBurndownReport(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, boards, ok := h.boardReportContext(w, r, false)
	if !ok {
		return
	}
	data := burndownReportData{agileReportBoards: boards, Kind: "release-burndown", Title: "Release burndown", Parameter: "version",
		Empty: "Create a version to watch its work burn down."}
	versions, err := h.Store.ProjectVersions(r.Context(), boards.Project.ID)
	if err != nil {
		http.Error(w, "Could not load versions.", http.StatusInternalServerError)
		return
	}
	wanted := r.URL.Query().Get("version")
	var version *models.Version
	for _, candidate := range versions {
		if candidate.Archived {
			continue
		}
		data.Choices = append(data.Choices, progressChoice{Value: candidate.ID, Name: candidate.Name + " (" + candidate.State() + ")"})
		if version == nil && (wanted == "" || candidate.ID == wanted) {
			version = candidate
		}
	}
	if wanted != "" && version == nil {
		http.NotFound(w, r)
		return
	}
	if boards.Board != nil && version != nil {
		data.Selected = version.ID
		issues, err := h.Store.VersionIssues(r.Context(), workspaceID, user.ID, boards.Project.ID, version.ID, "fixVersions")
		if err != nil {
			http.Error(w, "Could not load the work in the version.", http.StatusInternalServerError)
			return
		}
		report, err := h.Store.BurndownReport(r.Context(), workspaceID, boards.Board, issues, time.Now())
		if err != nil {
			http.Error(w, "Could not calculate the release burndown.", http.StatusInternalServerError)
			return
		}
		data.View = newBurndownView(report)
	}
	h.writeBurndown(w, r, user, workspaceID, data)
}

// writeBurndown answers with the chart's data or its page, whichever was
// asked for.
func (h *Handler) writeBurndown(w http.ResponseWriter, r *http.Request, user *models.User, workspaceID string, data burndownReportData) {
	if wantsCSV(r) {
		rows := [][]string{}
		if data.View != nil {
			for _, bar := range data.View.Bars {
				rows = append(rows, []string{bar.Name, bar.CompletedText, bar.AddedText, bar.RemainingText})
			}
		}
		writeReportCSV(w, data.Project.Key+" "+data.Kind+" "+data.Selected,
			[]string{"Sprint", "Completed", "Added", "Remaining"}, rows)
		return
	}
	actions, err := h.reportActions(r, workspaceID, user)
	if err != nil {
		http.Error(w, "Could not load report emails.", http.StatusInternalServerError)
		return
	}
	data.Actions = actions
	h.writeWorkspacePage(w, r, "page_burndown_report", user, workspaceID, data, "reports", data.Project.ID)
}

// newBurndownView lays the sprints out as bars with the remaining work as a
// line above them, which is how Jira draws both burndowns.
func newBurndownView(report models.BurndownReport) *burndownView {
	view := &burndownView{
		BurndownReport: report,
		Width:          720, Height: 260, Left: 56, Right: 700, Bottom: 220, TickX: 48,
		RemainingText: chartNumber(report.Remaining),
	}
	highest := report.Remaining
	for _, sprint := range report.Sprints {
		for _, value := range []float64{sprint.Remaining, sprint.Completed + sprint.Added} {
			if value > highest {
				highest = value
			}
		}
	}
	if highest <= 0 {
		highest = 1
	}
	for tick := 0; tick <= 4; tick++ {
		value := highest * float64(tick) / 4
		view.Ticks = append(view.Ticks, chartTick{
			Y:     view.Bottom - (view.Bottom-40)*float64(tick)/4,
			Label: chartNumber(value),
		})
	}
	count := len(report.Sprints)
	if count == 0 {
		return view
	}
	span := (view.Right - view.Left) / float64(count)
	view.BarWidth = math.Max(12, math.Min(48, span*0.5))
	points := make([]string, 0, count)
	for index, sprint := range report.Sprints {
		centre := view.Left + span*(float64(index)+0.5)
		bar := burndownBar{
			Name: sprint.Sprint.Name, Added: sprint.Added, Completed: sprint.Completed, Remaining: sprint.Remaining,
			AddedText: chartNumber(sprint.Added), CompletedText: chartNumber(sprint.Completed),
			RemainingText: chartNumber(sprint.Remaining),
			X:             centre - view.BarWidth/2,
		}
		scale := func(value float64) float64 { return (view.Bottom - 40) * value / highest }
		bar.CompletedHeight = scale(sprint.Completed)
		bar.CompletedY = view.Bottom - bar.CompletedHeight
		bar.AddedHeight = scale(sprint.Added)
		bar.AddedY = bar.CompletedY - bar.AddedHeight
		bar.PointX, bar.PointY = centre, view.Bottom-scale(sprint.Remaining)
		points = append(points, fmt.Sprintf("%.1f,%.1f", bar.PointX, bar.PointY))
		view.Bars = append(view.Bars, bar)
	}
	view.Line = strings.Join(points, " ")
	return view
}
