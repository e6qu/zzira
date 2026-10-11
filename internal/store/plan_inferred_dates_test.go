package store

import (
	"encoding/json"
	"github.com/e6qu/zzira/internal/models"
	"testing"
)

func TestInferredPlanDatesPreserveExplicitAndUnavailableDates(t *testing.T) {
	item := models.TimelineItem{StartDate: "2026-11-05"}
	fillPlanDates(&item, "2026-11-02T10:00:00Z", "2026-11-20", "Sprint dates")
	if item.StartDate != "2026-11-05" || item.InferredStart != "" || item.DueDate != "2026-11-20" || item.InferredEnd != "Sprint dates" {
		t.Fatalf("explicit date overwritten: %+v", item)
	}
	absent := models.TimelineItem{}
	fillPlanDates(&absent, "", "2026-02-30", "Release dates")
	if absent.StartDate != "" || absent.DueDate != "" || absent.InferredStart != "" || absent.InferredEnd != "" {
		t.Fatalf("invented dates: %+v", absent)
	}
	for _, field := range []string{"startDate", "endDate"} {
		if string(planScheduleCurrent(&PlanItem{}, &models.TimelineItem{StartDate: "2026-11-02", DueDate: "2026-11-20", InferredStart: "Release dates", InferredEnd: "Release dates"}, field)) != "null" {
			t.Fatalf("scheduler treated inferred %s as stored", field)
		}
	}
}

func TestInferredPlanReleaseDatesStayWithinTheIssueProject(t *testing.T) {
	item := models.TimelineItem{Issue: &models.Issue{ProjectID: "own", Fields: map[string]json.RawMessage{"fixVersions": json.RawMessage(`[{"id":"1"},{"id":"2"},{"id":"foreign"},{"id":"gone"}]`)}}}
	inferPlanReleaseDates(&item, map[string]planReleaseDates{"own/1": {Start: "2026-11-05", End: "2026-11-20"}, "own/2": {Start: "2026-11-02", End: "2026-11-27"}, "other/foreign": {Start: "2026-01-01", End: "2027-01-01"}})
	if item.StartDate != "2026-11-02" || item.DueDate != "2026-11-27" || item.InferredStart != "Release dates" || item.InferredEnd != "Release dates" {
		t.Fatalf("release projection crossed scope: %+v", item)
	}
}

func TestAutoSchedulingTurnsAnInferredSpanIntoExplicitDrafts(t *testing.T) {
	issue := &models.Issue{ID: "inferred-work", Key: "IN-1", Status: models.Status{Category: "new"}}
	item := &PlanItem{Issue: issue, TeamID: 1, SprintID: "sprint-1", Changed: map[string]bool{}}
	planning := PlanPlanning{Items: map[string]*PlanItem{issue.ID: item}, Work: PlanWork{Items: []models.TimelineItem{{Issue: issue, StartDate: "2026-11-02", DueDate: "2026-11-13", InferredStart: "Sprint dates", InferredEnd: "Sprint dates"}}}, Capacity: []PlanTeamCapacity{{Team: PlanTeam{ID: 1, PlanningStyle: "Scrum"}, Iterations: []PlanIteration{{Key: "sprint-1", Start: "2026-11-02", End: "2026-11-13", Capacity: 10}}}}}
	result := planAutoSchedule(planning, true, PlanScheduleRequest{All: true})
	if result.Scheduled != 1 || len(result.Changes) != 2 {
		t.Fatalf("matching inferred span did not create explicit dates: %+v", result)
	}
	for _, change := range result.Changes {
		if (change.Field != "startDate" && change.Field != "endDate") || string(change.Current) != "null" {
			t.Fatalf("wrong inferred baseline: %+v", change)
		}
	}
}
