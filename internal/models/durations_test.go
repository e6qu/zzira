package models

import "testing"

func TestJiraDurations(t *testing.T) {
	cfg := TimeTrackingConfiguration{DefaultUnit: "minute", TimeFormat: "pretty", WorkingHoursPerDay: 8, WorkingDaysPerWeek: 5}
	for input, want := range map[string]int64{"1w 2d 3h 30m": 5*8*3600 + 2*8*3600 + 3*3600 + 1800, "90": 5400, "1.5h": 5400, "2d": 16 * 3600} {
		got, err := ParseJiraDuration(input, cfg)
		if err != nil || got != want {
			t.Fatalf("ParseJiraDuration(%q) = %d, %v; want %d", input, got, err, want)
		}
	}
	for _, bad := range []string{"", "3x", "1h 2h", "soon"} {
		if _, err := ParseJiraDuration(bad, cfg); err == nil {
			t.Fatalf("ParseJiraDuration(%q) accepted", bad)
		}
	}
	if got := FormatJiraDuration(5*8*3600+2*8*3600+3*3600+1800, cfg); got != "1w 2d 3h 30m" {
		t.Fatalf("pretty = %q", got)
	}
	cfg.TimeFormat = "hours"
	if got := FormatJiraDuration(5400, cfg); got != "1.5h" {
		t.Fatalf("hours = %q", got)
	}
	cfg.TimeFormat, cfg.DefaultUnit = "days", "hour"
	if got := FormatJiraDuration(12*3600, cfg); got != "1.5d" {
		t.Fatalf("days = %q", got)
	}
	if got, _ := ParseJiraDuration("4", cfg); got != 4*3600 {
		t.Fatalf("default hour unit = %d", got)
	}
}
