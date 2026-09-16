package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/models"
)

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

	var actorID string
	if err := st.Pool.QueryRow(ctx, `SELECT id FROM users WHERE email='demo@zzira.dev'`).Scan(&actorID); err != nil {
		t.Skip("demo user not seeded")
	}
	issueOne, _, err := st.CreateIssue(ctx, actorID, "prj_default", "backlog lifecycle one",
		json.RawMessage(`{"type":"doc","version":1,"content":[]}`), "st_todo", "it_task", "pr_medium", "", nil, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	issueTwo, _, err := st.CreateIssue(ctx, actorID, "prj_default", "backlog lifecycle two",
		json.RawMessage(`{"type":"doc","version":1,"content":[]}`), "st_todo", "it_task", "pr_medium", "", nil, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	sprintOne, sprintAction, err := st.CreateSprint(ctx, actorID, "ws_default", "brd_default", "Lifecycle one", "First outcome")
	if err != nil {
		t.Fatal(err)
	}
	var sprintPayload models.SprintUpsertPayload
	if err := json.Unmarshal(sprintAction.Payload, &sprintPayload); err != nil {
		t.Fatal(err)
	}
	if sprintPayload.Sprint.BoardID != "brd_default" {
		t.Fatalf("sprint action board id = %q, want brd_default", sprintPayload.Sprint.BoardID)
	}
	sprintTwo, _, err := st.CreateSprint(ctx, actorID, "ws_default", "brd_default", "Lifecycle two", "Second outcome")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM sprint_issues WHERE sprint_id = ANY($1)`, []string{sprintOne.ID, sprintTwo.ID})
		_, _ = st.Pool.Exec(ctx, `DELETE FROM sprints WHERE id = ANY($1)`, []string{sprintOne.ID, sprintTwo.ID})
		_, _ = st.Pool.Exec(ctx, `DELETE FROM issues WHERE id = ANY($1)`, []string{issueOne.ID, issueTwo.ID})
	})

	backlog, err := st.BacklogIssues(ctx, "brd_default", actorID)
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
	if _, err := st.AddIssueToSprint(ctx, actorID, "ws_default", sprintOne.ID, issueOne.ID, rank); err != nil {
		t.Fatal(err)
	}
	rank, err = st.NextSprintRank(ctx, sprintTwo.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddIssueToSprint(ctx, actorID, "ws_default", sprintTwo.ID, issueOne.ID, rank); err != nil {
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
	if _, _, err := st.UpdateSprint(ctx, actorID, "ws_default", sprintOne.ID, SprintUpdate{
		Name: sprintOne.Name, Goal: sprintOne.Goal, State: "active", StartDate: &start, EndDate: &end,
	}); err != nil {
		t.Fatalf("start first sprint: %v", err)
	}
	if _, _, err := st.UpdateSprint(ctx, actorID, "ws_default", sprintTwo.ID, SprintUpdate{
		Name: sprintTwo.Name, Goal: sprintTwo.Goal, State: "active", StartDate: &start, EndDate: &end,
	}); !errors.Is(err, ErrSprintConflict) {
		t.Fatalf("parallel active sprint error = %v, want ErrSprintConflict", err)
	}
	if _, _, err := st.UpdateSprint(ctx, actorID, "ws_default", sprintOne.ID, SprintUpdate{
		Name: sprintOne.Name, Goal: sprintOne.Goal, State: "closed", StartDate: &start, EndDate: &end,
	}); err != nil {
		t.Fatalf("complete first sprint: %v", err)
	}
	if _, _, err := st.UpdateSprint(ctx, actorID, "ws_default", sprintOne.ID, SprintUpdate{
		Name: sprintOne.Name, Goal: sprintOne.Goal, State: "active", StartDate: &start, EndDate: &end,
	}); !errors.Is(err, ErrSprintValidation) {
		t.Fatalf("reopening closed sprint error = %v, want ErrSprintValidation", err)
	}

	if _, _, err := st.UpdateSprint(ctx, actorID, "ws_default", sprintTwo.ID, SprintUpdate{
		Name: sprintTwo.Name, Goal: sprintTwo.Goal, State: "active", StartDate: &start, EndDate: &end,
	}); err != nil {
		t.Fatalf("start second sprint: %v", err)
	}
	if err := st.RemoveIssueFromPlanning(ctx, actorID, "ws_default", issueOne.ID); err != nil {
		t.Fatal(err)
	}
	backlog, err = st.BacklogIssues(ctx, "brd_default", actorID)
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
// parallel sprints. This drives both sides of that switch on a board of its
// own, so the lifecycle test's board keeps its own sequence.
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
	var actorID string
	if err := st.Pool.QueryRow(ctx, `SELECT id FROM users WHERE email='demo@zzira.dev'`).Scan(&actorID); err != nil {
		t.Skip("demo user not seeded")
	}

	board, err := st.CreateBoard(ctx, actorID, "ws_default", BoardCreate{Name: "Parallel sprints " + NewID("brd"), Type: "scrum", ProjectID: "prj_default"})
	if err != nil {
		t.Fatal(err)
	}
	first, _, err := st.CreateSprint(ctx, actorID, "ws_default", board.ID, "Parallel one", "First outcome")
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := st.CreateSprint(ctx, actorID, "ws_default", board.ID, "Parallel two", "Second outcome")
	if err != nil {
		t.Fatal(err)
	}

	// The switch is restored however this test leaves, so the rest of the
	// package sees the site as it found it.
	original, err := st.JiraSiteConfiguration(ctx, "ws_default")
	if err != nil {
		t.Fatal(err)
	}
	setParallel := func(on bool) {
		t.Helper()
		cfg := *original
		cfg.ParallelSprintsEnabled = on
		if err := st.UpdateGlobalJiraConfiguration(ctx, "ws_default", actorID, cfg); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		setParallel(original.ParallelSprintsEnabled)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM sprints WHERE board_id=$1`, board.ID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM boards WHERE id=$1`, board.ID)
	})

	start := time.Now().UTC().Truncate(time.Second)
	end := start.Add(14 * 24 * time.Hour)
	active := func(sprint *models.Sprint) error {
		_, _, err := st.UpdateSprint(ctx, actorID, "ws_default", sprint.ID, SprintUpdate{
			Name: sprint.Name, Goal: sprint.Goal, State: "active", StartDate: &start, EndDate: &end,
		})
		return err
	}

	setParallel(false)
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
	if _, _, err := st.UpdateSprint(ctx, actorID, "ws_default", first.ID, SprintUpdate{
		Name: first.Name, Goal: first.Goal, State: "future", StartDate: &start, EndDate: &end,
	}); !errors.Is(err, ErrSprintValidation) {
		t.Fatalf("active to future = %v, want ErrSprintValidation", err)
	}
}
