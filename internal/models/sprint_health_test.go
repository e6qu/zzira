package models

import (
	"testing"
	"time"
)

func TestSprintDaysRemaining(t *testing.T) {
	sprint := Sprint{EndDate: "2026-09-20T00:00:00Z"}
	if days, overdue, ok := SprintDaysRemaining(sprint, time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)); !ok || overdue || days != 8 {
		t.Fatalf("remaining = %d, %v, %v", days, overdue, ok)
	}
	if days, overdue, ok := SprintDaysRemaining(sprint, time.Date(2026, 9, 21, 1, 0, 0, 0, time.UTC)); !ok || !overdue || days != 2 {
		t.Fatalf("overdue = %d, %v, %v", days, overdue, ok)
	}
	if _, _, ok := SprintDaysRemaining(Sprint{}, time.Now()); ok {
		t.Fatal("a sprint without an end date has days remaining")
	}
}

func TestNewSprintHealth(t *testing.T) {
	report := SprintReport{
		Sprint: Sprint{EndDate: "2026-09-20T00:00:00Z"}, Start: "2026-09-10T00:00:00Z", Statistic: "Story point estimate",
		CompletedTotal: 3, NotCompletedTotal: 9,
		Completed:    []SprintReportIssue{{Key: "A"}},
		NotCompleted: []SprintReportIssue{{Key: "B"}, {Key: "C", AddedAfterStart: true}},
		Removed:      []SprintReportIssue{{Key: "D"}},
	}
	health := NewSprintHealth(report, time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC))
	// A quarter of the time and estimate; one added and one removed against three committed.
	if want := (SprintHealth{Elapsed: 25, HasEnd: true, Complete: 25, ScopeChange: 67, Committed: 3, Added: 1, Removed: 1, Statistic: "Story point estimate"}); health != want {
		t.Fatalf("health = %+v, want %+v", health, want)
	}
	report.CompletedTotal, report.NotCompletedTotal = 0, 0
	if health := NewSprintHealth(report, time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)); health.Complete != 33 || health.Elapsed != 100 {
		t.Fatalf("unestimated or finished health = %+v", health)
	}
	report.Sprint.EndDate = ""
	if health := NewSprintHealth(report, time.Now()); health.HasEnd || health.Elapsed != 0 {
		t.Fatalf("health without an end = %+v", health)
	}
	if health := NewSprintHealth(SprintReport{NotCompleted: []SprintReportIssue{{AddedAfterStart: true}}}, time.Now()); health.ScopeChange != 100 || health.Committed != 0 {
		t.Fatalf("health with nothing committed = %+v", health)
	}
}
