package store

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/models"
)

func TestSprintReportAndVelocityReplayTheSprint(t *testing.T) {
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
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Sprint report test')`, workspaceID)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Report actor')`, actorID, actorID+"@example.invalid")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'member')`, workspaceID, actorID)
	exec(`INSERT INTO projects(id,workspace_id,key,name,workflow_id) VALUES($1,$2,$3,'Report project','wf_default')`, projectID, workspaceID, "SR"+projectID[len(projectID)-5:])
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
	board, err := st.CreateBoard(ctx, actorID, workspaceID, BoardCreate{Name: "Sprint report board", Type: "scrum", ProjectID: projectID})
	if err != nil {
		t.Fatal(err)
	}
	var fieldID string
	if err := st.Pool.QueryRow(ctx, `
		UPDATE boards SET estimation_field_id=(SELECT id FROM custom_fields WHERE workspace_id=$2 AND app_installation_id IS NULL AND name='Story point estimate')
		WHERE id=$1 RETURNING estimation_field_id`, board.ID, workspaceID).Scan(&fieldID); err != nil {
		t.Fatal(err)
	}
	if board, err = st.BoardByID(ctx, board.ID); err != nil {
		t.Fatal(err)
	}

	create := func(summary string, points float64) *models.Issue {
		t.Helper()
		issue, _, err := st.CreateIssue(ctx, actorID, projectID, summary, json.RawMessage(`{"type":"doc","version":1,"content":[]}`),
			"st_todo", "it_task", "pr_medium", "", nil, map[string]json.RawMessage{fieldID: json.RawMessage(formatEstimate(&points))}, "", "")
		if err != nil {
			t.Fatal(err)
		}
		return issue
	}
	update := func(issue *models.Issue, up IssueUpdate) {
		t.Helper()
		if _, _, err := st.UpdateIssue(ctx, actorID, workspaceID, issue.ID, up); err != nil {
			t.Fatal(err)
		}
	}
	done, todo := "st_done", "st_todo"
	plan := func(sprintID string, issue *models.Issue) {
		t.Helper()
		rank, err := st.NextSprintRank(ctx, sprintID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.AddIssueToSprint(ctx, actorID, workspaceID, sprintID, issue.ID, rank); err != nil {
			t.Fatal(err)
		}
	}
	sprint, _, err := st.CreateSprint(ctx, actorID, workspaceID, board.ID, "Report sprint", "Replay the sprint")
	if err != nil {
		t.Fatal(err)
	}

	shipped := create("report shipped", 3)
	grown := create("report grown", 5)
	already := create("report already done", 2)
	late := create("report added late", 1)
	dropped := create("report dropped", 8)
	update(already, IssueUpdate{StatusID: &done})
	for _, issue := range []*models.Issue{shipped, grown, already, dropped} {
		plan(sprint.ID, issue)
	}
	startDate, endDate := time.Now().UTC().Add(-time.Hour), time.Now().UTC().Add(13*24*time.Hour)
	if _, _, err := st.UpdateSprint(ctx, actorID, workspaceID, sprint.ID, SprintUpdate{Name: sprint.Name, Goal: sprint.Goal, State: "active", StartDate: &startDate, EndDate: &endDate}); err != nil {
		t.Fatal(err)
	}
	plan(sprint.ID, late)
	update(shipped, IssueUpdate{StatusID: &done})
	update(grown, IssueUpdate{Fields: map[string]json.RawMessage{fieldID: json.RawMessage(`8`)}})
	if err := st.RemoveIssueFromPlanning(ctx, actorID, workspaceID, dropped.ID); err != nil {
		t.Fatal(err)
	}
	// Reopening and finishing again inside the sprint still counts once.
	update(shipped, IssueUpdate{StatusID: &todo})
	update(shipped, IssueUpdate{StatusID: &done})
	closed, _, err := st.UpdateSprint(ctx, actorID, workspaceID, sprint.ID, SprintUpdate{Name: sprint.Name, Goal: sprint.Goal, State: "closed", StartDate: &startDate, EndDate: &endDate})
	if err != nil {
		t.Fatal(err)
	}
	if closed.ActivatedDate == "" || closed.CompleteDate == "" {
		t.Fatalf("closed sprint dates = %+v", closed)
	}
	stored, err := st.SprintByID(ctx, sprint.ID)
	if err != nil || stored.CompleteDate != closed.CompleteDate || stored.ActivatedDate != closed.ActivatedDate || stored.CreatedDate == "" {
		t.Fatalf("stored sprint = %+v, %v", stored, err)
	}
	// Planning after completion must not change the completed report.
	later := create("report after close", 13)
	_ = later

	report, err := st.SprintReport(ctx, workspaceID, actorID, board, stored, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	keys := func(issues []models.SprintReportIssue) []string {
		out := make([]string, 0, len(issues))
		for _, issue := range issues {
			out = append(out, issue.Key)
		}
		return out
	}
	expect := func(name string, got []models.SprintReportIssue, want ...*models.Issue) {
		t.Helper()
		gotKeys := keys(got)
		if len(gotKeys) != len(want) {
			t.Fatalf("%s = %v, want %d items; report %+v", name, gotKeys, len(want), report)
		}
		for index, issue := range want {
			if gotKeys[index] != issue.Key {
				t.Fatalf("%s = %v, want %s at %d", name, gotKeys, issue.Key, index)
			}
		}
	}
	expect("completed", report.Completed, shipped)
	expect("not completed", report.NotCompleted, grown, late)
	expect("completed outside", report.CompletedOutside, already)
	expect("removed", report.Removed, dropped)
	if report.Statistic != "Story point estimate" || report.CompletedTotal != 3 || report.NotCompletedTotal != 9 || report.RemovedTotal != 8 {
		t.Fatalf("report totals = %+v", report)
	}
	if growth := report.NotCompleted[0]; growth.EstimateStart != "5" || growth.EstimateEnd != "8" || growth.AddedAfterStart {
		t.Fatalf("grown row = %+v", growth)
	}
	if !report.NotCompleted[1].AddedAfterStart {
		t.Fatalf("late row = %+v", report.NotCompleted[1])
	}

	type step struct {
		label     string
		remaining float64
	}
	want := []step{
		{"Sprint start", 16},
		{"Added to sprint", 17},
		{"Work completed", 14},
		{"Estimate changed from 5 to 8", 17},
		{"Removed from sprint", 9},
		{"Work reopened", 12},
		{"Work completed", 9},
		{"Sprint completed", 9},
	}
	if len(report.Burndown) != len(want) {
		t.Fatalf("burndown = %+v", report.Burndown)
	}
	for index, expected := range want {
		if got := report.Burndown[index]; got.Label != expected.label || got.Remaining != expected.remaining {
			t.Fatalf("burndown[%d] = %+v, want %+v; all %+v", index, got, expected, report.Burndown)
		}
	}
	if report.Burndown[0].Scope != 18 {
		t.Fatalf("starting scope = %v", report.Burndown[0].Scope)
	}

	velocity, err := st.VelocityReport(ctx, workspaceID, actorID, board)
	if err != nil {
		t.Fatal(err)
	}
	if velocity.Statistic != "Story point estimate" || len(velocity.Sprints) != 1 || velocity.Sprints[0].Commitment != 18 || velocity.Sprints[0].Completed != 3 {
		t.Fatalf("velocity = %+v", velocity)
	}

	// Without an estimation field every work item counts once.
	if _, err := st.Pool.Exec(ctx, `UPDATE boards SET estimation_field_id=NULL WHERE id=$1`, board.ID); err != nil {
		t.Fatal(err)
	}
	if board, err = st.BoardByID(ctx, board.ID); err != nil {
		t.Fatal(err)
	}
	counted, err := st.SprintReport(ctx, workspaceID, actorID, board, stored, time.Now())
	if err != nil || counted.Statistic != "Issue count" || counted.CompletedTotal != 1 || counted.NotCompletedTotal != 2 {
		t.Fatalf("issue count report = %+v, %v", counted, err)
	}
}
