package store

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestCumulativeFlowAndControlChartReplayStatuses(t *testing.T) {
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
	workspaceID, actorID, projectID := NewID("ws"), NewID("usr"), NewID("prj")
	// Jira project keys are upper case, which the board filter relies on.
	projectKey := "CF" + strings.ToUpper(projectID[len(projectID)-5:])
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Flow test')`, workspaceID)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Flow actor')`, actorID, actorID+"@example.invalid")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'member')`, workspaceID, actorID)
	exec(`INSERT INTO projects(id,workspace_id,key,name,workflow_id) VALUES($1,$2,$3,'Flow project','wf_default')`, projectID, workspaceID, projectKey)
	t.Cleanup(func() {
		exec(`DELETE FROM boards WHERE project_id=$1`, projectID)
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM custom_fields WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		exec(`DELETE FROM users WHERE id=$1`, actorID)
	})
	board, err := st.CreateBoard(ctx, actorID, workspaceID, BoardCreate{Name: "Flow board", Type: "kanban", ProjectID: projectID})
	if err != nil {
		t.Fatal(err)
	}
	day := func(offset int, hour int) time.Time {
		return time.Date(2026, 9, 10+offset, hour, 0, 0, 0, time.UTC)
	}
	// Each change is stamped at a known time so the replay is exact.
	create := func(summary string, at time.Time) string {
		t.Helper()
		issue, _, err := st.CreateIssue(ctx, actorID, projectID, summary, json.RawMessage(`{"type":"doc","version":1,"content":[]}`), "st_todo", "it_task", "pr_medium", "", nil, nil, "", "")
		if err != nil {
			t.Fatal(err)
		}
		exec(`UPDATE issues SET created_at=$2 WHERE id=$1`, issue.ID, at)
		exec(`UPDATE actions SET created_at=$3 WHERE workspace_id=$1 AND entity_id=$2`, workspaceID, issue.ID, at)
		return issue.ID
	}
	move := func(issueID, statusID string, at time.Time) {
		t.Helper()
		status := statusID
		_, action, err := st.UpdateIssue(ctx, actorID, workspaceID, issueID, IssueUpdate{StatusID: &status})
		if err != nil {
			t.Fatal(err)
		}
		exec(`UPDATE actions SET created_at=$3 WHERE workspace_id=$1 AND seq=$2`, workspaceID, action.Seq, at)
	}
	fast := create("Fast", day(0, 9))
	move(fast, "st_inprogress", day(0, 10))
	move(fast, "st_done", day(0, 16))
	slow := create("Slow", day(0, 9))
	move(slow, "st_inprogress", day(1, 9))
	move(slow, "st_done", day(2, 9))
	// Reopened work only counts from the move into done that completes it.
	reopened := create("Reopened", day(1, 8))
	move(reopened, "st_inprogress", day(1, 9))
	move(reopened, "st_done", day(1, 10))
	move(reopened, "st_inprogress", day(1, 11))
	waiting := create("Waiting", day(2, 8))
	_ = waiting
	skipped := create("Skipped", day(0, 12))
	move(skipped, "st_done", day(0, 13))

	now := day(2, 18)
	flow, err := st.CumulativeFlow(ctx, board, actorID, 3, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(flow.Columns) != 3 || flow.Columns[0].StatusID != "st_todo" || len(flow.Days) != 3 {
		t.Fatalf("flow = %+v", flow)
	}
	want := map[string][]int{
		"2026-09-10": {1, 0, 2},
		"2026-09-11": {0, 2, 2},
		"2026-09-12": {1, 1, 3},
	}
	for _, flowDay := range flow.Days {
		counts := want[flowDay.Date]
		if len(counts) != 3 || flowDay.Counts[0] != counts[0] || flowDay.Counts[1] != counts[1] || flowDay.Counts[2] != counts[2] {
			t.Fatalf("flow %s = %v, want %v", flowDay.Date, flowDay.Counts, counts)
		}
	}

	chart, err := st.ControlChart(ctx, board, actorID, 3, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(chart.Samples) != 2 || chart.Samples[0].CycleSeconds != 6*3600 || chart.Samples[1].CycleSeconds != 24*3600 {
		t.Fatalf("control chart = %+v", chart)
	}
	if chart.AverageSeconds != 15*3600 || chart.MedianSeconds != 15*3600 {
		t.Fatalf("control chart summary = %+v", chart)
	}
	// Completed work older than the window leaves the chart.
	recent, err := st.ControlChart(ctx, board, actorID, 1, now)
	if err != nil || len(recent.Samples) != 1 || recent.Samples[0].CycleSeconds != 24*3600 {
		t.Fatalf("recent control chart = %+v, %v", recent, err)
	}
}
