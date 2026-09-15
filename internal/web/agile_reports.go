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
	Actions reportActions
	Sprints []*models.Sprint
	Sprint  *models.Sprint
	Report  *sprintReportView
}

type velocityReportData struct {
	agileReportBoards
	Actions reportActions
	Report  *velocityReportView
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
	if wantsCSV(r) {
		rows := [][]string{}
		if data.Report != nil {
			for _, section := range data.Report.Sections {
				rows = append(rows, workItemCSVRows(section.Title, section.Issues)...)
			}
		}
		writeReportCSV(w, boards.Project.Key+" sprint report", workItemCSVHeader(), rows)
		return
	}
	actions, actionsErr := h.reportActions(r, workspaceID, user)
	if actionsErr != nil {
		http.Error(w, "Could not load report emails.", http.StatusInternalServerError)
		return
	}
	data.Actions = actions
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
	if wantsCSV(r) {
		rows := [][]string{}
		if data.Report != nil {
			for _, bar := range data.Report.Bars {
				rows = append(rows, []string{bar.Name, bar.Commitment, bar.Completed})
			}
		}
		writeReportCSV(w, boards.Project.Key+" velocity chart", []string{"Sprint", "Commitment", "Completed"}, rows)
		return
	}
	actions, actionsErr := h.reportActions(r, workspaceID, user)
	if actionsErr != nil {
		http.Error(w, "Could not load report emails.", http.StatusInternalServerError)
		return
	}
	data.Actions = actions
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
	Compare         bool
	Comparison      map[string]string
	PreviousSamples []models.CycleSample
	agileReportBoards
	Actions reportActions
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
	data := flowReportData{agileReportBoards: boards, Days: days, Windows: flowWindows, Compare: page == "page_control_chart_report" && wantsComparison(r)}
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
			if data.Compare {
				previous, err := h.Store.ControlChart(r.Context(), boards.Board, user.ID, days, previousPeriod(now, days))
				if err != nil {
					http.Error(w, "Could not calculate the control chart.", http.StatusInternalServerError)
					return
				}
				data.PreviousSamples = previous.Samples
				data.Comparison = map[string]string{
					"completed": compareCount(len(chart.Samples), len(previous.Samples), days),
					"average":   compareDuration(chart.AverageSeconds, previous.AverageSeconds, days),
					"median":    compareDuration(chart.MedianSeconds, previous.MedianSeconds, days),
				}
			}
		}
	}
	if wantsCSV(r) {
		if data.Flow != nil {
			header := []string{"Date"}
			for _, column := range data.Flow.Columns {
				header = append(header, column.Name)
			}
			rows := [][]string{}
			for _, day := range data.Flow.Days {
				row := []string{day.Date}
				for _, count := range day.Counts {
					row = append(row, strconv.Itoa(count))
				}
				rows = append(rows, row)
			}
			writeReportCSV(w, boards.Project.Key+" cumulative flow", header, rows)
			return
		}
		header, rows := []string{"Work item", "Summary", "Completed", "Cycle time (hours)"}, [][]string{}
		if data.Control != nil {
			rows = cycleCSVRows(data.Control.Samples)
			if data.Compare {
				header, rows = withPeriods(header, cycleCSVRows(data.PreviousSamples), rows)
			}
		}
		writeReportCSV(w, boards.Project.Key+" control chart", header, rows)
		return
	}
	actions, actionsErr := h.reportActions(r, workspaceID, user)
	if actionsErr != nil {
		http.Error(w, "Could not load report emails.", http.StatusInternalServerError)
		return
	}
	data.Actions = actions
	h.writeWorkspacePage(w, r, page, user, workspaceID, data, "reports", boards.Project.ID)
}

// progressChart is the SVG geometry of an epic or version report.
type progressChart struct {
	Width, Height, Left, Right, Bottom, TickX float64
	Total, Completed                          string
	Ticks                                     []chartTick
	StartLabel, EndLabel                      string
}

type progressReportView struct {
	models.ProgressReport
	Chart                                   progressChart
	Percent                                 int
	TotalText, CompletedText, RemainingText string
	Sections                                []sprintReportSection
}

// newProgressReportView draws the total and completed estimate as daily
// lines and splits the work into what is done and what remains.
func newProgressReportView(report models.ProgressReport, layout string) *progressReportView {
	const top = 16.0
	view := &progressReportView{ProgressReport: report, Percent: report.Progress.Percent(),
		TotalText: chartNumber(report.TotalEstimate), CompletedText: chartNumber(report.CompletedEstimate),
		RemainingText: chartNumber(report.TotalEstimate - report.CompletedEstimate)}
	view.Chart = progressChart{Width: 640, Height: 260, Left: 48, Right: 624, Bottom: 220, TickX: 40}
	view.Sections = []sprintReportSection{
		{ID: "done-work", Title: "Completed work items", Empty: "No work is done yet.", Issues: report.Completed},
		{ID: "remaining-work", Title: "Incomplete work items", Empty: "All the work is done.", Issues: report.Incomplete},
	}
	if len(report.Points) == 0 {
		return view
	}
	maximum := 0.0
	for _, point := range report.Points {
		maximum = math.Max(maximum, point.Total)
	}
	maximum = chartScale(maximum)
	step := (view.Chart.Right - view.Chart.Left) / math.Max(float64(len(report.Points)-1), 1)
	x := func(index int) float64 { return math.Round((view.Chart.Left+float64(index)*step)*10) / 10 }
	y := func(value float64) float64 {
		return math.Round((view.Chart.Bottom-value/maximum*(view.Chart.Bottom-top))*10) / 10
	}
	total, completed := make([]string, 0, len(report.Points)), make([]string, 0, len(report.Points))
	for index, point := range report.Points {
		total = append(total, fmt.Sprintf("%g,%g", x(index), y(point.Total)))
		completed = append(completed, fmt.Sprintf("%g,%g", x(index), y(point.Completed)))
	}
	view.Chart.Total, view.Chart.Completed = strings.Join(total, " "), strings.Join(completed, " ")
	view.Chart.Ticks = chartTicks(maximum, top, view.Chart.Bottom)
	view.Chart.StartLabel = displayDay(report.Points[0].Date, layout)
	view.Chart.EndLabel = displayDay(report.Points[len(report.Points)-1].Date, layout)
	return view
}

type progressChoice struct {
	Value, Name string
}

type progressReportData struct {
	agileReportBoards
	Actions                       reportActions
	Kind, Title, Parameter, Empty string
	Choices                       []progressChoice
	Selected                      string
	Report                        *progressReportView
}

// EpicReport renders the progress of the work in an epic.
func (h *Handler) EpicReport(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, boards, ok := h.boardReportContext(w, r, false)
	if !ok {
		return
	}
	data := progressReportData{agileReportBoards: boards, Kind: "epic", Title: "Epic report", Parameter: "epic", Empty: "Create an epic to follow its progress."}
	epics, err := h.Store.EpicsInProjects(r.Context(), workspaceID, user.ID, []string{boards.Project.ID})
	if err != nil {
		http.Error(w, "Could not load epics.", http.StatusInternalServerError)
		return
	}
	wanted := r.URL.Query().Get("epic")
	var epic *models.Issue
	for _, candidate := range epics {
		data.Choices = append(data.Choices, progressChoice{Value: candidate.Key, Name: candidate.Key + " " + candidate.Summary})
		if epic == nil && (wanted == "" || strings.EqualFold(candidate.Key, wanted)) {
			epic = candidate
		}
	}
	if wanted != "" && epic == nil {
		http.NotFound(w, r)
		return
	}
	if boards.Board != nil && epic != nil {
		data.Selected = epic.Key
		children, err := h.Store.EpicChildren(r.Context(), workspaceID, user.ID, []string{epic.ID})
		if err != nil {
			http.Error(w, "Could not load the work in the epic.", http.StatusInternalServerError)
			return
		}
		start, _ := time.Parse(time.RFC3339, epic.CreatedAt)
		report, err := h.Store.ProgressReport(r.Context(), workspaceID, boards.Board, children, start, time.Now())
		if err != nil {
			http.Error(w, "Could not calculate the epic report.", http.StatusInternalServerError)
			return
		}
		data.Report = newProgressReportView(report, h.siteLook(r, workspaceID).DateDay)
	}
	if wantsCSV(r) {
		rows := [][]string{}
		if data.Report != nil {
			for _, section := range data.Report.Sections {
				rows = append(rows, workItemCSVRows(section.Title, section.Issues)...)
			}
		}
		writeReportCSV(w, boards.Project.Key+" "+data.Kind+" report "+data.Selected, workItemCSVHeader(), rows)
		return
	}
	actions, actionsErr := h.reportActions(r, workspaceID, user)
	if actionsErr != nil {
		http.Error(w, "Could not load report emails.", http.StatusInternalServerError)
		return
	}
	data.Actions = actions
	h.writeWorkspacePage(w, r, "page_progress_report", user, workspaceID, data, "reports", boards.Project.ID)
}

// VersionReport renders the progress of the work fixed in a version.
func (h *Handler) VersionReport(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, boards, ok := h.boardReportContext(w, r, false)
	if !ok {
		return
	}
	data := progressReportData{agileReportBoards: boards, Kind: "version", Title: "Version report", Parameter: "version", Empty: "Create a version to follow its progress."}
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
		start, _ := time.Parse("2006-01-02", version.StartDate)
		report, err := h.Store.ProgressReport(r.Context(), workspaceID, boards.Board, issues, start, time.Now())
		if err != nil {
			http.Error(w, "Could not calculate the version report.", http.StatusInternalServerError)
			return
		}
		data.Report = newProgressReportView(report, h.siteLook(r, workspaceID).DateDay)
	}
	if wantsCSV(r) {
		rows := [][]string{}
		if data.Report != nil {
			for _, section := range data.Report.Sections {
				rows = append(rows, workItemCSVRows(section.Title, section.Issues)...)
			}
		}
		writeReportCSV(w, boards.Project.Key+" "+data.Kind+" report "+data.Selected, workItemCSVHeader(), rows)
		return
	}
	actions, actionsErr := h.reportActions(r, workspaceID, user)
	if actionsErr != nil {
		http.Error(w, "Could not load report emails.", http.StatusInternalServerError)
		return
	}
	data.Actions = actions
	h.writeWorkspacePage(w, r, "page_progress_report", user, workspaceID, data, "reports", boards.Project.ID)
}

// analysisWindows are the day ranges the issue analysis reports offer.
var analysisWindows = []int{7, 30, 90}

type createdResolvedRow struct {
	Date                                           string
	Created, Resolved, CreatedTotal, ResolvedTotal int
}

// createdResolvedView draws created and resolved work as two lines, daily or
// as running totals.
type createdResolvedView struct {
	models.CreatedResolvedReport
	Cumulative                                bool
	Width, Height, Left, Right, Bottom, TickX float64
	Created, Resolved                         string
	Ticks                                     []chartTick
	Rows                                      []createdResolvedRow
	StartLabel, EndLabel                      string
}

func newCreatedResolvedView(report models.CreatedResolvedReport, cumulative bool, layout string) *createdResolvedView {
	const top = 16.0
	view := &createdResolvedView{CreatedResolvedReport: report, Cumulative: cumulative, Width: 640, Height: 260, Left: 48, Right: 624, Bottom: 220, TickX: 40}
	if len(report.Days) == 0 {
		return view
	}
	values := func(day models.CreatedResolvedDay) (float64, float64) {
		if cumulative {
			return float64(day.CreatedTotal), float64(day.ResolvedTotal)
		}
		return float64(day.Created), float64(day.Resolved)
	}
	maximum := 0.0
	for _, day := range report.Days {
		created, resolved := values(day)
		maximum = math.Max(maximum, math.Max(created, resolved))
	}
	maximum = chartScale(maximum)
	step := (view.Right - view.Left) / math.Max(float64(len(report.Days)-1), 1)
	x := func(index int) float64 { return math.Round((view.Left+float64(index)*step)*10) / 10 }
	y := func(value float64) float64 { return math.Round((view.Bottom-value/maximum*(view.Bottom-top))*10) / 10 }
	created, resolved := make([]string, 0, len(report.Days)), make([]string, 0, len(report.Days))
	for index, day := range report.Days {
		createdValue, resolvedValue := values(day)
		created = append(created, fmt.Sprintf("%g,%g", x(index), y(createdValue)))
		resolved = append(resolved, fmt.Sprintf("%g,%g", x(index), y(resolvedValue)))
		view.Rows = append(view.Rows, createdResolvedRow{Date: displayDay(day.Date, layout), Created: day.Created, Resolved: day.Resolved, CreatedTotal: day.CreatedTotal, ResolvedTotal: day.ResolvedTotal})
	}
	view.Created, view.Resolved = strings.Join(created, " "), strings.Join(resolved, " ")
	view.Ticks = chartTicks(maximum, top, view.Bottom)
	view.StartLabel, view.EndLabel = view.Rows[0].Date, view.Rows[len(view.Rows)-1].Date
	return view
}

type resolutionBar struct {
	Date, Average string
	Resolved      int
	X, Y, Height  float64
}

// resolutionTimeView draws the average resolution time of each day as a bar.
type resolutionTimeView struct {
	models.ResolutionTimeReport
	Width, Height, Left, Right, Bottom, TickX, BarWidth float64
	Bars                                                []resolutionBar
	Ticks                                               []chartTick
	Average                                             string
	StartLabel, EndLabel                                string
}

func newResolutionTimeView(report models.ResolutionTimeReport, layout string) *resolutionTimeView {
	const top = 16.0
	view := &resolutionTimeView{ResolutionTimeReport: report, Width: 640, Height: 260, Left: 56, Right: 624, Bottom: 220, TickX: 48}
	if len(report.Days) == 0 {
		return view
	}
	maximumHours := 1.0
	for _, day := range report.Days {
		maximumHours = math.Max(maximumHours, float64(day.AverageSeconds)/3600)
	}
	maximumHours = chartScale(maximumHours)
	slot := (view.Right - view.Left) / float64(len(report.Days))
	view.BarWidth = math.Round(math.Max(slot*0.7, 1)*10) / 10
	for index, day := range report.Days {
		height := math.Round(float64(day.AverageSeconds)/3600/maximumHours*(view.Bottom-top)*10) / 10
		bar := resolutionBar{Date: displayDay(day.Date, layout), Resolved: day.Resolved, Height: height,
			X: math.Round((view.Left+float64(index)*slot+(slot-view.BarWidth)/2)*10) / 10, Y: view.Bottom - height}
		if day.Resolved > 0 {
			bar.Average = cycleDuration(day.AverageSeconds)
		}
		view.Bars = append(view.Bars, bar)
	}
	for _, tick := range chartTicks(maximumHours, top, view.Bottom) {
		hours, _ := strconv.ParseFloat(tick.Label, 64)
		view.Ticks = append(view.Ticks, chartTick{Y: tick.Y, Label: cycleDuration(int64(hours * 3600))})
	}
	if report.Resolved > 0 {
		view.Average = cycleDuration(report.AverageSeconds)
	}
	view.StartLabel, view.EndLabel = view.Bars[0].Date, view.Bars[len(view.Bars)-1].Date
	return view
}

type issueAnalysisData struct {
	Compare            bool
	Comparison         map[string]string
	PreviousCreated    []models.CreatedResolvedDay
	PreviousResolution []models.ResolutionDay
	Actions            reportActions
	Project            *models.Project
	Days               int
	Windows            []int
	CreatedResolved    *createdResolvedView
	Resolution         *resolutionTimeView
}

func analysisWindow(r *http.Request) (int, bool) {
	value := r.URL.Query().Get("days")
	if value == "" {
		return 30, true
	}
	for _, days := range analysisWindows {
		if strconv.Itoa(days) == value {
			return days, true
		}
	}
	return 0, false
}

// CreatedVsResolvedReport renders the project's created vs. resolved work.
func (h *Handler) CreatedVsResolvedReport(w http.ResponseWriter, r *http.Request) {
	h.issueAnalysisReport(w, r, "page_created_resolved_report")
}

// ResolutionTimeReport renders how long the project's work takes to resolve.
func (h *Handler) ResolutionTimeReport(w http.ResponseWriter, r *http.Request) {
	h.issueAnalysisReport(w, r, "page_resolution_time_report")
}

func (h *Handler) issueAnalysisReport(w http.ResponseWriter, r *http.Request, page string) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	project, ok := h.reportProject(w, r, workspaceID)
	if !ok {
		return
	}
	days, ok := analysisWindow(r)
	if !ok {
		http.Error(w, "Choose a 7, 30, or 90 day window.", http.StatusBadRequest)
		return
	}
	data := issueAnalysisData{Project: project, Days: days, Windows: analysisWindows, Compare: wantsComparison(r)}
	layout := h.siteLook(r, workspaceID).DateDay
	now := time.Now().UTC()
	if page == "page_created_resolved_report" {
		report, err := h.Store.CreatedVsResolved(r.Context(), workspaceID, user.ID, project.ID, days, now)
		if err != nil {
			http.Error(w, "Could not count created and resolved work.", http.StatusInternalServerError)
			return
		}
		data.CreatedResolved = newCreatedResolvedView(report, r.URL.Query().Get("cumulative") == "true", layout)
		if data.Compare {
			previous, err := h.Store.CreatedVsResolved(r.Context(), workspaceID, user.ID, project.ID, days, previousPeriod(now, days))
			if err != nil {
				http.Error(w, "Could not count created and resolved work.", http.StatusInternalServerError)
				return
			}
			data.PreviousCreated = previous.Days
			data.Comparison = map[string]string{
				"created":  compareCount(report.CreatedTotal, previous.CreatedTotal, days),
				"resolved": compareCount(report.ResolvedTotal, previous.ResolvedTotal, days),
			}
		}
	} else {
		report, err := h.Store.ResolutionTime(r.Context(), workspaceID, user.ID, project.ID, days, now)
		if err != nil {
			http.Error(w, "Could not calculate resolution time.", http.StatusInternalServerError)
			return
		}
		data.Resolution = newResolutionTimeView(report, layout)
		if data.Compare {
			previous, err := h.Store.ResolutionTime(r.Context(), workspaceID, user.ID, project.ID, days, previousPeriod(now, days))
			if err != nil {
				http.Error(w, "Could not calculate resolution time.", http.StatusInternalServerError)
				return
			}
			data.PreviousResolution = previous.Days
			data.Comparison = map[string]string{
				"resolved": compareCount(report.Resolved, previous.Resolved, days),
				"average":  compareDuration(report.AverageSeconds, previous.AverageSeconds, days),
			}
		}
	}
	if wantsCSV(r) {
		if data.CreatedResolved != nil {
			header, rows := []string{"Date", "Created", "Resolved", "Created in total", "Resolved in total"}, createdCSVRows(data.CreatedResolved.Days)
			if data.Compare {
				header, rows = withPeriods(header, createdCSVRows(data.PreviousCreated), rows)
			}
			writeReportCSV(w, project.Key+" created vs resolved", header, rows)
			return
		}
		header, rows := []string{"Date", "Resolved", "Average resolution time (hours)"}, resolutionCSVRows(data.Resolution.Days)
		if data.Compare {
			header, rows = withPeriods(header, resolutionCSVRows(data.PreviousResolution), rows)
		}
		writeReportCSV(w, project.Key+" resolution time", header, rows)
		return
	}
	actions, actionsErr := h.reportActions(r, workspaceID, user)
	if actionsErr != nil {
		http.Error(w, "Could not load report emails.", http.StatusInternalServerError)
		return
	}
	data.Actions = actions
	h.writeWorkspacePage(w, r, page, user, workspaceID, data, "reports", project.ID)
}

func workItemCSVHeader() []string {
	return []string{"Section", "Work item", "Summary", "Work type", "Status", "Estimate at start", "Estimate at end", "Added after start"}
}

// workItemCSVRows lists a report section's work items as CSV rows.
func workItemCSVRows(section string, issues []models.SprintReportIssue) [][]string {
	rows := make([][]string, 0, len(issues))
	for _, issue := range issues {
		added := "No"
		if issue.AddedAfterStart {
			added = "Yes"
		}
		rows = append(rows, []string{section, issue.Key, issue.Summary, issue.IssueType, issue.Status, issue.EstimateStart, issue.EstimateEnd, added})
	}
	return rows
}

func cycleCSVRows(samples []models.CycleSample) [][]string {
	rows := make([][]string, 0, len(samples))
	for _, sample := range samples {
		rows = append(rows, []string{sample.Key, sample.Summary, sample.CompletedAt, strconv.FormatFloat(float64(sample.CycleSeconds)/3600, 'f', 2, 64)})
	}
	return rows
}

func createdCSVRows(days []models.CreatedResolvedDay) [][]string {
	rows := make([][]string, 0, len(days))
	for _, day := range days {
		rows = append(rows, []string{day.Date, strconv.Itoa(day.Created), strconv.Itoa(day.Resolved), strconv.Itoa(day.CreatedTotal), strconv.Itoa(day.ResolvedTotal)})
	}
	return rows
}

func resolutionCSVRows(days []models.ResolutionDay) [][]string {
	rows := make([][]string, 0, len(days))
	for _, day := range days {
		rows = append(rows, []string{day.Date, strconv.Itoa(day.Resolved), strconv.FormatFloat(float64(day.AverageSeconds)/3600, 'f', 2, 64)})
	}
	return rows
}
