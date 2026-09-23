package store

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/e6qu/zzira/internal/models"
)

// Scheduling a plan automatically is what a planner asks for when the work is
// ranked but nobody has put dates on it: each team's work goes into its
// sprints in rank order, as far as the team's capacity reaches, after
// whatever blocks it. The answers are scenario changes, so a planner reviews
// them and decides what reaches Jira, exactly as a typed date does.

// PlanScheduleRequest is what one run of the scheduler should do.
type PlanScheduleRequest struct {
	// All schedules every unfinished work item rather than only the work
	// nothing is planned for.
	All bool
	// From is the earliest day the run may plan into; iterations that ended
	// before it are left alone.
	From time.Time
}

// PlanScheduleChange is one field the scheduler would set, in the shape a
// scenario change takes.
type PlanScheduleChange struct {
	IssueID, IssueKey, Field string
	Value                    json.RawMessage
	Current                  json.RawMessage
}

// PlanScheduleSkip is one work item the scheduler could not place, and why.
type PlanScheduleSkip struct{ Key, Reason string }

// PlanScheduleResult is what a run did.
type PlanScheduleResult struct {
	Changes []PlanScheduleChange
	// Scheduled is how many work items were given a place, Unestimated how
	// many of those carry no estimate and so took no capacity.
	Scheduled, Unestimated int
	Skipped                []PlanScheduleSkip
}

// planScheduleIteration is one team iteration as the scheduler fills it.
type planScheduleIteration struct {
	key, start, end string
	sprint          bool
	capacity, used  float64
}

// AutoSchedulePlan plans the work a scenario has not placed and writes the
// answers as that scenario's changes. It returns what it did, including the
// work it could not place and why.
func (s *Store) AutoSchedulePlan(ctx context.Context, workspaceID, actorID string, plan Plan, scenarioID int64, request PlanScheduleRequest, now time.Time) (PlanScheduleResult, error) {
	planning, err := s.PlanPlanning(ctx, workspaceID, actorID, plan, scenarioID, now)
	if err != nil {
		return PlanScheduleResult{}, err
	}
	result := planAutoSchedule(planning, plan.Scheduling.Dependencies != "Concurrent", request)
	for _, change := range result.Changes {
		if err := s.SetPlanChange(ctx, workspaceID, actorID, plan.ID, scenarioID, change.IssueID, change.Field, change.Value, change.Current); err != nil {
			return result, fmt.Errorf("plan %s: %w", change.IssueKey, err)
		}
	}
	return result, nil
}

// planAutoSchedule is the scheduler itself, over a plan as a scenario plans
// it. It answers with changes rather than writing them, so it can be read and
// tested on its own.
func planAutoSchedule(planning PlanPlanning, sequential bool, request PlanScheduleRequest) PlanScheduleResult {
	result := PlanScheduleResult{Changes: []PlanScheduleChange{}, Skipped: []PlanScheduleSkip{}}
	from := request.From.Format("2006-01-02")

	// Each team's iterations, with the work already planned into them as the
	// load they start with. Scheduling everything again empties them, because
	// the run is deciding where that work goes.
	iterations := map[int64][]*planScheduleIteration{}
	notes := map[int64]string{}
	for _, capacity := range planning.Capacity {
		notes[capacity.Team.ID] = capacity.Note
		for _, iteration := range capacity.Iterations {
			if iteration.End != "" && iteration.End < from {
				continue
			}
			used := iteration.Planned
			if request.All {
				used = iteration.Completed
			}
			iterations[capacity.Team.ID] = append(iterations[capacity.Team.ID], &planScheduleIteration{
				key: iteration.Key, start: iteration.Start, end: iteration.End,
				sprint: capacity.Team.PlanningStyle != "Kanban", capacity: iteration.Capacity, used: used,
			})
		}
	}

	// What blocks what, so nothing is planned before the work it waits for.
	// A plan whose dependencies are sequential puts blocked work in a later
	// iteration; one that allows them concurrently lets them share it.
	blockers := map[string][]string{}
	for _, dependency := range planning.Dependencies {
		if dependency.Blocker == nil || dependency.Blocked == nil {
			continue
		}
		blockers[dependency.Blocked.Issue.ID] = append(blockers[dependency.Blocked.Issue.ID], dependency.Blocker.Issue.ID)
	}

	// The work in the order the plan ranks it, parents last: an epic's dates
	// are what the work under it adds up to, which the plan rolls up, so the
	// scheduler places the work itself.
	order := map[string]int{}
	placed := map[string]int{}
	leaves := []*PlanItem{}
	for index, item := range planFlatItems(planning) {
		order[item.Issue.ID] = index
		if item.hasChildren {
			continue
		}
		if planned, ok := planning.Items[item.Issue.ID]; ok {
			leaves = append(leaves, planned)
		}
	}
	sort.SliceStable(leaves, func(a, b int) bool { return order[leaves[a].Issue.ID] < order[leaves[b].Issue.ID] })

	for _, item := range leaves {
		timeline := planning.timelineItem(item.Issue.ID)
		if timeline == nil {
			continue
		}
		if item.Issue.Status.Category == "done" {
			continue
		}
		scheduled := item.SprintID != "" || timeline.StartDate != "" || timeline.DueDate != ""
		if scheduled && !request.All {
			if index, ok := planScheduleIndexOf(iterations[item.TeamID], item.SprintID); ok {
				placed[item.Issue.ID] = index
			}
			continue
		}
		if item.TeamID == 0 {
			result.Skipped = append(result.Skipped, PlanScheduleSkip{Key: item.Issue.Key, Reason: "it has no team"})
			continue
		}
		team := iterations[item.TeamID]
		if len(team) == 0 {
			reason := notes[item.TeamID]
			if reason == "" {
				reason = "its team has no iterations to plan into"
			}
			result.Skipped = append(result.Skipped, PlanScheduleSkip{Key: item.Issue.Key, Reason: reason})
			continue
		}
		earliest := 0
		for _, blocker := range blockers[item.Issue.ID] {
			at, ok := placed[blocker]
			if !ok {
				continue
			}
			if sequential {
				at++
			}
			if at > earliest {
				earliest = at
			}
		}
		estimate := 0.0
		if item.Estimate != nil {
			estimate = *item.Estimate
		}
		index := -1
		for candidate := earliest; candidate < len(team); candidate++ {
			iteration := team[candidate]
			remaining := iteration.capacity - iteration.used
			// Work bigger than a whole iteration still has to start
			// somewhere: it goes in the first iteration with room left.
			if remaining >= estimate || (estimate > iteration.capacity && remaining > 0) {
				index = candidate
				break
			}
		}
		if index < 0 {
			result.Skipped = append(result.Skipped, PlanScheduleSkip{Key: item.Issue.Key, Reason: "no iteration of its team has room after the work it waits for"})
			continue
		}
		iteration := team[index]
		iteration.used += estimate
		placed[item.Issue.ID] = index
		result.Scheduled++
		if item.Estimate == nil {
			result.Unestimated++
		}
		if iteration.sprint && item.SprintID != iteration.key {
			result.Changes = append(result.Changes, planScheduleChange(item, "sprint", iteration.key, planScheduleCurrent(item, timeline, "sprint")))
		}
		if iteration.start != "" && planDayOf(timeline.StartDate) != iteration.start {
			result.Changes = append(result.Changes, planScheduleChange(item, "startDate", iteration.start, planScheduleCurrent(item, timeline, "startDate")))
		}
		if iteration.end != "" && planDayOf(timeline.DueDate) != iteration.end {
			result.Changes = append(result.Changes, planScheduleChange(item, "endDate", iteration.end, planScheduleCurrent(item, timeline, "endDate")))
		}
	}
	return result
}

// planScheduleIndexOf finds which of a team's iterations a sprint is.
func planScheduleIndexOf(iterations []*planScheduleIteration, sprintID string) (int, bool) {
	for index, iteration := range iterations {
		if iteration.key == sprintID {
			return index, true
		}
	}
	return 0, false
}

func planScheduleChange(item *PlanItem, field, value string, current json.RawMessage) PlanScheduleChange {
	encoded, _ := json.Marshal(value)
	return PlanScheduleChange{IssueID: item.Issue.ID, IssueKey: item.Issue.Key, Field: field, Value: encoded, Current: current}
}

// planScheduleCurrent is what the work item holds today, which a scenario
// change records so a planner sees what it would replace.
func planScheduleCurrent(item *PlanItem, timeline *models.TimelineItem, field string) json.RawMessage {
	var value any
	switch field {
	case "startDate":
		if timeline.StartDate != "" {
			value = planDayOf(timeline.StartDate)
		}
	case "endDate":
		if timeline.DueDate != "" {
			value = planDayOf(timeline.DueDate)
		}
	case "sprint":
		if item.SprintID != "" {
			value = item.SprintID
		}
	}
	if value == nil {
		return json.RawMessage("null")
	}
	encoded, _ := json.Marshal(value)
	return encoded
}

// planFlatItem is one row of the plan with whether anything sits under it.
type planFlatItem struct {
	Issue       *models.Issue
	hasChildren bool
}

// planFlatItems walks the plan's work in the order it is ranked, parents
// before the work beneath them.
func planFlatItems(planning PlanPlanning) []planFlatItem {
	flat := []planFlatItem{}
	var walk func(items []models.TimelineItem)
	walk = func(items []models.TimelineItem) {
		for index := range items {
			flat = append(flat, planFlatItem{Issue: items[index].Issue, hasChildren: len(items[index].Children) > 0})
			walk(items[index].Children)
		}
	}
	walk(planning.Work.Items)
	return flat
}
