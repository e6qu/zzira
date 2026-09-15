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
	// Burnup draws scope and completed work over the same window.
	Burnup burnupChart
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

// burnupChart is the SVG geometry of a sprint burnup.
type burnupChart struct {
	Width, Height, Left, Right, Bottom, TickX float64
	Scope, Completed                          string
	Ticks                                     []chartTick
	StartLabel, EndLabel                      string
}

// newBurnupChart lays out the sprint's scope and the work completed within it
// as step lines over the sprint.
func newBurnupChart(report models.SprintReport, layout string) burnupChart {
	chart := burnupChart{Width: 640, Height: 260, Left: 48, Right: 624, Bottom: 220, TickX: 40}
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
	if planned, plannedErr := time.Parse(time.RFC3339, report.Sprint.EndDate); plannedErr == nil && planned.After(finish) {
		finish = planned
	}
	span := finish.Sub(start).Seconds()
	if span <= 0 {
		span = 1
	}
	maximum := 0.0
	for _, event := range report.Burndown {
		maximum = math.Max(maximum, event.Scope)
	}
	maximum = chartScale(maximum)
	x := func(at time.Time) float64 {
		offset := math.Min(math.Max(at.Sub(start).Seconds(), 0), span)
		return math.Round((chart.Left+offset/span*(chart.Right-chart.Left))*10) / 10
	}
	y := func(value float64) float64 {
		return math.Round((chart.Bottom-value/maximum*(chart.Bottom-top))*10) / 10
	}
	step := func(value func(models.SprintBurndownEvent) float64) string {
		previous := value(report.Burndown[0])
		points := []string{fmt.Sprintf("%g,%g", x(start), y(previous))}
		for _, event := range report.Burndown[1:] {
			at, err := time.Parse(time.RFC3339, event.At)
			if err != nil {
				continue
			}
			points = append(points, fmt.Sprintf("%g,%g", x(at), y(previous)), fmt.Sprintf("%g,%g", x(at), y(value(event))))
			previous = value(event)
		}
		points = append(points, fmt.Sprintf("%g,%g", x(end), y(previous)))
		return strings.Join(points, " ")
	}
	chart.Scope = step(func(event models.SprintBurndownEvent) float64 { return event.Scope })
	chart.Completed = step(func(event models.SprintBurndownEvent) float64 { return event.Scope - event.Remaining })
	chart.Ticks = chartTicks(maximum, top, chart.Bottom)
	chart.StartLabel, chart.EndLabel = start.Format(layout), finish.Format(layout)
	return chart
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
		Burnup:      newBurnupChart(report, look.day),
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
	return h.boardReportContext(w, r, true)
}

// boardReportContext loads the project's boards, only scrum boards when
// scrumOnly, and the board the request names, or the first one.
func (h *Handler) boardReportContext(w http.ResponseWriter, r *http.Request, scrumOnly bool) (*models.User, string, agileReportBoards, bool) {
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
		if board.Type == "scrum" || !scrumOnly {
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

// flowWindows are the day ranges the flow reports offer.
var flowWindows = []int{14, 30, 90}

func reportWindow(r *http.Request) (int, bool) {
	value := r.URL.Query().Get("days")
	if value == "" {
		return 30, true
	}
	for _, days := range flowWindows {
		if strconv.Itoa(days) == value {
			return days, true
		}
	}
	return 0, false
}

type flowBand struct {
	Name, Class, Points string
}

type flowRow struct {
	Date   string
	Counts []int
}

// cumulativeFlowView stacks each column's count, first column on top, as
// Jira draws the diagram.
type cumulativeFlowView struct {
	models.CumulativeFlow
	Width, Height, Left, Right, Bottom, TickX float64
	Bands                                     []flowBand
	Ticks                                     []chartTick
	Rows                                      []flowRow
	StartLabel, EndLabel                      string
}

func newCumulativeFlowView(flow models.CumulativeFlow, layout string) *cumulativeFlowView {
	const top = 16.0
	view := &cumulativeFlowView{CumulativeFlow: flow, Width: 640, Height: 260, Left: 48, Right: 624, Bottom: 220, TickX: 40}
	if len(flow.Days) == 0 || len(flow.Columns) == 0 {
		return view
	}
	maximum := 0.0
	for _, day := range flow.Days {
		total := 0
		for _, count := range day.Counts {
			total += count
		}
		maximum = math.Max(maximum, float64(total))
	}
	maximum = chartScale(maximum)
	step := (view.Right - view.Left) / math.Max(float64(len(flow.Days)-1), 1)
	x := func(index int) float64 { return math.Round((view.Left+float64(index)*step)*10) / 10 }
	y := func(value float64) float64 { return math.Round((view.Bottom-value/maximum*(view.Bottom-top))*10) / 10 }
	// The last column sits at the bottom; each band spans from the total of
	// the columns after it to that total plus its own count.
	for column := range flow.Columns {
		lower := make([]float64, len(flow.Days))
		upper := make([]float64, len(flow.Days))
		for index, day := range flow.Days {
			below := 0
			for later := column + 1; later < len(flow.Columns); later++ {
				below += day.Counts[later]
			}
			lower[index], upper[index] = float64(below), float64(below+day.Counts[column])
		}
		points := make([]string, 0, len(flow.Days)*2)
		for index := range flow.Days {
			points = append(points, fmt.Sprintf("%g,%g", x(index), y(upper[index])))
		}
		for index := len(flow.Days) - 1; index >= 0; index-- {
			points = append(points, fmt.Sprintf("%g,%g", x(index), y(lower[index])))
		}
		view.Bands = append(view.Bands, flowBand{Name: flow.Columns[column].Name, Class: fmt.Sprintf("flow-band-%d", column%6), Points: strings.Join(points, " ")})
	}
	view.Ticks = chartTicks(maximum, top, view.Bottom)
	for _, day := range flow.Days {
		view.Rows = append(view.Rows, flowRow{Date: displayDay(day.Date, layout), Counts: day.Counts})
	}
	view.StartLabel, view.EndLabel = view.Rows[0].Date, view.Rows[len(view.Rows)-1].Date
	return view
}

func displayDay(value, layout string) string {
	day, err := time.Parse("2006-01-02", value)
	if err != nil {
		return value
	}
	return day.Format(layout)
}

type cyclePoint struct {
	models.CycleSample
	X, Y          float64
	Completed     string
	CycleDuration string
}

// controlChartView scatters each completed item by completion time and cycle
// time, with the average as a line.
type controlChartView struct {
	models.ControlChart
	Width, Height, Left, Right, Bottom, TickX float64
	Points                                    []cyclePoint
	Ticks                                     []chartTick
	AverageY                                  float64
	Average, Median                           string
	StartLabel, EndLabel                      string
}

// cycleDuration writes a cycle time in days and hours, as Jira's chart does.
func cycleDuration(seconds int64) string {
	hours := seconds / 3600
	days, rest := hours/24, hours%24
	switch {
	case days > 0 && rest > 0:
		return fmt.Sprintf("%dd %dh", days, rest)
	case days > 0:
		return fmt.Sprintf("%dd", days)
	case hours > 0:
		return fmt.Sprintf("%dh", hours)
	}
	return fmt.Sprintf("%dm", seconds/60)
}

func newControlChartView(chart models.ControlChart, days int, now time.Time, layout, complete string) *controlChartView {
	const top = 16.0
	view := &controlChartView{ControlChart: chart, Width: 640, Height: 260, Left: 56, Right: 624, Bottom: 220, TickX: 48}
	now = now.UTC()
	since := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -(days - 1))
	span := now.Sub(since).Seconds()
	maximumHours := 1.0
	for _, sample := range chart.Samples {
		maximumHours = math.Max(maximumHours, float64(sample.CycleSeconds)/3600)
	}
	maximumHours = chartScale(maximumHours)
	x := func(at time.Time) float64 {
		offset := math.Min(math.Max(at.Sub(since).Seconds(), 0), span)
		return math.Round((view.Left+offset/span*(view.Right-view.Left))*10) / 10
	}
	y := func(seconds int64) float64 {
		return math.Round((view.Bottom-float64(seconds)/3600/maximumHours*(view.Bottom-top))*10) / 10
	}
	for _, sample := range chart.Samples {
		completed, err := time.Parse(time.RFC3339, sample.CompletedAt)
		if err != nil {
			continue
		}
		view.Points = append(view.Points, cyclePoint{CycleSample: sample, X: x(completed), Y: y(sample.CycleSeconds), Completed: completed.Format(complete), CycleDuration: cycleDuration(sample.CycleSeconds)})
	}
	view.Ticks = []chartTick{}
	for _, tick := range chartTicks(maximumHours, top, view.Bottom) {
		hours, _ := strconv.ParseFloat(tick.Label, 64)
		view.Ticks = append(view.Ticks, chartTick{Y: tick.Y, Label: cycleDuration(int64(hours * 3600))})
	}
	if len(chart.Samples) > 0 {
		view.AverageY = y(chart.AverageSeconds)
		view.Average, view.Median = cycleDuration(chart.AverageSeconds), cycleDuration(chart.MedianSeconds)
	}
	view.StartLabel, view.EndLabel = since.Format(layout), now.Format(layout)
	return view
}

type flowReportData struct {
	agileReportBoards
	Days    int
	Windows []int
	Flow    *cumulativeFlowView
	Control *controlChartView
}

// CumulativeFlowReport renders the cumulative flow diagram for a board.
func (h *Handler) CumulativeFlowReport(w http.ResponseWriter, r *http.Request) {
	h.flowReport(w, r, "page_cumulative_flow_report")
}

// ControlChartReport renders the control chart for a board.
func (h *Handler) ControlChartReport(w http.ResponseWriter, r *http.Request) {
	h.flowReport(w, r, "page_control_chart_report")
}

func (h *Handler) flowReport(w http.ResponseWriter, r *http.Request, page string) {
	user, workspaceID, boards, ok := h.boardReportContext(w, r, false)
	if !ok {
		return
	}
	days, ok := reportWindow(r)
	if !ok {
		http.Error(w, "Choose a 14, 30, or 90 day window.", http.StatusBadRequest)
		return
	}
	data := flowReportData{agileReportBoards: boards, Days: days, Windows: flowWindows}
	if boards.Board != nil {
		look := h.siteLook(r, workspaceID)
		now := time.Now()
		if page == "page_cumulative_flow_report" {
			flow, err := h.Store.CumulativeFlow(r.Context(), boards.Board, user.ID, days, now)
			if err != nil {
				http.Error(w, "Could not calculate cumulative flow.", http.StatusInternalServerError)
				return
			}
			data.Flow = newCumulativeFlowView(flow, look.DateDay)
		} else {
			chart, err := h.Store.ControlChart(r.Context(), boards.Board, user.ID, days, now)
			if err != nil {
				http.Error(w, "Could not calculate the control chart.", http.StatusInternalServerError)
				return
			}
			data.Control = newControlChartView(chart, days, now, look.DateDay, look.DateComplete)
		}
	}
	h.writeWorkspacePage(w, r, page, user, workspaceID, data, "reports", boards.Project.ID)
}
