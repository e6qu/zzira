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

func TestProgressReportReplaysEstimatesByDay(t *testing.T) {
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
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Progress test')`, workspaceID)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Progress actor')`, actorID, actorID+"@example.invalid")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'member')`, workspaceID, actorID)
	exec(`INSERT INTO projects(id,workspace_id,key,name,workflow_id) VALUES($1,$2,$3,'Progress project','wf_default')`, projectID, workspaceID, "PG"+strings.ToUpper(projectID[len(projectID)-5:]))
	t.Cleanup(func() {
		exec(`DELETE FROM boards WHERE project_id=$1`, projectID)
		exec(`UPDATE issues SET parent_id=NULL WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM custom_fields WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		exec(`DELETE FROM users WHERE id=$1`, actorID)
	})
	board, err := st.CreateBoard(ctx, actorID, workspaceID, BoardCreate{Name: "Progress board", Type: "scrum", ProjectID: projectID})
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
	day := func(offset, hour int) time.Time { return time.Date(2026, 9, 1+offset, hour, 0, 0, 0, time.UTC) }
	stamp := func(issueID string, seq int64, at time.Time) {
		t.Helper()
		if seq == 0 {
			exec(`UPDATE issues SET created_at=$2 WHERE id=$1`, issueID, at)
			exec(`UPDATE actions SET created_at=$3 WHERE workspace_id=$1 AND entity_id=$2`, workspaceID, issueID, at)
			return
		}
		exec(`UPDATE actions SET created_at=$3 WHERE workspace_id=$1 AND seq=$2`, workspaceID, seq, at)
	}
	epic, _, err := st.CreateIssue(ctx, actorID, projectID, "Checkout", json.RawMessage(`{"type":"doc","version":1,"content":[]}`), "st_todo", "it_epic", "pr_medium", "", nil, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	stamp(epic.ID, 0, day(0, 8))
	child := func(summary, points string, at time.Time) *models.Issue {
		t.Helper()
		issue, _, err := st.CreateIssue(ctx, actorID, projectID, summary, json.RawMessage(`{"type":"doc","version":1,"content":[]}`), "st_todo", "it_task", "pr_medium", "", nil, map[string]json.RawMessage{fieldID: json.RawMessage(points)}, "", epic.ID)
		if err != nil {
			t.Fatal(err)
		}
		stamp(issue.ID, 0, at)
		return issue
	}
	cart := child("Cart", "3", day(0, 9))
	pay := child("Pay", "5", day(1, 9))
	done := "st_done"
	_, action, err := st.UpdateIssue(ctx, actorID, workspaceID, cart.ID, IssueUpdate{StatusID: &done})
	if err != nil {
		t.Fatal(err)
	}
	stamp(cart.ID, action.Seq, day(1, 12))
	_, action, err = st.UpdateIssue(ctx, actorID, workspaceID, pay.ID, IssueUpdate{Fields: map[string]json.RawMessage{fieldID: json.RawMessage(`8`)}})
	if err != nil {
		t.Fatal(err)
	}
	stamp(pay.ID, action.Seq, day(2, 10))
	unestimated := child("Receipt", "null", day(2, 11))

	children, err := st.EpicChildren(ctx, workspaceID, actorID, []string{epic.ID})
	if err != nil || len(children) != 3 {
		t.Fatalf("epic children = %d, %v", len(children), err)
	}
	report, err := st.ProgressReport(ctx, workspaceID, board, children, day(0, 0), day(2, 18))
	if err != nil {
		t.Fatal(err)
	}
	want := []models.ProgressPoint{{Date: "2026-09-01", Total: 3}, {Date: "2026-09-02", Total: 8, Completed: 3}, {Date: "2026-09-03", Total: 11, Completed: 3}}
	if len(report.Points) != len(want) {
		t.Fatalf("points = %+v", report.Points)
	}
	for index, point := range want {
		if report.Points[index] != point {
			t.Fatalf("point %d = %+v, want %+v", index, report.Points[index], point)
		}
	}
	if report.Statistic != "Story point estimate" || report.TotalEstimate != 11 || report.CompletedEstimate != 3 || report.Unestimated != 1 || report.Progress.Done != 1 || report.Progress.Total() != 3 {
		t.Fatalf("report = %+v", report)
	}
	if len(report.Completed) != 1 || report.Completed[0].Key != cart.Key || len(report.Incomplete) != 2 || report.Incomplete[1].Key != unestimated.Key || report.Incomplete[1].EstimateEnd != "" {
		t.Fatalf("report work = %+v / %+v", report.Completed, report.Incomplete)
	}
}
