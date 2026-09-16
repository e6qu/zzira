package web

import (
	"math"
	"time"

	"github.com/e6qu/zzira/internal/store"
)

// activityView is an activity stream event with its time for people.
type activityView struct {
	store.ActivityEntry
	When, WhenLabel string
}

func newActivityViews(entries []store.ActivityEntry, layout string) []activityView {
	views := make([]activityView, 0, len(entries))
	for _, entry := range entries {
		views = append(views, activityView{ActivityEntry: entry, When: entry.At.UTC().Format(time.RFC3339), WhenLabel: entry.At.UTC().Format(layout)})
	}
	return views
}

type calendarWeekday struct{ Name, Short string }

// calendarDay is one day of the calendar gadget's month.
type calendarDay struct {
	Day              int
	Today            bool
	Issues, Versions []store.CalendarEntry
}

// calendarView lays a month out in weeks starting on Monday; days outside the
// month are nil.
type calendarView struct {
	Title    string
	Weekdays []calendarWeekday
	Weeks    [][]*calendarDay
	More     int
	Empty    bool
}

var calendarWeekdays = []calendarWeekday{{"Monday", "Mon"}, {"Tuesday", "Tue"}, {"Wednesday", "Wed"}, {"Thursday", "Thu"}, {"Friday", "Fri"}, {"Saturday", "Sat"}, {"Sunday", "Sun"}}

func newCalendarView(calendar *store.GadgetCalendar, now time.Time) *calendarView {
	first := calendar.Month
	view := &calendarView{Title: first.Format("January 2006"), Weekdays: calendarWeekdays, More: calendar.More, Empty: len(calendar.Issues) == 0 && len(calendar.Versions) == 0}
	cells := make([]*calendarDay, (int(first.Weekday())+6)%7)
	byDate := map[string]*calendarDay{}
	today := now.UTC().Format("2006-01-02")
	for date := first; date.Month() == first.Month(); date = date.AddDate(0, 0, 1) {
		day := &calendarDay{Day: date.Day(), Today: date.Format("2006-01-02") == today}
		byDate[date.Format("2006-01-02")] = day
		cells = append(cells, day)
	}
	for len(cells)%7 != 0 {
		cells = append(cells, nil)
	}
	for _, entry := range calendar.Issues {
		if day := byDate[entry.Date]; day != nil {
			day.Issues = append(day.Issues, entry)
		}
	}
	for _, entry := range calendar.Versions {
		if day := byDate[entry.Date]; day != nil {
			day.Versions = append(day.Versions, entry)
		}
	}
	for start := 0; start < len(cells); start += 7 {
		view.Weeks = append(view.Weeks, cells[start:start+7])
	}
	return view
}

// roadMapRow is a version on the road map gadget.
type roadMapRow struct {
	Name, ReleaseLabel   string
	Overdue              bool
	Done, Total, Maximum int
}

type roadMapView struct {
	Rows []roadMapRow
}

func newRoadMapView(versions []store.RoadMapVersion, layout string) *roadMapView {
	view := &roadMapView{}
	for _, version := range versions {
		row := roadMapRow{Name: version.Version.Name, ReleaseLabel: displayDay(version.Version.ReleaseDate, layout), Overdue: version.Overdue, Done: version.Progress.Done, Total: version.Total, Maximum: max(version.Total, 1)}
		view.Rows = append(view.Rows, row)
	}
	return view
}

// bubblePoint is work drawn on the bubble chart.
type bubblePoint struct {
	store.BubbleIssue
	X, Y, R float64
	// Shade darkens work updated more recently, from 1 to 4.
	Shade int
}

// bubbleView plots work by days since its last update across, participants
// or votes up, and the other as the bubble's size.
type bubbleView struct {
	Width, Height, Left, Right, Top, Bottom float64
	Points                                  []bubblePoint
	VerticalName, SizeName                  string
	MaxDays, MaxVertical                    int
}

func newBubbleView(bubbles []store.BubbleIssue, axis string) *bubbleView {
	view := &bubbleView{Width: 640, Height: 260, Left: 40, Right: 610, Top: 24, Bottom: 230, VerticalName: "Participants", SizeName: "Votes"}
	if axis == "votes" {
		view.VerticalName, view.SizeName = "Votes", "Participants"
	}
	measures := func(bubble store.BubbleIssue) (int, int) {
		if axis == "votes" {
			return bubble.Votes, bubble.Participants
		}
		return bubble.Participants, bubble.Votes
	}
	maxSize := 0
	for _, bubble := range bubbles {
		vertical, size := measures(bubble)
		view.MaxDays, view.MaxVertical, maxSize = max(view.MaxDays, bubble.UpdatedDays), max(view.MaxVertical, vertical), max(maxSize, size)
	}
	span := func(value, maximum int, from, to float64) float64 {
		if maximum == 0 {
			return from
		}
		return math.Round((from+(to-from)*float64(value)/float64(maximum))*10) / 10
	}
	for _, bubble := range bubbles {
		vertical, size := measures(bubble)
		point := bubblePoint{BubbleIssue: bubble, X: span(bubble.UpdatedDays, view.MaxDays, view.Left, view.Right), Y: span(vertical, view.MaxVertical, view.Bottom, view.Top), R: span(size, maxSize, 6, 20)}
		switch {
		case bubble.UpdatedDays <= 1:
			point.Shade = 4
		case bubble.UpdatedDays <= 7:
			point.Shade = 3
		case bubble.UpdatedDays <= 30:
			point.Shade = 2
		default:
			point.Shade = 1
		}
		view.Points = append(view.Points, point)
	}
	return view
}
