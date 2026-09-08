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
