package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

// timelineBar is where a scheduled work item sits on the timeline.
type timelineBar struct {
	X, Width float64
	// Open marks a bar missing one end, drawn to the edge it lacks.
	Open bool
}

type timelineRow struct {
	Item models.TimelineItem
	// Child is whether this row hangs under another, and Depth how deep: a
	// hierarchy can be taller than epic and story, so a row indents by how
	// far down it sits rather than by being a child at all.
	Child       bool
	Depth       int
	Indent      int
	Bar         *timelineBar
	StartLabel  string
	DueLabel    string
	Unscheduled bool
}

type timelineMonth struct {
	Label    string
	X, Width float64
}

type timelineData struct {
	Project      *models.Project
	Rows         []timelineRow
	Months       []timelineMonth
	Width        float64
	TodayX       float64
	ShowToday    bool
	StartFieldID string
	Notice       string
	Error        string
}

func parseTimelineDay(value string) (time.Time, bool) {
	day, err := time.Parse("2006-01-02", value)
	return day, err == nil
}

// newTimelineData lays scheduled work across whole months, from the month of
// the earliest date to the month of the latest, and at least three months
// from today when nothing is scheduled.
func newTimelineData(project *models.Project, timeline models.ProjectTimeline, today time.Time, layout string) timelineData {
	const monthWidth = 160.0
	data := timelineData{Project: project, StartFieldID: timeline.StartFieldID}
	var earliest, latest time.Time
	consider := func(values ...string) {
		for _, value := range values {
			if day, ok := parseTimelineDay(value); ok {
				if earliest.IsZero() || day.Before(earliest) {
					earliest = day
				}
				if latest.IsZero() || day.After(latest) {
					latest = day
				}
			}
		}
	}
	var considerTree func(items []models.TimelineItem)
	considerTree = func(items []models.TimelineItem) {
		for _, item := range items {
			consider(item.StartDate, item.DueDate)
			considerTree(item.Children)
		}
	}
	considerTree(timeline.Epics)
	today = time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)
	if earliest.IsZero() {
		earliest, latest = today, today
	}
	first := time.Date(earliest.Year(), earliest.Month(), 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(latest.Year(), latest.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, 1, 0)
	if minimum := first.AddDate(0, 3, 0); end.Before(minimum) {
		end = minimum
	}
	for month := first; month.Before(end); month = month.AddDate(0, 1, 0) {
		data.Months = append(data.Months, timelineMonth{Label: month.Format("Jan 2006"), X: float64(len(data.Months)) * monthWidth, Width: monthWidth})
	}
	data.Width = float64(len(data.Months)) * monthWidth
	span := end.Sub(first).Hours()
	x := func(day time.Time) float64 {
		return math.Round(math.Min(math.Max(day.Sub(first).Hours()/span, 0), 1)*data.Width*10) / 10
	}
	if !today.Before(first) && today.Before(end) {
		data.ShowToday, data.TodayX = true, x(today)
	}
	row := func(item models.TimelineItem, depth int) timelineRow {
		out := timelineRow{Item: item, Child: depth > 0, Depth: depth, Indent: depth * 20}
		start, hasStart := parseTimelineDay(item.StartDate)
		due, hasDue := parseTimelineDay(item.DueDate)
		if hasStart {
			out.StartLabel = start.Format(layout)
		}
		if hasDue {
			out.DueLabel = due.Format(layout)
		}
		switch {
		case hasStart && hasDue:
			// A due date is the last working day, so the bar runs through it.
			left, right := x(start), x(due.AddDate(0, 0, 1))
			out.Bar = &timelineBar{X: left, Width: math.Max(right-left, 4)}
		case hasStart:
			left := x(start)
			out.Bar = &timelineBar{X: left, Width: math.Max(data.Width-left, 4), Open: true}
		case hasDue:
			out.Bar = &timelineBar{X: 0, Width: math.Max(x(due.AddDate(0, 0, 1)), 4), Open: true}
		default:
			out.Unscheduled = true
		}
		return out
	}
	var appendRows func(items []models.TimelineItem, depth int)
	appendRows = func(items []models.TimelineItem, depth int) {
		for _, item := range items {
			data.Rows = append(data.Rows, row(item, depth))
			appendRows(item.Children, depth+1)
		}
	}
	appendRows(timeline.Epics, 0)
	return data
}

// timelineProject loads a software project whose Roadmap feature is on.
func (h *Handler) timelineProject(w http.ResponseWriter, r *http.Request, workspaceID string) (*models.Project, bool) {
	project, err := h.Store.ProjectByIDOrKey(r.Context(), workspaceID, r.PathValue("key"))
	if err != nil || project.ProjectTypeKey != "software" {
		http.NotFound(w, r)
		return nil, false
	}
	enabled, err := h.Store.ProjectFeatureEnabled(r.Context(), project.ID, "jsw.classic.roadmap")
	if err != nil {
		http.Error(w, "Could not load the timeline.", http.StatusInternalServerError)
		return nil, false
	}
	if !enabled {
		http.NotFound(w, r)
		return nil, false
	}
	return project, true
}

// ProjectTimeline renders the project's epics and child work across months.
func (h *Handler) ProjectTimeline(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	project, ok := h.timelineProject(w, r, workspaceID)
	if !ok {
		return
	}
	timeline, err := h.Store.ProjectTimeline(r.Context(), workspaceID, user.ID, project.ID)
	if err != nil {
		http.Error(w, "Could not load the timeline.", http.StatusInternalServerError)
		return
	}
	data := newTimelineData(project, timeline, time.Now(), h.siteLook(r, workspaceID).DateDay)
	data.Notice, data.Error = r.URL.Query().Get("notice"), r.URL.Query().Get("error")
	h.writeWorkspacePage(w, r, "page_project_timeline", user, workspaceID, data, "timeline", project.ID)
}

// ScheduleTimelineItem sets a work item's start and due dates from the timeline.
func (h *Handler) ScheduleTimelineItem(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	project, ok := h.timelineProject(w, r, workspaceID)
	if !ok {
		return
	}
	back := func(key, value string) {
		http.Redirect(w, r, "/projects/"+url.PathEscape(project.Key)+"/timeline?"+url.Values{key: {value}}.Encode(), http.StatusSeeOther)
	}
	key := strings.TrimSpace(r.PostFormValue("issue"))
	start, due := strings.TrimSpace(r.PostFormValue("startDate")), strings.TrimSpace(r.PostFormValue("dueDate"))
	if startDay, ok := parseTimelineDay(start); ok {
		if dueDay, ok := parseTimelineDay(due); ok && dueDay.Before(startDay) {
			back("error", "The due date cannot be before the start date.")
			return
		}
	}
	fieldID, err := h.Store.StartDateFieldID(r.Context(), workspaceID)
	if err != nil {
		http.Error(w, "Could not load the timeline.", http.StatusInternalServerError)
		return
	}
	in := commands.UpdateIssueInput{ActorID: user.ID, WorkspaceID: workspaceID, IssueIDOrKey: key, DueDate: &due}
	if fieldID != "" {
		value := json.RawMessage("null")
		if start != "" {
			encoded, _ := json.Marshal(start)
			value = encoded
		}
		in.Fields = map[string]json.RawMessage{fieldID: value}
	} else if start != "" {
		back("error", "This site has no Start date field.")
		return
	}
	issue, err := h.Store.IssueByIDOrKey(r.Context(), workspaceID, key)
	if err != nil || issue.ProjectID != project.ID {
		back("error", "Choose work from this project.")
		return
	}
	if _, _, err := h.Commands.UpdateIssue(r.Context(), in); err != nil {
		message := err.Error()
		if errors.Is(err, commands.ErrIssueNotEditable) {
			message = "This work item cannot be edited in its current status."
		}
		back("error", message)
		return
	}
	back("notice", fmt.Sprintf("%s scheduled.", issue.Key))
}

type planListRow struct {
	Plan store.Plan
	Edit bool
}

type plansPageData struct {
	Plans []planListRow
	// Sources are what a new plan can read, so a plan is created where plans
	// are listed rather than only over REST. Creating one is site
	// administration, as the REST resource is.
	Sources     []planSourceChoice
	Estimations []string
	CanCreate   bool
	Error       string
}

// PlansPage lists the active plans the user can view.
func (h *Handler) PlansPage(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	plans, _, err := h.Store.Plans(r.Context(), workspaceID, false, false, 0, 100)
	if err != nil {
		http.Error(w, "Could not load plans.", http.StatusInternalServerError)
		return
	}
	data := plansPageData{Error: r.URL.Query().Get("error"), Estimations: planEstimations}
	if data.CanCreate, err = h.Store.IsAdmin(r.Context(), workspaceID, user.ID); err != nil {
		http.Error(w, "Could not load plans.", http.StatusInternalServerError)
		return
	}
	if data.CanCreate {
		if data.Sources, err = h.planSourceChoices(r, workspaceID, user.ID, store.Plan{}); err != nil {
			http.Error(w, "Could not load what a plan can read.", http.StatusInternalServerError)
			return
		}
	}
	for _, plan := range plans {
		if plan.Status != "" && plan.Status != "Active" {
			continue
		}
		view, edit, err := h.Store.PlanAccess(r.Context(), workspaceID, user.ID, plan)
		if err != nil {
			http.Error(w, "Could not load plans.", http.StatusInternalServerError)
			return
		}
		if view {
			data.Plans = append(data.Plans, planListRow{Plan: plan, Edit: edit})
		}
	}
	h.writeWorkspacePage(w, r, "page_plans", user, workspaceID, data, "plans", "")
}
