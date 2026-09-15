// Package cron reads Quartz cron expressions, the schedule syntax Jira
// Automation's scheduled trigger uses, and finds when they next fire.
package cron

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Schedule is a parsed Quartz cron expression: seconds, minutes, hours, day
// of month, month, day of week and an optional year.
type Schedule struct {
	second                     int
	minutes, hours             uint64
	daysOfMonth, months, weeks uint64
	years                      map[int]bool
	// byDayOfMonth is true when the day of week is "?", so days are chosen by
	// day of month; otherwise by day of week.
	byDayOfMonth bool
}

var (
	monthNames = map[string]int{"JAN": 1, "FEB": 2, "MAR": 3, "APR": 4, "MAY": 5, "JUN": 6, "JUL": 7, "AUG": 8, "SEP": 9, "OCT": 10, "NOV": 11, "DEC": 12}
	dayNames   = map[string]int{"SUN": 1, "MON": 2, "TUE": 3, "WED": 4, "THU": 5, "FRI": 6, "SAT": 7}
)

// Parse reads a Quartz cron expression. Exactly one of the day-of-month and
// day-of-week fields must be "?", and the seconds field must be one number so
// a schedule fires at most once a minute. L, W and # are not supported.
func Parse(expression string) (*Schedule, error) {
	fields := strings.Fields(expression)
	if len(fields) != 6 && len(fields) != 7 {
		return nil, errors.New("a cron expression has 6 or 7 fields: seconds, minutes, hours, day of month, month, day of week and an optional year")
	}
	if strings.ContainsAny(strings.ToUpper(expression), "LW#") && !containsOnlyNames(fields) {
		return nil, errors.New("L, W and # are not supported in cron expressions")
	}
	second, err := strconv.Atoi(fields[0])
	if err != nil || second < 0 || second > 59 {
		return nil, errors.New("the seconds field must be one number from 0 to 59")
	}
	schedule := &Schedule{second: second}
	if schedule.minutes, err = parseField(fields[1], 0, 59, nil); err != nil {
		return nil, fmt.Errorf("minutes: %w", err)
	}
	if schedule.hours, err = parseField(fields[2], 0, 23, nil); err != nil {
		return nil, fmt.Errorf("hours: %w", err)
	}
	dayOfMonthUnset, dayOfWeekUnset := fields[3] == "?", fields[5] == "?"
	if dayOfMonthUnset == dayOfWeekUnset {
		return nil, errors.New("exactly one of the day-of-month and day-of-week fields must be ?")
	}
	schedule.byDayOfMonth = dayOfWeekUnset
	if !dayOfMonthUnset {
		if schedule.daysOfMonth, err = parseField(fields[3], 1, 31, nil); err != nil {
			return nil, fmt.Errorf("day of month: %w", err)
		}
	}
	if schedule.months, err = parseField(fields[4], 1, 12, monthNames); err != nil {
		return nil, fmt.Errorf("month: %w", err)
	}
	if !dayOfWeekUnset {
		if schedule.weeks, err = parseField(fields[5], 1, 7, dayNames); err != nil {
			return nil, fmt.Errorf("day of week: %w", err)
		}
	}
	if len(fields) == 7 && fields[6] != "*" {
		bits, err := parseYears(fields[6])
		if err != nil {
			return nil, fmt.Errorf("year: %w", err)
		}
		schedule.years = bits
	}
	return schedule, nil
}

// containsOnlyNames reports whether every L or W in the expression belongs to
// a month or day name, such as JUL or WED.
func containsOnlyNames(fields []string) bool {
	for index, field := range fields {
		upper := strings.ToUpper(field)
		if strings.Contains(upper, "#") {
			return false
		}
		names := map[string]int{}
		switch index {
		case 4:
			names = monthNames
		case 5:
			names = dayNames
		}
		for name := range names {
			upper = strings.ReplaceAll(upper, name, "")
		}
		if strings.ContainsAny(upper, "LW") {
			return false
		}
	}
	return true
}

func parseValue(text string, names map[string]int) (int, error) {
	if value, ok := names[strings.ToUpper(text)]; ok {
		return value, nil
	}
	value, err := strconv.Atoi(text)
	if err != nil {
		return 0, fmt.Errorf("%q is not a number or name", text)
	}
	return value, nil
}

// parseField reads a list of values, ranges and steps into a bit set.
func parseField(field string, low, high int, names map[string]int) (uint64, error) {
	var bits uint64
	for _, part := range strings.Split(field, ",") {
		from, to, step, err := parseRange(part, low, high, names)
		if err != nil {
			return 0, err
		}
		for value := from; value <= to; value += step {
			bits |= 1 << uint(value)
		}
	}
	return bits, nil
}

func parseRange(part string, low, high int, names map[string]int) (int, int, int, error) {
	rangeText, stepText, stepped := strings.Cut(part, "/")
	step := 1
	if stepped {
		var err error
		if step, err = strconv.Atoi(stepText); err != nil || step < 1 {
			return 0, 0, 0, fmt.Errorf("%q has an invalid step", part)
		}
	}
	from, to := low, high
	switch {
	case rangeText == "*":
	case strings.Contains(rangeText, "-"):
		first, last, _ := strings.Cut(rangeText, "-")
		var err error
		if from, err = parseValue(first, names); err != nil {
			return 0, 0, 0, err
		}
		if to, err = parseValue(last, names); err != nil {
			return 0, 0, 0, err
		}
	default:
		value, err := parseValue(rangeText, names)
		if err != nil {
			return 0, 0, 0, err
		}
		from, to = value, value
		if stepped {
			to = high
		}
	}
	if from < low || to > high || from > to {
		return 0, 0, 0, fmt.Errorf("%q must be within %d to %d", part, low, high)
	}
	return from, to, step, nil
}

func parseYears(field string) (map[int]bool, error) {
	years := map[int]bool{}
	for _, part := range strings.Split(field, ",") {
		from, to, step, err := parseRange(part, 1970, 2099, nil)
		if err != nil {
			return nil, err
		}
		for year := from; year <= to; year += step {
			years[year] = true
		}
	}
	return years, nil
}

// searchDays bounds how far ahead Next looks for a matching day.
const searchDays = 366 * 5

// Next is the first time after the given instant when the schedule fires, in
// the location's local time. Local times skipped by a daylight-saving change
// do not fire. ok is false when nothing fires within five years.
func (s *Schedule) Next(after time.Time, location *time.Location) (time.Time, bool) {
	local := after.In(location)
	start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location)
	for offset := 0; offset < searchDays; offset++ {
		year, month, day := start.AddDate(0, 0, offset).Date()
		if s.years != nil && !s.years[year] || s.months&(1<<uint(month)) == 0 {
			continue
		}
		weekday := time.Date(year, month, day, 12, 0, 0, 0, location).Weekday()
		if s.byDayOfMonth && s.daysOfMonth&(1<<uint(day)) == 0 || !s.byDayOfMonth && s.weeks&(1<<uint(weekday+1)) == 0 {
			continue
		}
		for hour := 0; hour < 24; hour++ {
			if s.hours&(1<<uint(hour)) == 0 {
				continue
			}
			for minute := 0; minute < 60; minute++ {
				if s.minutes&(1<<uint(minute)) == 0 {
					continue
				}
				candidate := time.Date(year, month, day, hour, minute, s.second, 0, location)
				if candidate.Day() != day || candidate.Hour() != hour || candidate.Minute() != minute {
					continue
				}
				if candidate.After(after) {
					return candidate, true
				}
			}
		}
	}
	return time.Time{}, false
}
