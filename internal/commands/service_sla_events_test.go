package commands

import (
	"slices"
	"testing"

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
