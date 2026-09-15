package web

import (
	"strings"
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/models"
)

func TestBurndownChartStepsRemainingWorkAgainstTheGuideline(t *testing.T) {
	report := models.SprintReport{
		Sprint: models.Sprint{State: "closed", EndDate: "2026-09-11T00:00:00Z"},
		Start:  "2026-09-01T00:00:00Z", End: "2026-09-11T00:00:00Z",
		Burndown: []models.SprintBurndownEvent{
			{At: "2026-09-01T00:00:00Z", Label: "Sprint start", Remaining: 10, Scope: 10},
			{At: "2026-09-06T00:00:00Z", Label: "Work completed", Change: -4, Remaining: 6, Scope: 10},
			{At: "2026-09-11T00:00:00Z", Label: "Sprint completed", Remaining: 6, Scope: 10},
		},
	}
	chart := newBurndownChart(report, "2006-01-02")
	// Ten days span 576 px from x=48; y runs from 220 (zero) to 16 (ten).
	if chart.Remaining != "48,16 336,16 336,97.6 624,97.6 624,97.6 624,97.6" {
		t.Fatalf("remaining = %q", chart.Remaining)
	}
	if !chart.Guideline || chart.GuideX1 != 48 || chart.GuideY1 != 16 || chart.GuideX2 != 624 || chart.GuideY2 != 220 {
		t.Fatalf("guideline = %+v", chart)
	}
	if len(chart.Ticks) != 3 || chart.Ticks[2].Label != "10" || chart.Ticks[1].Label != "5" || chart.StartLabel != "2026-09-01" || chart.EndLabel != "2026-09-11" {
		t.Fatalf("axis = %+v", chart)
	}

	view := newSprintReportView(report, siteDateLayouts{day: "2006-01-02", complete: "2006-01-02 15:04"})
	if len(view.Sections) != 4 || view.Events[1].ChangeDisplay != "-4" || view.Events[1].When != "2026-09-06 00:00" {
		t.Fatalf("view = %+v", view)
	}
}

func TestVelocityChartScalesBarsAndShortensLabels(t *testing.T) {
	view := newVelocityReportView(models.VelocityReport{Statistic: "Story point estimate", Sprints: []models.VelocitySprint{
		{Sprint: models.Sprint{Name: "Platform sprint twelve"}, Commitment: 20, Completed: 10},
		{Sprint: models.Sprint{Name: "Short"}, Commitment: 8, Completed: 8},
	}})
	if view.AverageCompleted != "9" || len(view.Bars) != 2 {
		t.Fatalf("velocity = %+v", view)
	}
	first := view.Bars[0]
	if first.CommitmentHeight != 204 || first.CommitmentY != 16 || first.CompletedHeight != 102 || first.CompletedY != 118 {
		t.Fatalf("first bar = %+v", first)
	}
	if first.Label != "Platform spri…" || view.Bars[1].Label != "Short" || !strings.HasPrefix(first.Name, "Platform sprint") {
		t.Fatalf("labels = %+v", view.Bars)
	}
}

func TestBurnupAndFlowChartsLayOutTheirSeries(t *testing.T) {
	report := models.SprintReport{
		Sprint: models.Sprint{EndDate: "2026-09-11T00:00:00Z"},
		Start:  "2026-09-01T00:00:00Z", End: "2026-09-11T00:00:00Z",
		Burndown: []models.SprintBurndownEvent{
			{At: "2026-09-01T00:00:00Z", Remaining: 10, Scope: 10},
			{At: "2026-09-06T00:00:00Z", Remaining: 6, Scope: 10},
		},
	}
	burnup := newBurnupChart(report, "2006-01-02")
	if burnup.Scope != "48,16 336,16 336,16 624,16" || burnup.Completed != "48,220 336,220 336,138.4 624,138.4" {
		t.Fatalf("burnup = %+v", burnup)
	}

	flow := newCumulativeFlowView(models.CumulativeFlow{
		Columns: []models.FlowColumn{{StatusID: "st_todo", Name: "To Do"}, {StatusID: "st_done", Name: "Done"}},
		Days:    []models.FlowDay{{Date: "2026-09-01", Counts: []int{2, 0}}, {Date: "2026-09-02", Counts: []int{1, 3}}},
	}, "02/Jan/06")
	// Four items at most; To Do stacks on top of Done.
	if len(flow.Bands) != 2 || flow.Bands[0].Points != "48,118 624,16 624,67 48,220" || flow.Bands[1].Points != "48,220 624,67 624,220 48,220" {
		t.Fatalf("bands = %+v", flow.Bands)
	}
	if flow.StartLabel != "01/Sep/26" || len(flow.Rows) != 2 || flow.Rows[1].Counts[1] != 3 {
		t.Fatalf("flow rows = %+v", flow)
	}

	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	control := newControlChartView(models.ControlChart{
		Samples:        []models.CycleSample{{Key: "ZZ-1", CompletedAt: "2026-09-13T12:00:00Z", CycleSeconds: 30 * 3600}, {Key: "ZZ-2", CompletedAt: "2026-09-14T00:00:00Z", CycleSeconds: 90 * 60}},
		AverageSeconds: 15*3600 + 45*60, MedianSeconds: 15*3600 + 45*60,
	}, 2, now, "2006-01-02", "2006-01-02 15:04")
	if len(control.Points) != 2 || control.Points[0].CycleDuration != "1d 6h" || control.Points[1].CycleDuration != "1h" || control.Average != "15h" {
		t.Fatalf("control = %+v", control)
	}
	if control.Points[0].Y != 16 || control.StartLabel != "2026-09-13" || control.Ticks[2].Label != "1d 6h" {
		t.Fatalf("control geometry = %+v", control)
	}
	if cycleDuration(20*60) != "20m" || cycleDuration(48*3600) != "2d" {
		t.Fatal("cycle durations")
	}
}

func TestProgressReportDrawsTotalAndCompletedByDay(t *testing.T) {
	view := newProgressReportView(models.ProgressReport{
		Statistic: "Story point estimate",
		Points:    []models.ProgressPoint{{Date: "2026-09-01", Total: 3}, {Date: "2026-09-02", Total: 8, Completed: 3}, {Date: "2026-09-03", Total: 8, Completed: 8}},
		Completed: []models.SprintReportIssue{{Key: "ZZ-1"}}, Incomplete: []models.SprintReportIssue{{Key: "ZZ-2"}},
		TotalEstimate: 8, CompletedEstimate: 3, Progress: models.VersionProgress{Done: 1, ToDo: 1},
	}, "02/Jan/06")
	if view.Chart.Total != "48,143.5 336,16 624,16" || view.Chart.Completed != "48,220 336,143.5 624,16" {
		t.Fatalf("chart = %+v", view.Chart)
	}
	if view.Percent != 50 || view.RemainingText != "5" || len(view.Sections) != 2 || view.Chart.StartLabel != "01/Sep/26" {
		t.Fatalf("view = %+v", view)
	}
}

func TestIssueAnalysisChartsDrawDailyAndRunningSeries(t *testing.T) {
	report := models.CreatedResolvedReport{Days: []models.CreatedResolvedDay{
		{Date: "2026-09-10", Created: 2, Resolved: 1, CreatedTotal: 2, ResolvedTotal: 1},
		{Date: "2026-09-11", Created: 1, Resolved: 3, CreatedTotal: 3, ResolvedTotal: 4},
	}, CreatedTotal: 3, ResolvedTotal: 4}
	daily := newCreatedResolvedView(report, false, "02/Jan/06")
	if daily.Created != "48,84 624,152" || daily.Resolved != "48,152 624,16" || daily.StartLabel != "10/Sep/26" {
		t.Fatalf("daily = %+v", daily)
	}
	running := newCreatedResolvedView(report, true, "02/Jan/06")
	if running.Created != "48,118 624,67" || running.Resolved != "48,169 624,16" {
		t.Fatalf("running = %+v", running)
	}

	resolution := newResolutionTimeView(models.ResolutionTimeReport{Days: []models.ResolutionDay{
		{Date: "2026-09-10", Resolved: 2, AverageSeconds: 6 * 3600},
		{Date: "2026-09-11"},
	}, Resolved: 2, AverageSeconds: 6 * 3600}, "2006-01-02")
	if len(resolution.Bars) != 2 || resolution.Bars[0].Height != 204 || resolution.Bars[0].Average != "6h" || resolution.Bars[1].Average != "" || resolution.BarWidth != 198.8 || resolution.Average != "6h" {
		t.Fatalf("resolution = %+v", resolution)
	}
}
