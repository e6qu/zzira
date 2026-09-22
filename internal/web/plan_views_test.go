package web

import (
	"testing"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

// TestRollingUpAPlanReadsTheWorkUnderEachParent is what a plan shows when it
// rolls up: a parent spans its children and carries their estimates, whatever
// its own fields say, while its own dates stay where an edit would find them.
func TestRollingUpAPlanReadsTheWorkUnderEachParent(t *testing.T) {
	estimate := func(value float64) *float64 { return &value }
	items := []models.TimelineItem{{
		Issue:     &models.Issue{ID: "epic", Key: "PL-1"},
		StartDate: "2026-04-01",
		Children: []models.TimelineItem{
			{Issue: &models.Issue{ID: "story-1", Key: "PL-2"}, StartDate: "2026-03-02", DueDate: "2026-03-20"},
			{Issue: &models.Issue{ID: "story-2", Key: "PL-3"}, StartDate: "2026-05-05", DueDate: "2026-06-30", Children: []models.TimelineItem{
				{Issue: &models.Issue{ID: "task", Key: "PL-4"}, DueDate: "2026-07-31"},
			}},
		},
	}}
	planned := map[string]*store.PlanItem{
		"epic":    {Estimate: estimate(2)},
		"story-1": {Estimate: estimate(5)},
		"story-2": {Estimate: estimate(8)},
		"task":    {},
	}
	spans, totals := planRollUp(items, planned)
	if spans["epic"] != [2]string{"2026-03-02", "2026-07-31"} {
		t.Fatalf("the epic spans %v", spans["epic"])
	}
	if spans["story-2"] != [2]string{"2026-05-05", "2026-07-31"} {
		t.Fatalf("the story spans %v", spans["story-2"])
	}
	if totals["epic"] != 15 || totals["story-2"] != 8 || totals["story-1"] != 5 {
		t.Fatalf("totals = %v", totals)
	}
	// A leaf with nothing under it is itself.
	if spans["story-1"] != [2]string{"2026-03-02", "2026-03-20"} {
		t.Fatalf("the leaf spans %v", spans["story-1"])
	}
	// The tree the timeline draws carries the spans; the tree the forms read
	// is the one that was passed in, unchanged.
	rolled := rolledTimeline(items, spans)
	if rolled[0].StartDate != "2026-03-02" || rolled[0].DueDate != "2026-07-31" {
		t.Fatalf("the drawn epic = %s to %s", rolled[0].StartDate, rolled[0].DueDate)
	}
	if items[0].StartDate != "2026-04-01" || items[0].DueDate != "" {
		t.Fatalf("rolling up changed the work item itself: %s to %s", items[0].StartDate, items[0].DueDate)
	}
}
