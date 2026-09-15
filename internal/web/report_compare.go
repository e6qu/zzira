package web

import (
	"fmt"
	"math"
	"net/http"
	"time"
)

// wantsComparison reports whether a report should compare its window with the
// same length of time just before it.
func wantsComparison(r *http.Request) bool {
	return r.URL.Query().Get("compare") == "previous"
}

// previousPeriod is the moment a report's previous period ends: its window's
// length before now.
func previousPeriod(now time.Time, days int) time.Time {
	return now.UTC().AddDate(0, 0, -days)
}

// withPeriods labels a compared report's CSV rows with their period, the
// previous period first.
func withPeriods(header []string, previous, current [][]string) ([]string, [][]string) {
	rows := make([][]string, 0, len(previous)+len(current))
	for _, row := range previous {
		rows = append(rows, append([]string{"Previous period"}, row...))
	}
	for _, row := range current {
		rows = append(rows, append([]string{"Current period"}, row...))
	}
	return append([]string{"Period"}, header...), rows
}

// compareCount describes a count against the previous period.
func compareCount(current, previous, days int) string {
	switch {
	case current > previous:
		return fmt.Sprintf("Up %d from %d in the previous %d days", current-previous, previous, days)
	case current < previous:
		return fmt.Sprintf("Down %d from %d in the previous %d days", previous-current, previous, days)
	default:
		return fmt.Sprintf("Same as the previous %d days", days)
	}
}

// compareDuration describes a time taken against the previous period; zero
// means there was nothing to measure.
func compareDuration(current, previous int64, days int) string {
	switch {
	case previous == 0:
		return fmt.Sprintf("Nothing to measure in the previous %d days", days)
	case current == 0:
		return fmt.Sprintf("Was %s in the previous %d days", cycleDuration(previous), days)
	case current > previous:
		return fmt.Sprintf("Longer by %s than %s in the previous %d days", cycleDuration(current-previous), cycleDuration(previous), days)
	case current < previous:
		return fmt.Sprintf("Shorter by %s than %s in the previous %d days", cycleDuration(previous-current), cycleDuration(previous), days)
	default:
		return fmt.Sprintf("Same as the previous %d days", days)
	}
}

// compareRate describes a percentage, or an average rating, against the
// previous period, which had samples samples.
func compareRate(current, previous float64, samples, days int, unit string) string {
	if samples == 0 {
		return fmt.Sprintf("Nothing to measure in the previous %d days", days)
	}
	change := math.Round((current-previous)*10) / 10
	switch {
	case change > 0:
		return fmt.Sprintf("Up %.1f%s from %.1f%s in the previous %d days", change, unit, previous, unit, days)
	case change < 0:
		return fmt.Sprintf("Down %.1f%s from %.1f%s in the previous %d days", -change, unit, previous, unit, days)
	default:
		return fmt.Sprintf("Same as the previous %d days", days)
	}
}
