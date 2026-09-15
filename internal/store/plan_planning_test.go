package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/models"
)

func TestPlanPlanningCapacityDependenciesAndScenarios(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	st, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if err := Migrate(ctx, st.Pool); err != nil {
		t.Fatal(err)
	}
	workspaceID, leadID, projectID := NewID("ws"), NewID("usr"), NewID("prj")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Planning test')`, workspaceID)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Lead')`, leadID, leadID+"@example.invalid")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'member')`, workspaceID, leadID)
	exec(`INSERT INTO projects(id,workspace_id,key,name,workflow_id) VALUES($1,$2,$3,'Planning project','wf_default')`, projectID, workspaceID, "PP"+strings.ToUpper(projectID[len(projectID)-5:]))
	t.Cleanup(func() {
		exec(`DELETE FROM plans WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM atlassian_teams WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM issue_links WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM sprints WHERE board_id IN (SELECT id FROM boards WHERE project_id=$1)`, projectID)
		exec(`DELETE FROM boards WHERE project_id=$1`, projectID)
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM custom_fields WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		exec(`DELETE FROM users WHERE id=$1`, leadID)
	})
	board, err := st.CreateBoard(ctx, leadID, workspaceID, BoardCreate{Name: "Planning board", Type: "scrum", ProjectID: projectID})
	if err != nil {
		t.Fatal(err)
	}
	first, _, err := st.CreateSprint(ctx, leadID, workspaceID, board.ID, "Sprint 1", "")
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := st.CreateSprint(ctx, leadID, workspaceID, board.ID, "Sprint 2", "")
	if err != nil {
		t.Fatal(err)
	}
	teamFieldID, err := st.PlanTeamFieldID(ctx, workspaceID)
	if err != nil || teamFieldID == "" {
		t.Fatalf("team field = %q, %v", teamFieldID, err)
	}
	pointsID, err := st.PlanStoryPointFieldID(ctx, workspaceID)
	if err != nil || pointsID == "" {
		t.Fatalf("story point field = %q, %v", pointsID, err)
	}
	targetStart, _ := st.siteDateFieldID(ctx, workspaceID, "Target start")
	targetEnd, _ := st.siteDateFieldID(ctx, workspaceID, "Target end")
	platform, err := st.CreateAtlassianTeam(ctx, workspaceID, leadID, "Platform", "")
	if err != nil {
		t.Fatal(err)
	}
	create := func(summary string, fields map[string]json.RawMessage) *models.Issue {
		t.Helper()
		issue, _, err := st.CreateIssue(ctx, leadID, projectID, summary, json.RawMessage(`{"type":"doc","version":1,"content":[]}`), "st_todo", "it_story", "pr_medium", "", nil, fields, "", "")
		if err != nil {
			t.Fatal(err)
		}
		return issue
	}
	owned := func(points string, start, end string) map[string]json.RawMessage {
		fields := map[string]json.RawMessage{teamFieldID: json.RawMessage(`"` + platform + `"`), targetStart: json.RawMessage(`"` + start + `"`), targetEnd: json.RawMessage(`"` + end + `"`)}
		if points != "" {
			fields[pointsID] = json.RawMessage(points)
		}
		return fields
	}
	api := create("API", owned("5", "2026-10-01", "2026-10-09"))
	client := create("Client", owned("8", "2026-10-12", "2026-10-20"))
	docs := create("Docs", owned("", "2026-10-12", "2026-10-14"))
	for _, issue := range []*models.Issue{api, client, docs} {
		rank, err := st.NextSprintRank(ctx, first.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.AddIssueToSprint(ctx, leadID, workspaceID, first.ID, issue.ID, rank); err != nil {
			t.Fatal(err)
		}
	}
	var blocksID string
	if err := st.Pool.QueryRow(ctx, `SELECT id FROM issue_link_types WHERE workspace_id=$1 AND name='Blocks'`, workspaceID).Scan(&blocksID); err != nil {
		t.Fatal(err)
	}
	// API blocks Client: the outward issue blocks the inward one.
	if _, _, err := st.CreateIssueLink(ctx, leadID, workspaceID, blocksID, client.ID, api.ID); err != nil {
		t.Fatal(err)
	}

	plan := Plan{Name: "Delivery", LeadAccountID: leadID, Scheduling: PlanScheduling{Estimation: "StoryPoints"}, IssueSources: []PlanIssueSource{{Type: "Board", Value: board.JiraID}}}
	NormalizePlan(&plan)
	planID, err := st.CreatePlan(ctx, workspaceID, leadID, plan)
	if err != nil {
		t.Fatal(err)
	}
	if plan, err = st.Plan(ctx, workspaceID, planID); err != nil {
		t.Fatal(err)
	}
	capacity, sprintLength := 10.0, int64(2)
	source := plan.IssueSources[0].ID
	if _, err := st.SavePlanTeam(ctx, workspaceID, planID, PlanTeam{AtlassianTeamID: platform, PlanningStyle: "Scrum", IssueSourceID: &source, Capacity: &capacity, SprintLength: &sprintLength}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SavePlanTeam(ctx, workspaceID, planID, PlanTeam{Name: "Ops", PlanningStyle: "Kanban"}, true); err != nil {
		t.Fatal(err)
	}
	scenarios, err := st.PlanScenarios(ctx, workspaceID, planID)
	if err != nil || len(scenarios) != 1 || !scenarios[0].Default || scenarios[0].ID != plan.ScenarioID || scenarios[0].Name != "Default" {
		t.Fatalf("scenarios = %+v, %v", scenarios, err)
	}
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	planning, err := st.PlanPlanning(ctx, workspaceID, leadID, plan, plan.ScenarioID, now)
	if err != nil {
		t.Fatal(err)
	}
	if item := planning.Items[api.ID]; item == nil || item.TeamName != "Platform" || item.SprintName != "Sprint 1" || item.Estimate == nil || *item.Estimate != 5 {
		t.Fatalf("api item = %+v", item)
	}
	if len(planning.Capacity) != 2 {
		t.Fatalf("capacity = %+v", planning.Capacity)
	}
	scrum, kanban := planning.Capacity[0], planning.Capacity[1]
	if scrum.Name != "Platform" || len(scrum.Iterations) != 2 || scrum.Iterations[0].Name != "Sprint 1" || scrum.Iterations[0].Capacity != 10 ||
		scrum.Iterations[0].Planned != 13 || scrum.Iterations[0].Unestimated != 1 || !scrum.Iterations[0].OverCapacity || scrum.Iterations[1].Planned != 0 || scrum.Iterations[1].OverCapacity {
		t.Fatalf("scrum capacity = %+v", scrum)
	}
	if kanban.Name != "Ops" || kanban.Note == "" || len(kanban.Iterations) != 0 {
		t.Fatalf("kanban capacity = %+v", kanban)
	}
	if len(planning.Dependencies) != 1 || planning.Dependencies[0].Blocker.Issue.ID != api.ID || !planning.Dependencies[0].OffTrack || !strings.Contains(planning.Dependencies[0].Reason, "same sprint") {
		t.Fatalf("dependencies = %+v", planning.Dependencies)
	}

	// A scenario plans differently without touching the default.
	stretch, err := st.CreatePlanScenario(ctx, workspaceID, leadID, planID, "Stretch", "orange", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreatePlanScenario(ctx, workspaceID, leadID, planID, "stretch", "blue", 0); !errors.Is(err, ErrPlanValidation) {
		t.Fatalf("duplicate scenario name error = %v", err)
	}
	change := func(scenarioID int64, issue *models.Issue, field, value string, current string) {
		t.Helper()
		normalized, err := NormalizePlanChangeValue(field, value)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.SetPlanChange(ctx, workspaceID, leadID, planID, scenarioID, issue.ID, field, normalized, json.RawMessage(current)); err != nil {
			t.Fatal(err)
		}
	}
	change(stretch, client, "estimate", "2", "8")
	change(stretch, client, "sprint", second.ID, `"`+first.ID+`"`)
	change(stretch, api, "endDate", "2026-10-15", `"2026-10-09"`)
	change(stretch, api, "summary", "API", `"API"`) // the value it already has is no change
	if err := st.SetPlanIterationCapacity(ctx, workspaceID, leadID, planID, stretch, planning.Capacity[0].Team.ID, second.ID, floatPointer(20)); err != nil {
		t.Fatal(err)
	}
	changes, err := st.PlanChanges(ctx, workspaceID, planID, stretch)
	if err != nil || len(changes) != 3 {
		t.Fatalf("changes = %+v, %v", changes, err)
	}
	stretched, err := st.PlanPlanning(ctx, workspaceID, leadID, plan, stretch, now)
	if err != nil {
		t.Fatal(err)
	}
	if item := stretched.Items[client.ID]; *item.Estimate != 2 || item.SprintID != second.ID || !item.Changed["estimate"] || !item.Changed["sprint"] {
		t.Fatalf("stretched client = %+v", item)
	}
	iterations := stretched.Capacity[0].Iterations
	if iterations[0].Planned != 5 || iterations[0].OverCapacity || iterations[1].Planned != 2 || iterations[1].Capacity != 20 || !iterations[1].Overridden {
		t.Fatalf("stretched iterations = %+v", iterations)
	}
	// The sprints are now in order, but API now ends after Client starts.
	if len(stretched.Dependencies) != 1 || !stretched.Dependencies[0].OffTrack || !strings.Contains(stretched.Dependencies[0].Reason, "ends after") {
		t.Fatalf("stretched dependencies = %+v", stretched.Dependencies)
	}
	if base, err := st.PlanPlanning(ctx, workspaceID, leadID, plan, plan.ScenarioID, now); err != nil || *base.Items[client.ID].Estimate != 8 || base.Changes != 0 {
		t.Fatalf("default after scenario changes = %+v, %v", base.Items[client.ID], err)
	}

	// Dependent work may share an iteration when the plan allows it.
	plan.Scheduling.Dependencies = "Concurrent"
	if err := st.UpdatePlan(ctx, workspaceID, plan); err != nil {
		t.Fatal(err)
	}
	if concurrent, err := st.PlanPlanning(ctx, workspaceID, leadID, plan, plan.ScenarioID, now); err != nil || concurrent.Dependencies[0].OffTrack {
		t.Fatalf("concurrent dependencies = %+v, %v", concurrent.Dependencies, err)
	}

	// A copy keeps the scenario's changes and capacities; the default stays.
	copied, err := st.CreatePlanScenario(ctx, workspaceID, leadID, planID, "Stretch copy", "teal", stretch)
	if err != nil {
		t.Fatal(err)
	}
	if copies, err := st.PlanChanges(ctx, workspaceID, planID, copied); err != nil || len(copies) != 3 {
		t.Fatalf("copied changes = %+v, %v", copies, err)
	}
	if err := st.DeletePlanScenario(ctx, workspaceID, planID, plan.ScenarioID); !errors.Is(err, ErrPlanValidation) {
		t.Fatalf("delete default scenario error = %v", err)
	}
	if err := st.DiscardPlanChange(ctx, workspaceID, planID, stretch, client.ID, "estimate"); err != nil {
		t.Fatal(err)
	}
	if err := st.DeletePlanScenario(ctx, workspaceID, planID, copied); err != nil {
		t.Fatal(err)
	}
	if scenarios, err = st.PlanScenarios(ctx, workspaceID, planID); err != nil || len(scenarios) != 2 || scenarios[1].Name != "Stretch" || scenarios[1].Changes != 2 {
		t.Fatalf("scenarios after delete = %+v, %v", scenarios, err)
	}
	if _, err := NormalizePlanChangeValue("startDate", "15/10/2026"); !errors.Is(err, ErrPlanValidation) {
		t.Fatalf("bad date error = %v", err)
	}
	if _, err := NormalizePlanChangeValue("priority", "High"); !errors.Is(err, ErrPlanValidation) {
		t.Fatalf("unknown field error = %v", err)
	}
}
