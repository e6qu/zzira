package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"time"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

// PlanItem is how a scenario plans one work item: its team, sprint and
// estimate, and which of its fields the scenario changes.
type PlanItem struct {
	Issue      *models.Issue
	TeamID     int64
	TeamName   string
	SprintID   string
	SprintName string
	Estimate   *float64
	Changed    map[string]bool
}

// PlanIteration is one sprint or week of a team: its capacity and the
// estimated work planned into it.
type PlanIteration struct {
	Key, Name, State   string
	Start, End         string
	Capacity           float64
	Overridden         bool
	Planned, Completed float64
	Unestimated, Items int
	OverCapacity       bool
}

// PlanTeamCapacity is a team's iterations in a scenario.
type PlanTeamCapacity struct {
	Team       PlanTeam
	Name       string
	Iterations []PlanIteration
	// Note explains why a team has no iterations to plan.
	Note string
	// FromVelocity says the capacity shown is what the team has been
	// completing rather than a number somebody typed, so the plan can say
	// where it came from.
	FromVelocity bool
}

// PlanDependency is a blocks link between two work items of the plan.
type PlanDependency struct {
	Blocker, Blocked *PlanItem
	OffTrack         bool
	Reason           string
}

// PlanSprint is a sprint work in the plan can be planned into.
type PlanSprint struct {
	ID, Name, State, Start, End string
	BoardID                     string
}

// PlanPlanning is a plan as one of its scenarios plans it.
type PlanPlanning struct {
	Work         PlanWork
	ScenarioID   int64
	Unit         string
	Items        map[string]*PlanItem
	Capacity     []PlanTeamCapacity
	Dependencies []PlanDependency
	Sprints      []PlanSprint
	Changes      int
}

// planEstimateUnit names what a plan estimates work in.
func planEstimateUnit(plan Plan) string {
	switch plan.Scheduling.Estimation {
	case "Days":
		return "days"
	case "Hours":
		return "hours"
	}
	return "story points"
}

func (s *Store) siteFieldIDByType(ctx context.Context, workspaceID, fieldType, name string) (string, error) {
	var id string
	err := s.Pool.QueryRow(ctx, `SELECT id FROM custom_fields WHERE workspace_id=$1 AND app_installation_id IS NULL AND type=$2 AND ($3='' OR name=$3) AND trashed_at IS NULL ORDER BY id LIMIT 1`,
		workspaceID, fieldType, name).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return id, err
}

// PlanTeamFieldID is the site's Team field, empty when it has none.
func (s *Store) PlanTeamFieldID(ctx context.Context, workspaceID string) (string, error) {
	return s.siteFieldIDByType(ctx, workspaceID, models.CustomFieldTeam, "")
}

// PlanStoryPointFieldID is the site's Story point estimate field.
func (s *Store) PlanStoryPointFieldID(ctx context.Context, workspaceID string) (string, error) {
	return s.siteFieldIDByType(ctx, workspaceID, models.CustomFieldNumber, "Story point estimate")
}

// planHoursPerDay is how many working hours make a day of estimate.
func (s *Store) planHoursPerDay(ctx context.Context, workspaceID string) float64 {
	if configuration, err := s.JiraSiteConfiguration(ctx, workspaceID); err == nil && configuration.TimeTracking.WorkingHoursPerDay > 0 {
		return configuration.TimeTracking.WorkingHoursPerDay
	}
	return 8
}

// PlanPlanning builds a plan as a scenario plans it: the plan's work with the
// scenario's changes applied, each team's iterations against its capacity,
// and the dependencies between the work.
func (s *Store) PlanPlanning(ctx context.Context, workspaceID, userID string, plan Plan, scenarioID int64, now time.Time) (PlanPlanning, error) {
	planning := PlanPlanning{ScenarioID: scenarioID, Unit: planEstimateUnit(plan), Items: map[string]*PlanItem{}, Capacity: []PlanTeamCapacity{}, Dependencies: []PlanDependency{}, Sprints: []PlanSprint{}}
	work, err := s.PlanWork(ctx, workspaceID, userID, plan, now)
	if err != nil {
		return planning, err
	}
	changes, err := s.PlanChanges(ctx, workspaceID, plan.ID, scenarioID)
	if err != nil {
		return planning, err
	}
	planning.Changes = len(changes)
	changed := map[string]map[string]json.RawMessage{}
	for _, change := range changes {
		if changed[change.IssueID] == nil {
			changed[change.IssueID] = map[string]json.RawMessage{}
		}
		changed[change.IssueID][change.Field] = change.Value
	}
	teamFieldID, err := s.PlanTeamFieldID(ctx, workspaceID)
	if err != nil {
		return planning, err
	}
	storyPointsID, err := s.PlanStoryPointFieldID(ctx, workspaceID)
	if err != nil {
		return planning, err
	}
	hoursPerDay := s.planHoursPerDay(ctx, workspaceID)
	teamsByAtlassian := map[string]PlanTeam{}
	teamsByID := map[int64]PlanTeam{}
	for _, team := range work.Teams {
		teamsByID[team.ID] = team
		if team.AtlassianTeamID != "" {
			teamsByAtlassian[team.AtlassianTeamID] = team
		}
	}
	teamNames, err := s.planTeamNames(ctx, workspaceID, work.Teams)
	if err != nil {
		return planning, err
	}

	// Every work item, epics and their children alike.
	var flat []*models.TimelineItem
	var walk func(items []models.TimelineItem)
	walk = func(items []models.TimelineItem) {
		for index := range items {
			flat = append(flat, &items[index])
			walk(items[index].Children)
		}
	}
	walk(work.Items)
	ids := make([]string, 0, len(flat))
	for _, item := range flat {
		ids = append(ids, item.Issue.ID)
	}
	sprintsByIssue, err := s.SprintsForIssues(ctx, ids)
	if err != nil {
		return planning, err
	}
	sprintNames := map[string]PlanSprint{}
	remember := func(sprint *models.Sprint) {
		if _, ok := sprintNames[sprint.ID]; !ok {
			sprintNames[sprint.ID] = PlanSprint{ID: sprint.ID, Name: sprint.Name, State: sprint.State, Start: sprint.StartDate, End: sprint.EndDate, BoardID: sprint.BoardID}
		}
	}
	teamBoards := map[int64]string{}
	for _, team := range work.Teams {
		boardID, err := s.planTeamBoard(ctx, workspaceID, plan, team)
		if err != nil {
			return planning, err
		}
		if boardID == "" {
			continue
		}
		teamBoards[team.ID] = boardID
		sprints, err := s.SprintsByBoard(ctx, boardID)
		if err != nil {
			return planning, err
		}
		for _, sprint := range sprints {
			if sprint.State == "active" || sprint.State == "future" {
				remember(sprint)
			}
		}
	}

	for _, item := range flat {
		issue := item.Issue
		planned := &PlanItem{Issue: issue, Changed: map[string]bool{}}
		if teamFieldID != "" {
			var teamID string
			if raw, ok := issue.Fields[teamFieldID]; ok && json.Unmarshal(raw, &teamID) == nil {
				if team, ok := teamsByAtlassian[teamID]; ok {
					planned.TeamID = team.ID
				}
			}
		}
		for _, sprint := range sprintsByIssue[issue.ID] {
			if sprint.State == "active" || sprint.State == "future" {
				planned.SprintID = sprint.ID
				remember(sprint)
			}
		}
		planned.Estimate = planIssueEstimate(issue, planning.Unit, storyPointsID, hoursPerDay)
		for field, raw := range changed[issue.ID] {
			planned.Changed[field] = true
			switch field {
			case "summary":
				var summary string
				if json.Unmarshal(raw, &summary) == nil {
					copied := *issue
					copied.Summary = summary
					issue = &copied
					item.Issue, planned.Issue = issue, issue
				}
			case "startDate", "endDate":
				var day string
				_ = json.Unmarshal(raw, &day)
				if field == "startDate" {
					item.StartDate = day
				} else {
					item.DueDate = day
				}
			case "team":
				var teamID int64
				planned.TeamID = 0
				if json.Unmarshal(raw, &teamID) == nil {
					if _, ok := teamsByID[teamID]; ok {
						planned.TeamID = teamID
					}
				}
			case "sprint":
				var sprintID string
				planned.SprintID = ""
				if json.Unmarshal(raw, &sprintID) == nil && sprintID != "" {
					if _, ok := sprintNames[sprintID]; !ok {
						if sprint, err := s.SprintByIDInWorkspace(ctx, workspaceID, sprintID); err == nil {
							remember(sprint)
						}
					}
					if _, ok := sprintNames[sprintID]; ok {
						planned.SprintID = sprintID
					}
				}
			case "estimate":
				var estimate *float64
				_ = json.Unmarshal(raw, &estimate)
				planned.Estimate = estimate
			}
		}
		if planned.TeamID != 0 {
			planned.TeamName = teamNames[planned.TeamID]
		}
		if planned.SprintID != "" {
			planned.SprintName = sprintNames[planned.SprintID].Name
		}
		planning.Items[issue.ID] = planned
	}
	planning.Work = work

	for _, sprint := range sprintNames {
		planning.Sprints = append(planning.Sprints, sprint)
	}
	sortPlanSprints(planning.Sprints)

	overrides, err := s.PlanIterationCapacities(ctx, scenarioID)
	if err != nil {
		return planning, err
	}
	override := map[string]float64{}
	for _, capacity := range overrides {
		override[strconv.FormatInt(capacity.TeamID, 10)+"/"+capacity.Iteration] = capacity.Capacity
	}
	sprintOrder := map[string]int{}
	for index, sprint := range planning.Sprints {
		sprintOrder[sprint.ID] = index
	}
	for _, team := range work.Teams {
		// A Scrum team that has not said what it can take in a sprint takes
		// what it has been taking: the average of its board's last completed
		// sprints. A team with no board and no number keeps the default.
		measured := (*float64)(nil)
		if team.Capacity == nil && team.PlanningStyle != "Kanban" && teamBoards[team.ID] != "" && planning.Unit == "story points" {
			velocity, err := s.planTeamVelocity(ctx, workspaceID, userID, teamBoards[team.ID])
			if err != nil {
				return planning, err
			}
			measured = velocity
		}
		planning.Capacity = append(planning.Capacity, planTeamCapacity(team, teamNames[team.ID], planning, teamBoards[team.ID], override, hoursPerDay, now, measured))
	}
	if planning.Dependencies, err = s.planDependencies(ctx, workspaceID, plan, planning, sprintOrder); err != nil {
		return planning, err
	}
	return planning, nil
}

func sortPlanSprints(sprints []PlanSprint) {
	sort.SliceStable(sprints, func(a, b int) bool {
		left, right := sprints[a], sprints[b]
		if (left.State == "active") != (right.State == "active") {
			return left.State == "active"
		}
		if left.Start != right.Start {
			if left.Start == "" || right.Start == "" {
				return right.Start == ""
			}
			return left.Start < right.Start
		}
		return left.Name < right.Name
	})
}

func planIssueEstimate(issue *models.Issue, unit, storyPointsID string, hoursPerDay float64) *float64 {
	switch unit {
	case "story points":
		if storyPointsID == "" {
			return nil
		}
		return decodeEstimate(issue.Fields[storyPointsID])
	case "hours", "days":
		if issue.OriginalEstimateSeconds == nil {
			return nil
		}
		value := float64(*issue.OriginalEstimateSeconds) / 3600
		if unit == "days" {
			value /= hoursPerDay
		}
		value = math.Round(value*100) / 100
		return &value
	}
	return nil
}

// planTeamNames names each team: a plan-only team by its name, an Atlassian
// team by the team's name.
func (s *Store) planTeamNames(ctx context.Context, workspaceID string, teams []PlanTeam) (map[int64]string, error) {
	names := map[int64]string{}
	var directory map[string]string
	for _, team := range teams {
		if team.AtlassianTeamID == "" {
			names[team.ID] = team.Name
			continue
		}
		if directory == nil {
			directory = map[string]string{}
			listed, err := s.AtlassianTeams(ctx, workspaceID)
			if err != nil {
				return nil, err
			}
			for _, found := range listed {
				directory[found.ID] = found.Name
			}
		}
		names[team.ID] = directory[team.AtlassianTeamID]
	}
	return names, nil
}

// planTeamBoard is the board a team plans its sprints on: its issue source,
// when that source is a board.
func (s *Store) planTeamBoard(ctx context.Context, workspaceID string, plan Plan, team PlanTeam) (string, error) {
	if team.IssueSourceID == nil {
		return "", nil
	}
	for _, source := range plan.IssueSources {
		if source.ID != *team.IssueSourceID || source.Type != "Board" {
			continue
		}
		var boardID string
		err := s.Pool.QueryRow(ctx, `SELECT b.id FROM boards b JOIN projects p ON p.id=b.project_id WHERE p.workspace_id=$1 AND b.jira_id=$2`, workspaceID, source.Value).Scan(&boardID)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return boardID, err
	}
	return "", nil
}

// planTeamVelocity is what a board's team has been completing: the mean of the
// completed work of its last closed sprints, which is the number a plan uses
// when nobody has said what the team can take. A board with no closed sprint
// has no velocity to read, and the plan keeps its default.
func (s *Store) planTeamVelocity(ctx context.Context, workspaceID, userID, boardID string) (*float64, error) {
	board, err := s.BoardByIDInWorkspace(ctx, workspaceID, boardID)
	if err != nil || board == nil {
		return nil, nil
	}
	report, err := s.VelocityReport(ctx, workspaceID, userID, board)
	if err != nil {
		return nil, err
	}
	total, counted := 0.0, 0
	for _, sprint := range report.Sprints {
		total += sprint.Completed
		counted++
	}
	if counted == 0 {
		return nil, nil
	}
	average := math.Round(total/float64(counted)*100) / 100
	if average <= 0 {
		return nil, nil
	}
	return &average, nil
}

// planTeamCapacity lays a team's work into its iterations. A Scrum team plans
// the active and future sprints of its board, each with the team's capacity
// per sprint in story points, or its weekly capacity times the sprint's weeks
// for time estimates. A Kanban team plans one-week iterations with time
// estimates. Work consumes the capacity of the iteration it is planned into:
// a sprint by the work's sprint, a week by the share of the work's dates that
// fall in it.
func planTeamCapacity(team PlanTeam, name string, planning PlanPlanning, boardID string, override map[string]float64, hoursPerDay float64, now time.Time, measured *float64) PlanTeamCapacity {
	out := PlanTeamCapacity{Team: team, Name: name, Iterations: []PlanIteration{}}
	unit := planning.Unit
	weekly := 200.0
	if unit == "days" {
		weekly = 200 / hoursPerDay
	}
	if team.Capacity != nil {
		weekly = *team.Capacity
	}
	perSprintPoints := 30.0
	if measured != nil {
		perSprintPoints = *measured
		out.FromVelocity = true
	}
	if team.Capacity != nil {
		perSprintPoints = *team.Capacity
	}
	items := []*PlanItem{}
	for _, item := range planning.Items {
		if item.TeamID == team.ID {
			items = append(items, item)
		}
	}
	sort.Slice(items, func(a, b int) bool { return items[a].Issue.Key < items[b].Issue.Key })
	key := func(iteration string) string { return strconv.FormatInt(team.ID, 10) + "/" + iteration }
	finish := func(iteration *PlanIteration) {
		if value, ok := override[key(iteration.Key)]; ok {
			iteration.Capacity, iteration.Overridden = value, true
		}
		iteration.Capacity = math.Round(iteration.Capacity*100) / 100
		iteration.Planned = math.Round(iteration.Planned*100) / 100
		iteration.Completed = math.Round(iteration.Completed*100) / 100
		iteration.OverCapacity = iteration.Planned > iteration.Capacity
	}
	if team.PlanningStyle == "Kanban" {
		if unit == "story points" {
			out.Note = "Kanban teams plan with time estimates; this plan estimates in story points."
			return out
		}
		today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		monday := today.AddDate(0, 0, -((int(today.Weekday()) + 6) % 7))
		for week := 0; week < 12; week++ {
			start := monday.AddDate(0, 0, 7*week)
			end := start.AddDate(0, 0, 6)
			iteration := PlanIteration{Key: start.Format("2006-01-02"), Name: "Week of " + start.Format("2 Jan 2006"), Start: start.Format("2006-01-02"), End: end.Format("2006-01-02"), Capacity: weekly}
			for _, item := range items {
				timeline := planning.timelineItem(item.Issue.ID)
				if timeline == nil {
					continue
				}
				first, okStart := parseTimelineDate(timeline.StartDate)
				last, okEnd := parseTimelineDate(timeline.DueDate)
				if !okStart || !okEnd || last.Before(first) || last.Before(start) || first.After(end) {
					continue
				}
				iteration.Items++
				if item.Estimate == nil {
					iteration.Unestimated++
					continue
				}
				days := last.Sub(first).Hours()/24 + 1
				overlapStart, overlapEnd := first, last
				if start.After(overlapStart) {
					overlapStart = start
				}
				if end.Before(overlapEnd) {
					overlapEnd = end
				}
				share := *item.Estimate * (overlapEnd.Sub(overlapStart).Hours()/24 + 1) / days
				iteration.Planned += share
				if item.Issue.Status.Category == "done" {
					iteration.Completed += share
				}
			}
			finish(&iteration)
			out.Iterations = append(out.Iterations, iteration)
		}
		return out
	}
	if boardID == "" {
		out.Note = "Give the team a board as its issue source to plan its sprints."
		return out
	}
	for _, sprint := range planning.Sprints {
		if sprint.BoardID != boardID {
			continue
		}
		capacity := perSprintPoints
		if unit != "story points" {
			weeks := 2.0
			if team.SprintLength != nil {
				weeks = float64(*team.SprintLength)
			} else if first, ok := parseTimelineDate(sprint.Start); ok {
				if last, ok := parseTimelineDate(sprint.End); ok && last.After(first) {
					weeks = math.Max(1, math.Round(last.Sub(first).Hours()/24/7))
				}
			}
			capacity = weekly * weeks
		}
		iteration := PlanIteration{Key: sprint.ID, Name: sprint.Name, State: sprint.State, Start: planDayOf(sprint.Start), End: planDayOf(sprint.End), Capacity: capacity}
		for _, item := range items {
			if item.SprintID != sprint.ID {
				continue
			}
			iteration.Items++
			if item.Estimate == nil {
				iteration.Unestimated++
				continue
			}
			iteration.Planned += *item.Estimate
			if item.Issue.Status.Category == "done" {
				iteration.Completed += *item.Estimate
			}
		}
		finish(&iteration)
		out.Iterations = append(out.Iterations, iteration)
	}
	if len(out.Iterations) == 0 {
		out.Note = "The team's board has no active or future sprints."
	}
	return out
}

func (planning PlanPlanning) timelineItem(issueID string) *models.TimelineItem {
	var find func(items []models.TimelineItem) *models.TimelineItem
	find = func(items []models.TimelineItem) *models.TimelineItem {
		for index := range items {
			if items[index].Issue.ID == issueID {
				return &items[index]
			}
			if found := find(items[index].Children); found != nil {
				return found
			}
		}
		return nil
	}
	return find(planning.Work.Items)
}

func parseTimelineDate(value string) (time.Time, bool) {
	day, err := time.Parse("2006-01-02", planDayOf(value))
	return day, err == nil
}

func planDayOf(value string) string {
	if len(value) >= 10 {
		return value[:10]
	}
	return value
}

// planDependencies finds the blocks links between work in the plan. One is
// off track when the blocking work ends after the blocked work starts, or is
// planned into a later sprint, or into the same sprint when the plan does not
// let dependent work share an iteration.
func (s *Store) planDependencies(ctx context.Context, workspaceID string, plan Plan, planning PlanPlanning, sprintOrder map[string]int) ([]PlanDependency, error) {
	ids := make([]string, 0, len(planning.Items))
	for id := range planning.Items {
		ids = append(ids, id)
	}
	dependencies := []PlanDependency{}
	if len(ids) == 0 {
		return dependencies, nil
	}
	rows, err := s.Pool.Query(ctx, `SELECT l.outward_id,l.inward_id FROM issue_links l JOIN issue_link_types lt ON lt.id=l.link_type_id
		WHERE l.workspace_id=$1 AND lower(lt.name)='blocks' AND l.outward_id=ANY($2) AND l.inward_id=ANY($2)
		ORDER BY l.created_at,l.id`, workspaceID, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var blockerID, blockedID string
		if err := rows.Scan(&blockerID, &blockedID); err != nil {
			return nil, err
		}
		dependency := PlanDependency{Blocker: planning.Items[blockerID], Blocked: planning.Items[blockedID]}
		blocker, blocked := planning.timelineItem(blockerID), planning.timelineItem(blockedID)
		if blocker != nil && blocked != nil {
			if end, ok := parseTimelineDate(blocker.DueDate); ok {
				if start, ok := parseTimelineDate(blocked.StartDate); ok && end.After(start) {
					dependency.OffTrack = true
					dependency.Reason = fmt.Sprintf("%s ends after %s starts.", dependency.Blocker.Issue.Key, dependency.Blocked.Issue.Key)
				}
			}
		}
		if !dependency.OffTrack && dependency.Blocker.SprintID != "" && dependency.Blocked.SprintID != "" {
			before, after := sprintOrder[dependency.Blocker.SprintID], sprintOrder[dependency.Blocked.SprintID]
			switch {
			case before > after:
				dependency.OffTrack = true
				dependency.Reason = fmt.Sprintf("%s is planned into a later sprint than %s.", dependency.Blocker.Issue.Key, dependency.Blocked.Issue.Key)
			case before == after && plan.Scheduling.Dependencies != "Concurrent":
				dependency.OffTrack = true
				dependency.Reason = fmt.Sprintf("%s and %s are planned into the same sprint.", dependency.Blocker.Issue.Key, dependency.Blocked.Issue.Key)
			}
		}
		dependencies = append(dependencies, dependency)
	}
	return dependencies, rows.Err()
}

// PlanDateFieldIDs are the fields a plan's start and end dates are read from
// and saved to.
func (s *Store) PlanDateFieldIDs(ctx context.Context, workspaceID string, plan Plan) (string, string, error) {
	startID, _, err := s.planDateField(ctx, workspaceID, plan.Scheduling.StartDate)
	if err != nil {
		return "", "", err
	}
	endID, _, err := s.planDateField(ctx, workspaceID, plan.Scheduling.EndDate)
	return startID, endID, err
}

// PlanHoursPerDay is how many working hours make a day of estimate on the site.
func (s *Store) PlanHoursPerDay(ctx context.Context, workspaceID string) float64 {
	return s.planHoursPerDay(ctx, workspaceID)
}

// Timeline is the timeline entry of a work item in the plan.
func (planning PlanPlanning) Timeline(issueID string) *models.TimelineItem {
	return planning.timelineItem(issueID)
}

// PlanSourceNames names each of a plan's issue sources for people choosing a
// team's source: a board, project or saved filter by its name.
func (s *Store) PlanSourceNames(ctx context.Context, workspaceID string, plan Plan) (map[int64]string, error) {
	names := map[int64]string{}
	for _, source := range plan.IssueSources {
		var name string
		var err error
		switch source.Type {
		case "Board":
			err = s.Pool.QueryRow(ctx, `SELECT b.name FROM boards b JOIN projects p ON p.id=b.project_id WHERE p.workspace_id=$1 AND b.jira_id=$2`, workspaceID, source.Value).Scan(&name)
		case "Project":
			err = s.Pool.QueryRow(ctx, `SELECT name||' ('||key||')' FROM projects WHERE workspace_id=$1 AND id=$2`, workspaceID, strconv.FormatInt(source.Value, 10)).Scan(&name)
		case "Filter":
			err = s.Pool.QueryRow(ctx, `SELECT name FROM filters WHERE workspace_id=$1 AND jira_id=$2`, workspaceID, source.Value).Scan(&name)
		}
		if errors.Is(err, pgx.ErrNoRows) {
			name, err = "Removed", nil
		}
		if err != nil {
			return nil, err
		}
		names[source.ID] = source.Type + ": " + name
	}
	return names, nil
}
