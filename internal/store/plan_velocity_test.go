package store

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/models"
)

// A plan's Scrum team takes what it has been taking: when nobody has said what
// a team can carry in a sprint, its capacity is the mean of its board's last
// closed sprints rather than a number nobody chose. A team that does say keeps
// its own number.
func TestPlanCapacityComesFromVelocityUntilSomebodySaysOtherwise(t *testing.T) {
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
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Velocity capacity test')`, workspaceID)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Lead')`, leadID, leadID+"@example.invalid")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'member')`, workspaceID, leadID)
	exec(`INSERT INTO projects(id,workspace_id,key,name,workflow_id) VALUES($1,$2,$3,'Velocity project','wf_default')`,
		projectID, workspaceID, "VC"+strings.ToUpper(projectID[len(projectID)-5:]))
	t.Cleanup(func() {
		exec(`DELETE FROM plans WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM atlassian_teams WHERE workspace_id=$1`, workspaceID)
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
	board, err := st.CreateBoard(ctx, leadID, workspaceID, BoardCreate{Name: "Velocity board", Type: "scrum", ProjectID: projectID})
	if err != nil {
		t.Fatal(err)
	}
	var fieldID string
	if err := st.Pool.QueryRow(ctx, `
		UPDATE boards SET estimation_field_id=(SELECT id FROM custom_fields WHERE workspace_id=$2 AND app_installation_id IS NULL AND name='Story point estimate')
		WHERE id=$1 RETURNING estimation_field_id`, board.ID, workspaceID).Scan(&fieldID); err != nil {
		t.Fatal(err)
	}

	// One sprint that finished eight points of the ten it took on, and a
	// future one for the plan to lay work into.
	done := "st_done"
	closedSprint, _, err := st.CreateSprint(ctx, leadID, workspaceID, board.ID, "Sprint 1", "")
	if err != nil {
		t.Fatal(err)
	}
	points := func(value float64) map[string]json.RawMessage {
		return map[string]json.RawMessage{fieldID: json.RawMessage(formatEstimate(&value))}
	}
	shipped, _, err := st.CreateIssue(ctx, leadID, projectID, "Shipped", json.RawMessage(`{"type":"doc","version":1,"content":[]}`),
		"st_todo", "it_task", "pr_medium", "", nil, points(8), "", "")
	if err != nil {
		t.Fatal(err)
	}
	left, _, err := st.CreateIssue(ctx, leadID, projectID, "Left over", json.RawMessage(`{"type":"doc","version":1,"content":[]}`),
		"st_todo", "it_task", "pr_medium", "", nil, points(2), "", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, issue := range []*models.Issue{shipped, left} {
		rank, rankErr := st.NextSprintRank(ctx, closedSprint.ID)
		if rankErr != nil {
			t.Fatal(rankErr)
		}
		if _, err := st.AddIssueToSprint(ctx, leadID, workspaceID, closedSprint.ID, issue.ID, rank); err != nil {
			t.Fatal(err)
		}
	}
	start, end := time.Now().UTC().Add(-14*24*time.Hour), time.Now().UTC().Add(-time.Hour)
	if _, _, err := st.UpdateSprint(ctx, leadID, workspaceID, closedSprint.ID,
		SprintUpdate{Name: closedSprint.Name, State: "active", StartDate: &start, EndDate: &end}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.UpdateIssue(ctx, leadID, workspaceID, shipped.ID, IssueUpdate{StatusID: &done}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.UpdateSprint(ctx, leadID, workspaceID, closedSprint.ID,
		SprintUpdate{Name: closedSprint.Name, State: "closed", StartDate: &start, EndDate: &end}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.CreateSprint(ctx, leadID, workspaceID, board.ID, "Sprint 2", ""); err != nil {
		t.Fatal(err)
	}

	plan := Plan{Name: "Velocity plan", LeadAccountID: leadID, Scheduling: PlanScheduling{Estimation: "StoryPoints"},
		IssueSources: []PlanIssueSource{{Type: "Board", Value: board.JiraID}}}
	NormalizePlan(&plan)
	planID, err := st.CreatePlan(ctx, workspaceID, leadID, plan)
	if err != nil {
		t.Fatal(err)
	}
	if plan, err = st.Plan(ctx, workspaceID, planID); err != nil {
		t.Fatal(err)
	}
	source := plan.IssueSources[0].ID
	teamID, err := st.SavePlanTeam(ctx, workspaceID, planID, PlanTeam{Name: "Delivery", PlanningStyle: "Scrum", IssueSourceID: &source}, true)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	planning, err := st.PlanPlanning(ctx, workspaceID, leadID, plan, plan.ScenarioID, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(planning.Capacity) != 1 {
		t.Fatalf("capacity = %+v", planning.Capacity)
	}
	measured := planning.Capacity[0]
	if !measured.FromVelocity {
		t.Fatalf("a team nobody typed a number for did not read its capacity from velocity: %+v", measured)
	}
	if len(measured.Iterations) == 0 || measured.Iterations[0].Capacity != 8 {
		t.Fatalf("capacity from velocity = %+v, want the 8 points the team completed", measured.Iterations)
	}

	// Somebody says what the team can take, and that is what the plan uses.
	chosen := 21.0
	if _, err := st.SavePlanTeam(ctx, workspaceID, planID,
		PlanTeam{ID: teamID, Name: "Delivery", PlanningStyle: "Scrum", IssueSourceID: &source, Capacity: &chosen}, false); err != nil {
		t.Fatal(err)
	}
	planning, err = st.PlanPlanning(ctx, workspaceID, leadID, plan, plan.ScenarioID, now)
	if err != nil {
		t.Fatal(err)
	}
	chosenCapacity := planning.Capacity[0]
	if chosenCapacity.FromVelocity || len(chosenCapacity.Iterations) == 0 || chosenCapacity.Iterations[0].Capacity != 21 {
		t.Fatalf("capacity after somebody chose one = %+v", chosenCapacity)
	}
}
