package models

import (
	"math"
	"time"
)

// SprintDaysRemaining is how many days are left before a sprint's planned
// end, or how many days it is overdue. ok is false without an end date.
func SprintDaysRemaining(sprint Sprint, now time.Time) (days int, overdue, ok bool) {
	end, err := time.Parse(time.RFC3339, sprint.EndDate)
	if err != nil {
		return 0, false, false
	}
	left := end.Sub(now)
	if left < 0 {
		return int(math.Ceil(-left.Hours() / 24)), true, true
	}
	return int(math.Ceil(left.Hours() / 24)), false, true
}

// SprintHealth is Jira's sprint health gadget: how much of an active sprint's
// time has passed, how much of its work is complete, and how much its scope
// changed after it started.
type SprintHealth struct {
	// Elapsed is the percentage of the planned sprint that has passed; HasEnd
	// is false when the sprint has no end date.
	Elapsed int
	HasEnd  bool
	// Complete is the percentage of the sprint's work complete, by the
	// board's estimation statistic or, without estimates, by work items.
	Complete int
	// ScopeChange is the work added or removed after the start, as a
	// percentage of the work committed at the start.
	ScopeChange               int
	Committed, Added, Removed int
	Statistic                 string
}

// NewSprintHealth measures a sprint's health from its sprint report.
func NewSprintHealth(report SprintReport, now time.Time) SprintHealth {
	health := SprintHealth{Statistic: report.Statistic}
	start, startErr := time.Parse(time.RFC3339, report.Start)
	if end, err := time.Parse(time.RFC3339, report.Sprint.EndDate); err == nil && startErr == nil && end.After(start) {
		health.HasEnd = true
		health.Elapsed = int(math.Round(math.Min(math.Max(now.Sub(start).Seconds()/end.Sub(start).Seconds(), 0), 1) * 100))
	}
	if total := report.CompletedTotal + report.NotCompletedTotal; total > 0 {
		health.Complete = int(math.Round(report.CompletedTotal / total * 100))
	} else if count := len(report.Completed) + len(report.NotCompleted); count > 0 {
		health.Complete = int(math.Round(float64(len(report.Completed)) / float64(count) * 100))
	}
	changed := 0
	for _, list := range [][]SprintReportIssue{report.Completed, report.NotCompleted, report.Removed} {
		for _, issue := range list {
			if issue.AddedAfterStart {
				health.Added++
				changed++
			} else {
				health.Committed++
			}
		}
	}
	for _, issue := range report.Removed {
		health.Removed++
		if !issue.AddedAfterStart {
			changed++
		}
	}
	switch {
	case health.Committed > 0:
		health.ScopeChange = int(math.Round(float64(changed) / float64(health.Committed) * 100))
	case changed > 0:
		health.ScopeChange = 100
	}
	return health
}
