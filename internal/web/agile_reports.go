package web

import (
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/e6qu/zzira/internal/models"
)

// agileReportBoards is a project's scrum boards and the one a report reads.
type agileReportBoards struct {
	Project *models.Project
	Boards  []*models.Board
	Board   *models.Board
}

type sprintReportData struct {
	agileReportBoards
	Sprints []*models.Sprint
	Sprint  *models.Sprint
	Report  *sprintReportView
}

type velocityReportData struct {
	agileReportBoards
	Report *velocityReportView
}

type chartTick struct {
	Y     float64
	Label string
}

// burndownChart is the SVG geometry of a sprint burndown.
type burndownChart struct {
	// TickX is where the value labels end, left of the plot.
	TickX                              float64
	Width, Height, Left, Right, Bottom float64
	Remaining                          string
	Guideline                          bool
	GuideX1, GuideY1, GuideX2, GuideY2 float64
	Ticks                              []chartTick
	StartLabel, EndLabel               string
}

type burndownEventView struct {
	models.SprintBurndownEvent
	When          string
	ChangeDisplay string
	RemainingText string
}

// sprintReportSection is one table of work in the sprint report.
type sprintReportSection struct {
	ID, Title, Empty string
	Issues           []models.SprintReportIssue
}

type sprintReportView struct {
	models.SprintReport
	Sections                                     []sprintReportSection
	StartDisplay, EndDisplay, PlannedEndDisplay  string
	CompletedText, NotCompletedText, RemovedText string
	AddedAfterStart                              int
	Events                                       []burndownEventView
	Chart                                        burndownChart
}

type velocityBar struct {
	Label                                   string
	Name, Commitment, Completed             string
	X, CommitmentY, CommitmentHeight        float64
	CompletedX, CompletedY, CompletedHeight float64
	LabelX                                  float64
}

type velocityReportView struct {
	models.VelocityReport
	TickX                              float64
	Width, Height, Left, Right, Bottom float64
	Ticks                              []chartTick
	Bars                               []velocityBar
	AverageCompleted                   string
}

// shortLabel fits a name under a chart bar; the full name stays in the
// bar's tooltip and the data table.
func shortLabel(name string, limit int) string {
	runes := []rune(name)
	if len(runes) <= limit {
		return name
	}
	return string(runes[:limit-1]) + "…"
}

func chartNumber(value float64) string {
	return strconv.FormatFloat(math.Round(value*100)/100, 'f', -1, 64)
}

// chartScale rounds a maximum up so axis ticks read as whole numbers.
func chartScale(maximum float64) float64 {
	if maximum <= 0 {
		return 1
	}
	return math.Ceil(maximum)
}

func chartTicks(maximum, top, bottom float64) []chartTick {
	ticks := make([]chartTick, 0, 3)
	for _, share := range []float64{0, 0.5, 1} {
		ticks = append(ticks, chartTick{Y: bottom - share*(bottom-top), Label: chartNumber(maximum * share)})
	}
	return ticks
}

// newBurndownChart lays out remaining work as a step line against the
// guideline from the starting remaining work to zero at the planned end.
func newBurndownChart(report models.SprintReport, layout string) burndownChart {
	chart := burndownChart{Width: 640, Height: 260, Left: 48, Right: 624, Bottom: 220, TickX: 40}
	const top = 16.0
	start, err := time.Parse(time.RFC3339, report.Start)
	if err != nil || len(report.Burndown) == 0 {
		return chart
	}
	end, err := time.Parse(time.RFC3339, report.End)
	if err != nil {
		end = start
	}
	finish := end
	planned, plannedErr := time.Parse(time.RFC3339, report.Sprint.EndDate)
	if plannedErr == nil && planned.After(finish) {
		finish = planned
	}
	span := finish.Sub(start).Seconds()
	if span <= 0 {
		span = 1
	}
	maximum := 0.0
	for _, event := range report.Burndown {
		maximum = math.Max(maximum, math.Max(event.Scope, event.Remaining))
	}
	maximum = chartScale(maximum)
	x := func(at time.Time) float64 {
		offset := math.Min(math.Max(at.Sub(start).Seconds(), 0), span)
		return math.Round((chart.Left+offset/span*(chart.Right-chart.Left))*10) / 10
	}
	y := func(value float64) float64 {
		return math.Round((chart.Bottom-value/maximum*(chart.Bottom-top))*10) / 10
	}
	points := make([]string, 0, len(report.Burndown)*2+1)
	previous := report.Burndown[0].Remaining
	points = append(points, fmt.Sprintf("%g,%g", x(start), y(previous)))
	for _, event := range report.Burndown[1:] {
		at, err := time.Parse(time.RFC3339, event.At)
		if err != nil {
			continue
		}
		points = append(points, fmt.Sprintf("%g,%g", x(at), y(previous)), fmt.Sprintf("%g,%g", x(at), y(event.Remaining)))
		previous = event.Remaining
	}
	points = append(points, fmt.Sprintf("%g,%g", x(end), y(previous)))
	chart.Remaining = strings.Join(points, " ")
	if plannedErr == nil {
		chart.Guideline = true
		chart.GuideX1, chart.GuideY1 = x(start), y(report.Burndown[0].Remaining)
		chart.GuideX2, chart.GuideY2 = x(planned), chart.Bottom
	}
	chart.Ticks = chartTicks(maximum, top, chart.Bottom)
	chart.StartLabel = start.Format(layout)
	chart.EndLabel = finish.Format(layout)
	return chart
}

func displayTime(value, layout string) string {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return ""
	}
	return parsed.Format(layout)
}

func newSprintReportView(report models.SprintReport, look siteDateLayouts) *sprintReportView {
	view := &sprintReportView{
		SprintReport: report,
		StartDisplay: displayTime(report.Start, look.day), EndDisplay: displayTime(report.End, look.day),
		PlannedEndDisplay: displayTime(report.Sprint.EndDate, look.day),
		CompletedText:     chartNumber(report.CompletedTotal), NotCompletedText: chartNumber(report.NotCompletedTotal),
		RemovedText: chartNumber(report.RemovedTotal),
		Chart:       newBurndownChart(report, look.day),
	}
	view.Sections = []sprintReportSection{
		{ID: "completed-work", Title: "Completed work items", Empty: "No work was completed in this sprint.", Issues: report.Completed},
		{ID: "incomplete-work", Title: "Work items not completed", Empty: "All work in this sprint was completed.", Issues: report.NotCompleted},
		{ID: "outside-work", Title: "Work items completed outside of this sprint", Empty: "No work was already complete when it joined the sprint.", Issues: report.CompletedOutside},
		{ID: "removed-work", Title: "Work items removed from sprint", Empty: "No work was removed from this sprint.", Issues: report.Removed},
	}
	for _, section := range view.Sections {
		for _, issue := range section.Issues {
			if issue.AddedAfterStart {
				view.AddedAfterStart++
			}
		}
	}
	for _, event := range report.Burndown {
		change := ""
		if event.Change > 0 {
			change = "+" + chartNumber(event.Change)
		} else if event.Change < 0 {
			change = chartNumber(event.Change)
		}
		view.Events = append(view.Events, burndownEventView{SprintBurndownEvent: event, When: displayTime(event.At, look.complete), ChangeDisplay: change, RemainingText: chartNumber(event.Remaining)})
	}
	return view
}

func newVelocityReportView(report models.VelocityReport) *velocityReportView {
	const top, groupWidth = 16.0, 96.0
	view := &velocityReportView{VelocityReport: report, Height: 260, Left: 48, Bottom: 220, TickX: 40}
	view.Width = math.Max(640, view.Left+groupWidth*float64(len(report.Sprints))+16)
	view.Right = view.Width - 16
	maximum, completed := 0.0, 0.0
	for _, sprint := range report.Sprints {
		maximum = math.Max(maximum, math.Max(sprint.Commitment, sprint.Completed))
		completed += sprint.Completed
	}
	maximum = chartScale(maximum)
	height := func(value float64) float64 {
		return math.Round(value/maximum*(view.Bottom-top)*10) / 10
	}
	for index, sprint := range report.Sprints {
		x := view.Left + groupWidth*float64(index) + 16
		bar := velocityBar{
			Name: sprint.Sprint.Name, Label: shortLabel(sprint.Sprint.Name, 14), Commitment: chartNumber(sprint.Commitment), Completed: chartNumber(sprint.Completed),
			X: x, CommitmentHeight: height(sprint.Commitment), CompletedX: x + 32, CompletedHeight: height(sprint.Completed),
			LabelX: x + 30,
		}
		bar.CommitmentY = view.Bottom - bar.CommitmentHeight
		bar.CompletedY = view.Bottom - bar.CompletedHeight
		view.Bars = append(view.Bars, bar)
	}
	view.Ticks = chartTicks(maximum, top, view.Bottom)
	if len(report.Sprints) > 0 {
		view.AverageCompleted = chartNumber(completed / float64(len(report.Sprints)))
	}
	return view
}

// siteDateLayouts carries the site's display layouts into report views.
type siteDateLayouts struct{ day, complete string }

// reportProject loads the project a report page belongs to. Projects that
// turned Reports off have no report pages.
func (h *Handler) reportProject(w http.ResponseWriter, r *http.Request, workspaceID string) (*models.Project, bool) {
	project, err := h.Store.ProjectByIDOrKey(r.Context(), workspaceID, r.PathValue("key"))
	if err != nil {
		http.NotFound(w, r)
		return nil, false
	}
	enabled, err := h.Store.ProjectFeatureEnabled(r.Context(), project.ID, "jsw.classic.reports")
	if err != nil {
		http.Error(w, "Could not load reports.", http.StatusInternalServerError)
		return nil, false
	}
	if !enabled {
		http.NotFound(w, r)
		return nil, false
	}
	return project, true
}

func boardMatches(board *models.Board, wanted string) bool {
	return board.ID == wanted || fmt.Sprint(board.JiraID) == wanted
}

func sprintMatches(sprint *models.Sprint, wanted string) bool {
	return sprint.ID == wanted || strconv.FormatInt(sprint.JiraID, 10) == wanted
}

// agileReportContext loads the project's scrum boards and the board the
// request names, or the first one.
func (h *Handler) agileReportContext(w http.ResponseWriter, r *http.Request) (*models.User, string, agileReportBoards, bool) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return nil, "", agileReportBoards{}, false
	}
	project, ok := h.reportProject(w, r, workspaceID)
	if !ok {
		return nil, "", agileReportBoards{}, false
	}
	boards, err := h.Store.BoardsByProject(r.Context(), workspaceID, project.ID)
	if err != nil {
		http.Error(w, "Could not load boards.", http.StatusInternalServerError)
		return nil, "", agileReportBoards{}, false
	}
	data := agileReportBoards{Project: project}
	for _, board := range boards {
		if board.Type == "scrum" {
			data.Boards = append(data.Boards, board)
		}
	}
	wanted := r.URL.Query().Get("board")
	for _, board := range data.Boards {
		if wanted == "" || boardMatches(board, wanted) {
			data.Board = board
			break
		}
	}
	if wanted != "" && data.Board == nil {
		http.NotFound(w, r)
		return nil, "", agileReportBoards{}, false
	}
	return user, workspaceID, data, true
}

// SprintReport renders Jira's sprint report for a started sprint.
func (h *Handler) SprintReport(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, boards, ok := h.agileReportContext(w, r)
	if !ok {
		return
	}
	data := sprintReportData{agileReportBoards: boards}
	if boards.Board != nil {
		sprints, err := h.Store.SprintsByBoard(r.Context(), boards.Board.ID)
		if err != nil {
			http.Error(w, "Could not load sprints.", http.StatusInternalServerError)
			return
		}
		for _, sprint := range sprints {
			if sprint.State != "future" {
				data.Sprints = append(data.Sprints, sprint)
			}
		}
		// The active sprint leads, then the most recently completed.
		sort.SliceStable(data.Sprints, func(i, j int) bool {
			if (data.Sprints[i].State == "active") != (data.Sprints[j].State == "active") {
				return data.Sprints[i].State == "active"
			}
			return data.Sprints[i].CompleteDate > data.Sprints[j].CompleteDate
		})
		wanted := r.URL.Query().Get("sprint")
		for _, sprint := range data.Sprints {
			if wanted == "" || sprintMatches(sprint, wanted) {
				data.Sprint = sprint
				break
			}
		}
		if wanted != "" && data.Sprint == nil {
			http.NotFound(w, r)
			return
		}
		if data.Sprint != nil {
			report, err := h.Store.SprintReport(r.Context(), workspaceID, user.ID, boards.Board, data.Sprint, time.Now())
			if err != nil {
				http.Error(w, "Could not calculate the sprint report.", http.StatusInternalServerError)
				return
			}
			look := h.siteLook(r, workspaceID)
			data.Report = newSprintReportView(report, siteDateLayouts{day: look.DateDay, complete: look.DateComplete})
		}
	}
	h.writeWorkspacePage(w, r, "page_sprint_report", user, workspaceID, data, "reports", boards.Project.ID)
}

// VelocityReport renders the velocity chart for a scrum board.
func (h *Handler) VelocityReport(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, boards, ok := h.agileReportContext(w, r)
	if !ok {
		return
	}
	data := velocityReportData{agileReportBoards: boards}
	if boards.Board != nil {
		report, err := h.Store.VelocityReport(r.Context(), workspaceID, user.ID, boards.Board)
		if err != nil {
			http.Error(w, "Could not calculate velocity.", http.StatusInternalServerError)
			return
		}
		data.Report = newVelocityReportView(report)
	}
	h.writeWorkspacePage(w, r, "page_velocity_report", user, workspaceID, data, "reports", boards.Project.ID)
}
