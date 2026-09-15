package commands

import (
	"slices"
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/models"
)

func TestServiceSLAEvents(t *testing.T) {
	ana, bea := &models.User{ID: "usr_ana"}, &models.User{ID: "usr_bea"}
	done := &models.Resolution{ID: "res_done"}
	issue := func(change func(*models.Issue)) *models.Issue {
		value := &models.Issue{Status: models.Status{ID: "st_todo"}}
		change(value)
		return value
	}
	same := func(*models.Issue) {}
	for _, check := range []struct {
		name          string
		before, after func(*models.Issue)
		want          []string
	}{
		{"nothing changed", same, same, []string{}},
		{"entered a status", same, func(i *models.Issue) { i.Status.ID = "st_progress" }, []string{"entered_status:st_progress"}},
		{"assigned from unassigned", same, func(i *models.Issue) { i.Assignee = ana }, []string{models.SLAConditionAssigneeFromUnassigned, models.SLAConditionAssigneeChanged}},
		{"unassigned", func(i *models.Issue) { i.Assignee = ana }, same, []string{models.SLAConditionAssigneeToUnassigned, models.SLAConditionAssigneeChanged}},
		{"reassigned", func(i *models.Issue) { i.Assignee = ana }, func(i *models.Issue) { i.Assignee = bea }, []string{models.SLAConditionAssigneeChanged}},
		{"due date set", same, func(i *models.Issue) { i.DueDate = "2026-10-01" }, []string{models.SLAConditionDueDateSet, models.SLAConditionDueDateChanged}},
		{"due date moved", func(i *models.Issue) { i.DueDate = "2026-10-01" }, func(i *models.Issue) { i.DueDate = "2026-10-02" }, []string{models.SLAConditionDueDateChanged}},
		{"due date cleared", func(i *models.Issue) { i.DueDate = "2026-10-01" }, same, []string{models.SLAConditionDueDateCleared, models.SLAConditionDueDateChanged}},
		{"resolved", same, func(i *models.Issue) { i.Status.ID = "st_done"; i.Resolution = done }, []string{"entered_status:st_done", models.SLAConditionResolutionSet}},
		{"reopened", func(i *models.Issue) { i.Resolution = done }, same, []string{models.SLAConditionResolutionCleared}},
	} {
		if got := serviceSLAEvents(issue(check.before), issue(check.after)); !slices.Equal(got, check.want) {
			t.Fatalf("%s: events = %v, want %v", check.name, got, check.want)
		}
	}
}

func TestServiceSLASpansReplayHistory(t *testing.T) {
	at := func(minutes int) time.Time { return time.Date(2026, 9, 16, 9, minutes, 0, 0, time.UTC) }
	history := []serviceSLAChange{
		{At: at(0), Events: []string{models.SLAConditionIssueCreated}},
		{At: at(5), Events: []string{models.SLAConditionCommentByCustomer}},
		{At: at(10), Events: []string{"entered_status:st_done", models.SLAConditionResolutionSet}},
		{At: at(20), Events: []string{models.SLAConditionResolutionCleared}},
		{At: at(30), Events: []string{models.SLAConditionCommentForCustomers}},
	}
	resolution := serviceSLASpans([]string{models.SLAConditionIssueCreated, models.SLAConditionResolutionCleared}, []string{models.SLAConditionResolutionSet}, history)
	if len(resolution) != 2 || !resolution[0].Start.Equal(at(0)) || resolution[0].Stop == nil || !resolution[0].Stop.Equal(at(10)) || !resolution[1].Start.Equal(at(20)) || resolution[1].Stop != nil {
		t.Fatalf("resolution spans = %+v", resolution)
	}
	response := serviceSLASpans([]string{models.SLAConditionIssueCreated}, []string{models.SLAConditionCommentForCustomers}, history)
	if len(response) != 1 || response[0].Stop == nil || !response[0].Stop.Equal(at(30)) {
		t.Fatalf("response spans = %+v", response)
	}
	if never := serviceSLASpans([]string{models.SLAConditionDueDateSet}, []string{models.SLAConditionResolutionSet}, history); len(never) != 0 {
		t.Fatalf("an SLA started without its start condition: %+v", never)
	}
}

func TestServiceSLAStatusPauseReplay(t *testing.T) {
	at := func(minutes int) time.Time { return time.Date(2026, 9, 16, 9, minutes, 0, 0, time.UTC) }
	history := []serviceSLAChange{
		{At: at(0), Status: "Open"},
		{At: at(10), Status: "Waiting for customer"},
		{At: at(25), Status: "In Progress"},
		{At: at(40), Status: "Waiting for customer"},
	}
	paused, ok := serviceSLAStatusPredicate(`status = "Waiting for customer"`)
	if !ok {
		t.Fatal("a status pause condition could not be replayed")
	}
	intervals := serviceSLAPauseIntervals(paused, history, "Waiting")
	if len(intervals) != 2 || !intervals[0].Start.Equal(at(10)) || intervals[0].Stop == nil || !intervals[0].Stop.Equal(at(25)) {
		t.Fatalf("pause intervals = %+v", intervals)
	}
	if !intervals[1].Start.Equal(at(40)) || intervals[1].Stop != nil || intervals[1].Reason != "Waiting" {
		t.Fatalf("the pause the request is still in = %+v", intervals[1])
	}
	// A request that never waits is never paused, and one created waiting is
	// paused from the moment it arrives.
	if none := serviceSLAPauseIntervals(paused, history[:1], "Waiting"); len(none) != 0 {
		t.Fatalf("an unwaiting request was paused: %+v", none)
	}
	if born := serviceSLAPauseIntervals(paused, []serviceSLAChange{{At: at(0), Status: "Waiting for customer"}}, "Waiting"); len(born) != 1 || !born[0].Start.Equal(at(0)) || born[0].Stop != nil {
		t.Fatalf("intervals of a request created waiting = %+v", born)
	}

	for _, check := range []struct {
		query  string
		status string
		want   bool
	}{
		{`status = "Waiting for customer"`, "Waiting for customer", true},
		{`status = "waiting for CUSTOMER"`, "Waiting for customer", true},
		{`status != "Waiting for customer"`, "Waiting for customer", false},
		{`status in ("Waiting for customer", "Waiting for approval")`, "Waiting for approval", true},
		{`status in ("Waiting for customer", "Waiting for approval")`, "In Progress", false},
		{`status not in ("Waiting for customer")`, "In Progress", true},
		{`status = "Waiting for customer" OR status = "Pending"`, "Pending", true},
		{`status != "Open" AND status != "In Progress"`, "In Progress", false},
		{`NOT status = "Open"`, "Open", false},
	} {
		holds, ok := serviceSLAStatusPredicate(check.query)
		if !ok {
			t.Fatalf("%s could not be replayed", check.query)
		}
		if holds(check.status) != check.want {
			t.Fatalf("%s in %q = %v, want %v", check.query, check.status, holds(check.status), check.want)
		}
	}

	// Conditions that ask about anything else, or about a status in a way a
	// history does not answer, are left to the live reconciliation.
	for _, query := range []string{
		`assignee IS EMPTY`,
		`status ~ "Waiting"`,
		`status = "Waiting for customer" AND assignee IS EMPTY`,
		`status IS EMPTY`,
		`"Time to resolution" = running()`,
		`text ~ "waiting"`,
		`status = "Waiting for customer" ORDER BY created`,
		`status =`,
	} {
		if _, ok := serviceSLAStatusPredicate(query); ok {
			t.Fatalf("%s was replayed as a status condition", query)
		}
	}
}
