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
