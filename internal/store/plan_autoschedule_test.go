package store

import (
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/models"
)

// A plan whose work is ranked but unscheduled: the scheduler fills each
// team's sprints in rank order, as far as capacity reaches, and puts blocked
// work after what blocks it.
func TestPlanAutoScheduleFillsSprintsInRankOrder(t *testing.T) {
	estimate := func(value float64) *float64 { return &value }
	item := func(key string, teamID int64, points *float64) *PlanItem {
		return &PlanItem{Issue: &models.Issue{ID: key, Key: key, Status: models.Status{Category: "new"}}, TeamID: teamID, Estimate: points, Changed: map[string]bool{}}
	}
	epic := &PlanItem{Issue: &models.Issue{ID: "EPIC-1", Key: "EPIC-1", Status: models.Status{Category: "new"}}, TeamID: 1, Changed: map[string]bool{}}
	one, two, three := item("PL-1", 1, estimate(5)), item("PL-2", 1, estimate(5)), item("PL-3", 1, estimate(3))
	planning := PlanPlanning{
		Unit:  "story points",
		Items: map[string]*PlanItem{epic.Issue.ID: epic, one.Issue.ID: one, two.Issue.ID: two, three.Issue.ID: three},
		Work: PlanWork{Items: []models.TimelineItem{{
			Issue: epic.Issue,
			Children: []models.TimelineItem{
				{Issue: one.Issue}, {Issue: two.Issue}, {Issue: three.Issue},
			},
		}}},
		Capacity: []PlanTeamCapacity{{
			Team: PlanTeam{ID: 1, PlanningStyle: "Scrum"}, Name: "Platform",
			Iterations: []PlanIteration{
				{Key: "sprint-1", Name: "Sprint 1", Start: "2026-01-05", End: "2026-01-16", Capacity: 8},
				{Key: "sprint-2", Name: "Sprint 2", Start: "2026-01-19", End: "2026-01-30", Capacity: 8},
			},
		}},
	}
	from := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	result := planAutoSchedule(planning, true, PlanScheduleRequest{From: from})
	if result.Scheduled != 3 || len(result.Skipped) != 0 {
		t.Fatalf("scheduled %d, skipped %+v", result.Scheduled, result.Skipped)
	}
	// Five points then three fit the first sprint; the second five spills into
	// the next one. An epic takes no place of its own: its bar is the work
	// beneath it.
	want := map[string][2]string{"PL-1": {"sprint-1", "2026-01-05"}, "PL-3": {"sprint-1", "2026-01-05"}, "PL-2": {"sprint-2", "2026-01-19"}}
	got := map[string][2]string{}
	for _, change := range result.Changes {
		if change.IssueKey == "EPIC-1" {
			t.Fatalf("the epic was scheduled: %+v", change)
		}
		entry := got[change.IssueKey]
		switch change.Field {
		case "sprint":
			entry[0] = string(change.Value[1 : len(change.Value)-1])
		case "startDate":
			entry[1] = string(change.Value[1 : len(change.Value)-1])
		}
		got[change.IssueKey] = entry
	}
	for key, expected := range want {
		if got[key] != expected {
			t.Fatalf("%s = %v, want %v (changes %+v)", key, got[key], expected, result.Changes)
		}
	}

	// Blocked work waits for a later sprint when the plan's dependencies are
	// sequential, even though the first sprint has room for it.
	planning.Capacity[0].Iterations[0].Capacity = 20
	blocked := item("PL-4", 1, estimate(1))
	planning.Items[blocked.Issue.ID] = blocked
	planning.Work.Items[0].Children = append(planning.Work.Items[0].Children, models.TimelineItem{Issue: blocked.Issue})
	planning.Dependencies = []PlanDependency{{Blocker: three, Blocked: blocked}}
	result = planAutoSchedule(planning, true, PlanScheduleRequest{From: from})
	for _, change := range result.Changes {
		if change.IssueKey == "PL-4" && change.Field == "sprint" && string(change.Value) != `"sprint-2"` {
			t.Fatalf("blocked work went into %s", change.Value)
		}
	}
	// Concurrently, it may share the sprint with what blocks it.
	result = planAutoSchedule(planning, false, PlanScheduleRequest{From: from})
	for _, change := range result.Changes {
		if change.IssueKey == "PL-4" && change.Field == "sprint" && string(change.Value) != `"sprint-1"` {
			t.Fatalf("concurrent dependencies put it in %s", change.Value)
		}
	}
}

// What the scheduler cannot place it says, rather than leaving it out
// silently: work with no team, a team with no iterations, and work that fits
// nowhere left.
func TestPlanAutoScheduleSaysWhatItCouldNotPlace(t *testing.T) {
	estimate := func(value float64) *float64 { return &value }
	homeless := &PlanItem{Issue: &models.Issue{ID: "PL-9", Key: "PL-9", Status: models.Status{Category: "new"}}, Estimate: estimate(2), Changed: map[string]bool{}}
	stranded := &PlanItem{Issue: &models.Issue{ID: "PL-8", Key: "PL-8", Status: models.Status{Category: "new"}}, TeamID: 2, Estimate: estimate(2), Changed: map[string]bool{}}
	huge := &PlanItem{Issue: &models.Issue{ID: "PL-7", Key: "PL-7", Status: models.Status{Category: "new"}}, TeamID: 1, Estimate: estimate(40), Changed: map[string]bool{}}
	after := &PlanItem{Issue: &models.Issue{ID: "PL-6", Key: "PL-6", Status: models.Status{Category: "new"}}, TeamID: 1, Estimate: estimate(1), Changed: map[string]bool{}}
	planning := PlanPlanning{
		Unit:  "story points",
		Items: map[string]*PlanItem{"PL-9": homeless, "PL-8": stranded, "PL-7": huge, "PL-6": after},
		Work: PlanWork{Items: []models.TimelineItem{
			{Issue: homeless.Issue}, {Issue: stranded.Issue}, {Issue: huge.Issue}, {Issue: after.Issue},
		}},
		Capacity: []PlanTeamCapacity{
			{Team: PlanTeam{ID: 1, PlanningStyle: "Scrum"}, Name: "Platform", Iterations: []PlanIteration{
				{Key: "sprint-1", Start: "2026-01-05", End: "2026-01-16", Capacity: 8},
			}},
			{Team: PlanTeam{ID: 2, PlanningStyle: "Scrum"}, Name: "Payments", Note: "The team's board has no active or future sprints."},
		},
	}
	result := planAutoSchedule(planning, true, PlanScheduleRequest{From: time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)})
	reasons := map[string]string{}
	for _, skip := range result.Skipped {
		reasons[skip.Key] = skip.Reason
	}
	if reasons["PL-9"] != "it has no team" {
		t.Fatalf("work with no team: %q", reasons["PL-9"])
	}
	if reasons["PL-8"] != "The team's board has no active or future sprints." {
		t.Fatalf("a team with no sprints: %q", reasons["PL-8"])
	}
	// Work bigger than a whole sprint still starts somewhere, and fills it, so
	// the next item finds no room at all.
	if reasons["PL-7"] != "" {
		t.Fatalf("work bigger than a sprint: %q", reasons["PL-7"])
	}
	if reasons["PL-6"] == "" {
		t.Fatal("work that fits nowhere was not reported")
	}
}

// A run that plans only what has nothing planned leaves the rest alone; one
// that plans everything moves it.
func TestPlanAutoScheduleLeavesPlannedWorkAlone(t *testing.T) {
	estimate := func(value float64) *float64 { return &value }
	planned := &PlanItem{Issue: &models.Issue{ID: "PL-1", Key: "PL-1", Status: models.Status{Category: "new"}}, TeamID: 1, SprintID: "sprint-2", Estimate: estimate(3), Changed: map[string]bool{}}
	fresh := &PlanItem{Issue: &models.Issue{ID: "PL-2", Key: "PL-2", Status: models.Status{Category: "new"}}, TeamID: 1, Estimate: estimate(3), Changed: map[string]bool{}}
	planning := PlanPlanning{
		Unit:  "story points",
		Items: map[string]*PlanItem{"PL-1": planned, "PL-2": fresh},
		Work: PlanWork{Items: []models.TimelineItem{
			{Issue: planned.Issue, StartDate: "2026-01-19", DueDate: "2026-01-30"}, {Issue: fresh.Issue},
		}},
		Capacity: []PlanTeamCapacity{{Team: PlanTeam{ID: 1, PlanningStyle: "Scrum"}, Name: "Platform", Iterations: []PlanIteration{
			{Key: "sprint-1", Start: "2026-01-05", End: "2026-01-16", Capacity: 8},
			{Key: "sprint-2", Start: "2026-01-19", End: "2026-01-30", Capacity: 8, Planned: 3},
		}}},
	}
	from := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	result := planAutoSchedule(planning, true, PlanScheduleRequest{From: from})
	for _, change := range result.Changes {
		if change.IssueKey == "PL-1" {
			t.Fatalf("planned work was moved: %+v", change)
		}
	}
	if result.Scheduled != 1 {
		t.Fatalf("scheduled = %d", result.Scheduled)
	}
	again := planAutoSchedule(planning, true, PlanScheduleRequest{All: true, From: from})
	if again.Scheduled != 2 {
		t.Fatalf("scheduling everything = %d", again.Scheduled)
	}
	moved := false
	for _, change := range again.Changes {
		if change.IssueKey == "PL-1" && change.Field == "sprint" && string(change.Value) == `"sprint-1"` {
			moved = true
		}
	}
	if !moved {
		t.Fatalf("scheduling everything left the planned work where it was: %+v", again.Changes)
	}
}
