package web

import (
	"math"
	"strings"
	"time"

	"github.com/e6qu/zzira/internal/models"
)

// dayBar is one day's bar in a gadget chart, with an optional second segment
// stacked on the first.
type dayBar struct {
	X, Width                                 float64
	LowerY, LowerHeight, UpperY, UpperHeight float64
}

// dayBars lays out one bar per day of a report window.
type dayBars struct {
	Width, Height float64
	Bars          []dayBar
}

func newDayBars(lower, upper []float64) dayBars {
	const top, left, right, bottom = 12.0, 8.0, 632.0, 220.0
	view := dayBars{Width: 640, Height: 228}
	if len(lower) == 0 {
		return view
	}
	maximum := 0.0
	for index := range lower {
		maximum = math.Max(maximum, lower[index]+upper[index])
	}
	maximum = chartScale(maximum)
	slot := (right - left) / float64(len(lower))
	width := math.Round(math.Max(slot*0.7, 1)*10) / 10
	scale := func(value float64) float64 { return math.Round(value/maximum*(bottom-top)*10) / 10 }
	for index := range lower {
		lowerHeight, upperHeight := scale(lower[index]), scale(upper[index])
		view.Bars = append(view.Bars, dayBar{
			X: math.Round((left+float64(index)*slot+(slot-width)/2)*10) / 10, Width: width,
			LowerY: bottom - lowerHeight, LowerHeight: lowerHeight,
			UpperY: bottom - lowerHeight - upperHeight, UpperHeight: upperHeight,
		})
	}
	return view
}

type recentlyCreatedRow struct {
	Date                 string
	Resolved, Unresolved int
}

// recentlyCreatedView stacks each day's created work, resolved beneath
// unresolved.
type recentlyCreatedView struct {
	models.RecentlyCreatedReport
	Chart dayBars
	Rows  []recentlyCreatedRow
}

func newRecentlyCreatedView(report models.RecentlyCreatedReport, layout string) *recentlyCreatedView {
	view := &recentlyCreatedView{RecentlyCreatedReport: report}
	resolved, unresolved := []float64{}, []float64{}
	for _, day := range report.Days {
		resolved, unresolved = append(resolved, float64(day.Resolved)), append(unresolved, float64(day.Unresolved))
		view.Rows = append(view.Rows, recentlyCreatedRow{Date: displayDay(day.Date, layout), Resolved: day.Resolved, Unresolved: day.Unresolved})
	}
	view.Chart = newDayBars(resolved, unresolved)
	return view
}

type averageAgeRow struct {
	Date, Average string
	Unresolved    int
}

// averageAgeView draws the average age of unresolved work at each day's end.
type averageAgeView struct {
	Chart      dayBars
	Rows       []averageAgeRow
	Unresolved int
	Average    string
}

func newAverageAgeView(report models.AverageAgeReport, layout string) *averageAgeView {
	view := &averageAgeView{Average: "-"}
	hours, none := []float64{}, []float64{}
	for _, day := range report.Days {
		row := averageAgeRow{Date: displayDay(day.Date, layout), Unresolved: day.Unresolved, Average: "-"}
		if day.Unresolved > 0 {
			row.Average = cycleDuration(day.AverageSeconds)
		}
		hours, none = append(hours, float64(day.AverageSeconds)/3600), append(none, 0)
		view.Rows = append(view.Rows, row)
	}
	if count := len(view.Rows); count > 0 {
		view.Unresolved, view.Average = view.Rows[count-1].Unresolved, view.Rows[count-1].Average
	}
	view.Chart = newDayBars(hours, none)
	return view
}

type timeSinceRow struct {
	Date  string
	Count int
}

// timeSinceView draws how much work's chosen date fell on each day.
type timeSinceView struct {
	models.TimeSinceReport
	FieldName, Verb string
	Chart           dayBars
	Rows            []timeSinceRow
}

func newTimeSinceView(report models.TimeSinceReport, layout string) *timeSinceView {
	name := models.TimeSinceFieldName(report.Field)
	view := &timeSinceView{TimeSinceReport: report, FieldName: name, Verb: strings.ToLower(name)}
	counts, none := []float64{}, []float64{}
	for _, day := range report.Days {
		counts, none = append(counts, float64(day.Count)), append(none, 0)
		view.Rows = append(view.Rows, timeSinceRow{Date: displayDay(day.Date, layout), Count: day.Count})
	}
	view.Chart = newDayBars(counts, none)
	return view
}

// daysRemainingView is how long is left in a sprint.
type daysRemainingView struct {
	Days            int
	Overdue, HasEnd bool
	EndLabel        string
}

func newDaysRemainingView(sprint *models.Sprint, now time.Time, layout string) *daysRemainingView {
	view := &daysRemainingView{}
	view.Days, view.Overdue, view.HasEnd = models.SprintDaysRemaining(*sprint, now)
	if end, err := time.Parse(time.RFC3339, sprint.EndDate); err == nil {
		view.EndLabel = end.UTC().Format(layout)
	}
	return view
}
