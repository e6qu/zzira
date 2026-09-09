package store

import (
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/models"
)

func TestServiceCalendarBusinessTime(t *testing.T) {
	location := time.UTC
	calendar := &models.ServiceCalendar{
		TimeZone: "UTC", Weekdays: []int16{1, 2, 3, 4, 5},
		StartMinute: 9 * 60, EndMinute: 17 * 60, Holidays: map[string]string{},
	}
	monday := time.Date(2026, time.September, 7, 16, 0, 0, 0, location)
	tuesday := time.Date(2026, time.September, 8, 10, 0, 0, 0, location)
	if got := serviceBusinessDuration(calendar, monday, tuesday, location); got != 2*time.Hour {
		t.Fatalf("business duration = %v", got)
	}
	if got := serviceBreachTime(calendar, monday, 2*time.Hour, location); !got.Equal(tuesday) {
		t.Fatalf("breach time = %v, want %v", got, tuesday)
	}
	cycle := calculateServiceSLACycle(calendar, location, "cycle", monday, nil, (2 * time.Hour).Milliseconds(), tuesday)
	if !cycle.Breached || cycle.ElapsedMillis != (2*time.Hour).Milliseconds() || cycle.RemainingMillis != 0 {
		t.Fatalf("cycle at deadline = %+v", cycle)
	}
	friday := time.Date(2026, time.September, 11, 16, 0, 0, 0, location)
	mondayMorning := time.Date(2026, time.September, 14, 10, 0, 0, 0, location)
	if got := serviceBreachTime(calendar, friday, 2*time.Hour, location); !got.Equal(mondayMorning) {
		t.Fatalf("weekend breach time = %v, want %v", got, mondayMorning)
	}
	calendar.Holidays["2026-09-14"] = "Holiday"
	tuesdayMorning := time.Date(2026, time.September, 15, 10, 0, 0, 0, location)
	if got := serviceBreachTime(calendar, friday, 2*time.Hour, location); !got.Equal(tuesdayMorning) {
		t.Fatalf("holiday breach time = %v, want %v", got, tuesdayMorning)
	}
}

func TestServiceSLACycleSubtractsDurablePauseIntervals(t *testing.T) {
	calendar := &models.ServiceCalendar{
		TimeZone: "UTC", Weekdays: []int16{1, 2, 3, 4, 5},
		StartMinute: 9 * 60, EndMinute: 17 * 60, Holidays: map[string]string{},
	}
	start := time.Date(2026, time.September, 7, 16, 0, 0, 0, time.UTC)
	pauseStart := time.Date(2026, time.September, 7, 16, 30, 0, 0, time.UTC)
	pauseStop := time.Date(2026, time.September, 8, 9, 30, 0, 0, time.UTC)
	end := time.Date(2026, time.September, 8, 11, 0, 0, 0, time.UTC)
	cycle := calculateServiceSLACycleWithPauses(calendar, time.UTC, "cycle", start, nil, (2 * time.Hour).Milliseconds(), end, []models.ServiceSLAPause{{StartTime: pauseStart, StopTime: &pauseStop}})
	if cycle.Paused || !cycle.Breached || cycle.ElapsedMillis != (2*time.Hour).Milliseconds() || !cycle.BreachTime.Equal(end) {
		t.Fatalf("cycle with closed pause = %+v", cycle)
	}
	active := calculateServiceSLACycleWithPauses(calendar, time.UTC, "cycle", start, nil, (2 * time.Hour).Milliseconds(), pauseStop, []models.ServiceSLAPause{{StartTime: pauseStart}})
	if !active.Paused || active.Breached || active.ElapsedMillis != (30*time.Minute).Milliseconds() || !active.BreachTime.Equal(end) {
		t.Fatalf("cycle with active pause = %+v", active)
	}
}
