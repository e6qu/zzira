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
		json.RawMessage(`{"type":"doc","version":1,"content":[]}`), "st_todo", "it_task", "pr_medium", "", nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	issueTwo, _, err := st.CreateIssue(ctx, actorID, "prj_default", "backlog lifecycle two",
		json.RawMessage(`{"type":"doc","version":1,"content":[]}`), "st_todo", "it_task", "pr_medium", "", nil, nil, "")
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
