package automation

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/cron"
)

func TestCronScheduledRulesQueueAtTheirTimes(t *testing.T) {
	fx := newAutomationFixture(t)
	actions := []map[string]any{{"component": "ACTION", "type": "jira.issue.add-label", "value": map[string]string{"label": "weekday"}}}
	cronBody := func(name, expression string) []byte {
		body := ruleBody(name, fx.admin, "ENABLED", "", actions)
		body["rule"].(map[string]any)["trigger"] = map[string]any{"component": "TRIGGER", "type": "jira.jql.scheduled", "schemaVersion": 1,
			"value": map[string]any{"timezone": "Europe/Bucharest", "jql": "", "schedule": map[string]any{"method": "CRON_EXPRESSION", "cronExpression": expression}}}
		raw, _ := json.Marshal(body)
		return raw
	}
	for _, invalid := range []string{"0 0 9 * * MON", "0 0 9 L * ?", "* 0 9 ? * *"} {
		if _, err := fx.service.CreateRule(fx.ctx, fx.ws, fx.admin, cronBody("Invalid "+invalid, invalid)); err == nil || !strings.Contains(err.Error(), "cron expression") {
			t.Fatalf("%q: %v", invalid, err)
		}
	}
	schedule, err := cron.Parse("0 0 9 ? * MON-FRI")
	if err != nil {
		t.Fatal(err)
	}
	bucharest, err := time.LoadLocation("Europe/Bucharest")
	if err != nil {
		t.Fatal(err)
	}
	weekdayNine := func(at *time.Time) bool {
		if at == nil {
			return false
		}
		local := at.In(bucharest)
		return local.Hour() == 9 && local.Minute() == 0 && local.Weekday() != time.Saturday && local.Weekday() != time.Sunday && at.After(time.Now())
	}
	before := time.Now()
	uuid, err := fx.service.CreateRule(fx.ctx, fx.ws, fx.admin, cronBody("Weekday mornings", "0 0 9 ? * MON-FRI"))
	if err != nil {
		t.Fatal(err)
	}
	rule, err := fx.service.Rule(fx.ctx, fx.ws, uuid)
	want, _ := schedule.Next(before, bucharest)
	if err != nil || rule.CronExpression != "0 0 9 ? * MON-FRI" || rule.IntervalMinutes != nil || rule.NextRunAt == nil || !rule.NextRunAt.Equal(want) {
		t.Fatalf("cron rule = %+v, %v; want next %s", rule, err, want)
	}

	// A due rule queues one run for its time and moves to the next time.
	due := time.Now().Add(-time.Minute).UTC().Truncate(time.Second)
	if _, err := fx.store.Pool.Exec(fx.ctx, `UPDATE automation_rules SET next_run_at=$2 WHERE uuid=$1`, uuid, due); err != nil {
		t.Fatal(err)
	}
	runner := &Runner{Service: fx.service}
	for range 2 {
		if err := runner.enqueueDue(fx.ctx, fx.ws); err != nil {
			t.Fatal(err)
		}
	}
	var queued, total int
	if err := fx.store.Pool.QueryRow(fx.ctx, `SELECT count(*) FILTER (WHERE scheduled_for=$2), count(*) FROM automation_runs WHERE rule_uuid=$1`, uuid, due).Scan(&queued, &total); err != nil || queued != 1 || total != 1 {
		t.Fatalf("queued runs = %d of %d, %v", queued, total, err)
	}
	if rule, err = fx.service.Rule(fx.ctx, fx.ws, uuid); err != nil || !weekdayNine(rule.NextRunAt) {
		t.Fatalf("next run after queueing = %v, %v", rule.NextRunAt, err)
	}

	// Disabling clears the schedule and enabling computes the next time again.
	if err := fx.service.SetState(fx.ctx, fx.ws, uuid, "DISABLED"); err != nil {
		t.Fatal(err)
	}
	if rule, _ = fx.service.Rule(fx.ctx, fx.ws, uuid); rule.NextRunAt != nil {
		t.Fatalf("disabled rule next run = %v", rule.NextRunAt)
	}
	if err := fx.service.SetState(fx.ctx, fx.ws, uuid, "ENABLED"); err != nil {
		t.Fatal(err)
	}
	if rule, _ = fx.service.Rule(fx.ctx, fx.ws, uuid); !weekdayNine(rule.NextRunAt) {
		t.Fatalf("re-enabled rule next run = %v", rule.NextRunAt)
	}

	// Switching to a fixed interval clears the cron expression and its time.
	interval, _ := json.Marshal(ruleBody("Weekday mornings", fx.admin, "ENABLED", "", actions))
	if err := fx.service.UpdateRule(fx.ctx, fx.ws, fx.admin, uuid, interval); err != nil {
		t.Fatal(err)
	}
	rule, err = fx.service.Rule(fx.ctx, fx.ws, uuid)
	if err != nil || rule.CronExpression != "" || rule.IntervalMinutes == nil || *rule.IntervalMinutes != 60 || rule.NextRunAt == nil || rule.NextRunAt.After(time.Now().Add(61*time.Minute)) {
		t.Fatalf("interval rule = %+v, %v", rule, err)
	}
}
