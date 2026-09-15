package cron

import (
	"testing"
	"time"
)

func TestNextFiresAtQuartzSchedules(t *testing.T) {
	bucharest, err := time.LoadLocation("Europe/Bucharest")
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		expression string
		location   *time.Location
		after      time.Time
		want       time.Time
	}{
		{"0 0 9 ? * MON-FRI", bucharest, time.Date(2026, 9, 18, 10, 0, 0, 0, bucharest), time.Date(2026, 9, 21, 9, 0, 0, 0, bucharest)},
		{"0 30 */6 * * ?", time.UTC, time.Date(2026, 9, 15, 5, 0, 0, 0, time.UTC), time.Date(2026, 9, 15, 6, 30, 0, 0, time.UTC)},
		{"0 0 12 1,15 * ?", time.UTC, time.Date(2026, 9, 15, 13, 0, 0, 0, time.UTC), time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)},
		{"0 15 10 ? jan,JUL 2 2027", time.UTC, time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC), time.Date(2027, 1, 4, 10, 15, 0, 0, time.UTC)},
		{"15 0/20 8-9 * * ? *", time.UTC, time.Date(2026, 9, 15, 8, 40, 15, 0, time.UTC), time.Date(2026, 9, 15, 9, 0, 15, 0, time.UTC)},
		// 03:30 does not exist when Bucharest springs forward on 28 March 2027.
		{"0 30 3 * * ?", bucharest, time.Date(2027, 3, 27, 4, 0, 0, 0, bucharest), time.Date(2027, 3, 29, 3, 30, 0, 0, bucharest)},
		{"0 0 0 29 FEB ?", time.UTC, time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), time.Date(2028, 2, 29, 0, 0, 0, 0, time.UTC)},
	} {
		schedule, err := Parse(check.expression)
		if err != nil {
			t.Fatalf("%s: %v", check.expression, err)
		}
		got, ok := schedule.Next(check.after, check.location)
		if !ok || !got.Equal(check.want) {
			t.Fatalf("%s after %s = %s, %v; want %s", check.expression, check.after, got, ok, check.want)
		}
	}
	past, err := Parse("0 0 0 1 1 ? 2020")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := past.Next(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.UTC); ok {
		t.Fatal("a schedule in the past fired")
	}
}

func TestParseRefusesUnsupportedExpressions(t *testing.T) {
	for _, expression := range []string{
		"", "0 9 * * *", "0 0 9 * * *", "0 0 9 ? * ?", "0 0 24 * * ?", "0 0 9 L * ?", "0 0 9 15W * ?",
		"*/5 0 9 * * ?", "0 0 9 * * ? 1969", "0 0 9 ? * MON#2", "0 0 9 ? * FUNDAY", "0 0 9 ? * 6L", "0 0 9 10-5 * ?", "0 0/0 9 * * ?",
	} {
		if _, err := Parse(expression); err == nil {
			t.Fatalf("%q was accepted", expression)
		}
	}
}
