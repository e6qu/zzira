package models

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

var durationPart = regexp.MustCompile(`^(\d+(?:\.\d+)?)([wdhm]?)$`)

// durationUnits is how many seconds one week, day, hour and minute of work
// last under the site's working time.
func durationUnits(cfg TimeTrackingConfiguration) map[string]float64 {
	hoursPerDay, daysPerWeek := cfg.WorkingHoursPerDay, cfg.WorkingDaysPerWeek
	if hoursPerDay <= 0 {
		hoursPerDay = 8
	}
	if daysPerWeek <= 0 {
		daysPerWeek = 5
	}
	return map[string]float64{"w": daysPerWeek * hoursPerDay * 3600, "d": hoursPerDay * 3600, "h": 3600, "m": 60}
}

// ParseJiraDuration reads an estimate the way Jira does: weeks, days, hours and
// minutes such as "1w 2d 3h 30m", measured in the site's working time. A bare
// number is in the site's default unit.
func ParseJiraDuration(input string, cfg TimeTrackingConfiguration) (int64, error) {
	input = strings.ToLower(strings.TrimSpace(input))
	if input == "" {
		return 0, fmt.Errorf("an estimate is required")
	}
	units := durationUnits(cfg)
	defaultUnit := map[string]string{"minute": "m", "hour": "h", "day": "d", "week": "w"}[cfg.DefaultUnit]
	if defaultUnit == "" {
		defaultUnit = "m"
	}
	total := 0.0
	seen := map[string]bool{}
	for _, part := range strings.Fields(input) {
		match := durationPart.FindStringSubmatch(part)
		if match == nil {
			return 0, fmt.Errorf("%q is not a valid duration; use weeks, days, hours and minutes such as 1w 2d 3h 30m", input)
		}
		unit := match[2]
		if unit == "" {
			unit = defaultUnit
		}
		if seen[unit] {
			return 0, fmt.Errorf("%q names the same unit twice", input)
		}
		seen[unit] = true
		value, _ := strconv.ParseFloat(match[1], 64)
		total += value * units[unit]
	}
	if total > math.MaxInt32*60 {
		return 0, fmt.Errorf("%q is too long an estimate", input)
	}
	return int64(math.Round(total/60)) * 60, nil
}

// FormatJiraDuration writes seconds as Jira displays durations under the
// site's time format: "pretty" as "1w 2d 3h 30m", "days" as "2.5d" and
// "hours" as "20.5h".
func FormatJiraDuration(seconds int64, cfg TimeTrackingConfiguration) string {
	units := durationUnits(cfg)
	switch cfg.TimeFormat {
	case "days":
		return trimDecimal(float64(seconds)/units["d"]) + "d"
	case "hours":
		return trimDecimal(float64(seconds)/units["h"]) + "h"
	}
	if seconds <= 0 {
		return "0m"
	}
	parts := []string{}
	remaining := float64(seconds)
	for _, unit := range []string{"w", "d", "h", "m"} {
		count := math.Floor(remaining / units[unit])
		if count > 0 {
			parts = append(parts, strconv.FormatFloat(count, 'f', 0, 64)+unit)
			remaining -= count * units[unit]
		}
	}
	if len(parts) == 0 {
		return "0m"
	}
	return strings.Join(parts, " ")
}

func trimDecimal(value float64) string {
	return strconv.FormatFloat(math.Round(value*100)/100, 'f', -1, 64)
}

// TimeTrackingView is a work item's time tracking as the issue page shows it.
type TimeTrackingView struct {
	Original, Remaining, Spent string
	// Estimated is set when either estimate is; SpentPercent and
	// RemainingPercent split the bar between logged and remaining time.
	Estimated                      bool
	SpentPercent, RemainingPercent int
}

// NewTimeTrackingView formats an issue's estimates and logged time.
func NewTimeTrackingView(issue Issue, cfg TimeTrackingConfiguration) TimeTrackingView {
	view := TimeTrackingView{Spent: FormatJiraDuration(issue.TimeSpentSeconds, cfg)}
	if issue.OriginalEstimateSeconds != nil {
		view.Original, view.Estimated = FormatJiraDuration(*issue.OriginalEstimateSeconds, cfg), true
	}
	remaining := int64(0)
	if issue.RemainingEstimateSeconds != nil {
		remaining = *issue.RemainingEstimateSeconds
		view.Remaining, view.Estimated = FormatJiraDuration(remaining, cfg), true
	}
	if total := issue.TimeSpentSeconds + remaining; total > 0 {
		view.SpentPercent = int(issue.TimeSpentSeconds * 100 / total)
		view.RemainingPercent = 100 - view.SpentPercent
	}
	return view
}
