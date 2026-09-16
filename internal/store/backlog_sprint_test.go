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

// planningFixture builds a workspace, a member, a project and a scrum board of
// its own. Planning tests own their site that way, so they neither depend on a
// seeded demo site nor disturb one.
func planningFixture(ctx context.Context, t *testing.T, st *Store, label string) (string, string, string, *models.Board) {
	t.Helper()
	workspaceID, actorID, projectID := NewID("ws"), NewID("usr"), NewID("prj")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,$2)`, workspaceID, label)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Planning actor')`, actorID, actorID+"@example.invalid")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'admin')`, workspaceID, actorID)
	exec(`INSERT INTO projects(id,workspace_id,key,name,workflow_id) VALUES($1,$2,$3,$4,'wf_default')`,
		projectID, workspaceID, "PL"+strings.ToUpper(projectID[len(projectID)-5:]), label)
	t.Cleanup(func() {
		drop := func(query string, args ...any) { _, _ = st.Pool.Exec(ctx, query, args...) }
		drop(`DELETE FROM sprint_issues WHERE sprint_id IN (SELECT s.id FROM sprints s JOIN boards b ON b.id=s.board_id WHERE b.project_id=$1)`, projectID)
		drop(`DELETE FROM sprints WHERE board_id IN (SELECT id FROM boards WHERE project_id=$1)`, projectID)
		drop(`DELETE FROM boards WHERE project_id=$1`, projectID)
		drop(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		drop(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		drop(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		drop(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		drop(`DELETE FROM custom_fields WHERE workspace_id=$1`, workspaceID)
		drop(`DELETE FROM jira_site_configuration WHERE workspace_id=$1`, workspaceID)
		drop(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		drop(`DELETE FROM users WHERE id=$1`, actorID)
	})
	board, err := st.CreateBoard(ctx, actorID, workspaceID, BoardCreate{Name: label + " board", Type: "scrum", ProjectID: projectID})
	if err != nil {
		t.Fatal(err)
	}
	return workspaceID, actorID, projectID, board
}

func TestBacklogSprintLifecycleAndExclusivePlanningMembership(t *testing.T) {
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
	workspaceID, actorID, projectID, board := planningFixture(ctx, t, st, "Backlog lifecycle")

	issueOne, _, err := st.CreateIssue(ctx, actorID, projectID, "backlog lifecycle one",
		json.RawMessage(`{"type":"doc","version":1,"content":[]}`), "st_todo", "it_task", "pr_medium", "", nil, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	issueTwo, _, err := st.CreateIssue(ctx, actorID, projectID, "backlog lifecycle two",
		json.RawMessage(`{"type":"doc","version":1,"content":[]}`), "st_todo", "it_task", "pr_medium", "", nil, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	sprintOne, sprintAction, err := st.CreateSprint(ctx, actorID, workspaceID, board.ID, "Lifecycle one", "First outcome")
	if err != nil {
		t.Fatal(err)
	}
	var sprintPayload models.SprintUpsertPayload
	if err := json.Unmarshal(sprintAction.Payload, &sprintPayload); err != nil {
		t.Fatal(err)
	}
	if sprintPayload.Sprint.BoardID != board.ID {
		t.Fatalf("sprint action board id = %q, want %q", sprintPayload.Sprint.BoardID, board.ID)
	}
	sprintTwo, _, err := st.CreateSprint(ctx, actorID, workspaceID, board.ID, "Lifecycle two", "Second outcome")
	if err != nil {
		t.Fatal(err)
	}

	backlog, err := st.BacklogIssues(ctx, board.ID, actorID)
	if err != nil {
		t.Fatal(err)
	}
	if !issueSliceContains(backlog, issueOne.ID) || !issueSliceContains(backlog, issueTwo.ID) {
		t.Fatalf("new issues not visible in backlog: %#v", backlog)
	}

	rank, err := st.NextSprintRank(ctx, sprintOne.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddIssueToSprint(ctx, actorID, workspaceID, sprintOne.ID, issueOne.ID, rank); err != nil {
		t.Fatal(err)
	}
	rank, err = st.NextSprintRank(ctx, sprintTwo.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddIssueToSprint(ctx, actorID, workspaceID, sprintTwo.ID, issueOne.ID, rank); err != nil {
		t.Fatal(err)
	}
	firstSprintIssues, err := st.IssuesBySprint(ctx, sprintOne.ID, actorID)
	if err != nil {
		t.Fatal(err)
	}
	if issueSliceContains(firstSprintIssues, issueOne.ID) {
		t.Fatal("moving an issue left it assigned to two open sprints")
	}
	secondSprintIssues, err := st.IssuesBySprint(ctx, sprintTwo.ID, actorID)
	if err != nil || !issueSliceContains(secondSprintIssues, issueOne.ID) {
		t.Fatalf("destination sprint membership missing: issues=%#v err=%v", secondSprintIssues, err)
	}

	start := time.Now().UTC().Truncate(time.Second)
	end := start.Add(14 * 24 * time.Hour)
	if _, _, err := st.UpdateSprint(ctx, actorID, workspaceID, sprintOne.ID, SprintUpdate{
		Name: sprintOne.Name, Goal: sprintOne.Goal, State: "active", StartDate: &start, EndDate: &end,
	}); err != nil {
		t.Fatalf("start first sprint: %v", err)
	}
	if _, _, err := st.UpdateSprint(ctx, actorID, workspaceID, sprintTwo.ID, SprintUpdate{
		Name: sprintTwo.Name, Goal: sprintTwo.Goal, State: "active", StartDate: &start, EndDate: &end,
	}); !errors.Is(err, ErrSprintConflict) {
		t.Fatalf("parallel active sprint error = %v, want ErrSprintConflict", err)
	}
	if _, _, err := st.UpdateSprint(ctx, actorID, workspaceID, sprintOne.ID, SprintUpdate{
		Name: sprintOne.Name, Goal: sprintOne.Goal, State: "closed", StartDate: &start, EndDate: &end,
	}); err != nil {
		t.Fatalf("complete first sprint: %v", err)
	}
	if _, _, err := st.UpdateSprint(ctx, actorID, workspaceID, sprintOne.ID, SprintUpdate{
		Name: sprintOne.Name, Goal: sprintOne.Goal, State: "active", StartDate: &start, EndDate: &end,
	}); !errors.Is(err, ErrSprintValidation) {
		t.Fatalf("reopening closed sprint error = %v, want ErrSprintValidation", err)
	}

	if _, _, err := st.UpdateSprint(ctx, actorID, workspaceID, sprintTwo.ID, SprintUpdate{
		Name: sprintTwo.Name, Goal: sprintTwo.Goal, State: "active", StartDate: &start, EndDate: &end,
	}); err != nil {
		t.Fatalf("start second sprint: %v", err)
	}
	if err := st.RemoveIssueFromPlanning(ctx, actorID, workspaceID, issueOne.ID); err != nil {
		t.Fatal(err)
	}
	backlog, err = st.BacklogIssues(ctx, board.ID, actorID)
	if err != nil || !issueSliceContains(backlog, issueOne.ID) {
		t.Fatalf("issue was not returned to backlog: issues=%#v err=%v", backlog, err)
	}
}

func issueSliceContains(issues []*models.Issue, issueID string) bool {
	for _, issue := range issues {
		if issue.ID == issueID {
			return true
		}
	}
	return false
}

// Jira Software runs one sprint at a time on a board unless the site turns on
// parallel sprints. This drives both sides of that switch.
func TestParallelSprintsSwitchGovernsASecondActiveSprint(t *testing.T) {
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
	workspaceID, actorID, _, board := planningFixture(ctx, t, st, "Parallel sprints")

	first, _, err := st.CreateSprint(ctx, actorID, workspaceID, board.ID, "Parallel one", "First outcome")
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := st.CreateSprint(ctx, actorID, workspaceID, board.ID, "Parallel two", "Second outcome")
	if err != nil {
		t.Fatal(err)
	}

	configuration, err := st.JiraSiteConfiguration(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if configuration.ParallelSprintsEnabled {
		t.Fatal("parallel sprints default = true, want false as Jira leaves it")
	}
	setParallel := func(on bool) {
		t.Helper()
		value := *configuration
		value.ParallelSprintsEnabled = on
		if err := st.UpdateGlobalJiraConfiguration(ctx, workspaceID, actorID, value); err != nil {
			t.Fatal(err)
		}
	}

	start := time.Now().UTC().Truncate(time.Second)
	end := start.Add(14 * 24 * time.Hour)
	active := func(sprint *models.Sprint) error {
		_, _, err := st.UpdateSprint(ctx, actorID, workspaceID, sprint.ID, SprintUpdate{
			Name: sprint.Name, Goal: sprint.Goal, State: "active", StartDate: &start, EndDate: &end,
		})
		return err
	}

	if err := active(first); err != nil {
		t.Fatalf("start the first sprint: %v", err)
	}
	if err := active(second); !errors.Is(err, ErrSprintConflict) {
		t.Fatalf("second sprint with the switch off = %v, want ErrSprintConflict", err)
	}

	setParallel(true)
	if err := active(second); err != nil {
		t.Fatalf("second sprint with the switch on: %v", err)
	}
	var activeCount int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM sprints WHERE board_id=$1 AND state='active'`, board.ID).Scan(&activeCount); err != nil {
		t.Fatal(err)
	}
	if activeCount != 2 {
		t.Fatalf("active sprints on the board = %d, want 2", activeCount)
	}

	// The switch governs only the parallel case: the state machine still
	// refuses a move Jira does not allow.
	if _, _, err := st.UpdateSprint(ctx, actorID, workspaceID, first.ID, SprintUpdate{
		Name: first.Name, Goal: first.Goal, State: "future", StartDate: &start, EndDate: &end,
	}); !errors.Is(err, ErrSprintValidation) {
		t.Fatalf("active to future = %v, want ErrSprintValidation", err)
	}
}
