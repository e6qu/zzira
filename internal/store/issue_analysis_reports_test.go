package store

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestCreatedVsResolvedAndResolutionTime(t *testing.T) {
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
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Analysis test')`, workspaceID)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Analysis actor')`, actorID, actorID+"@example.invalid")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'member')`, workspaceID, actorID)
	exec(`INSERT INTO projects(id,workspace_id,key,name,workflow_id) VALUES($1,$2,$3,'Analysis project','wf_default')`, projectID, workspaceID, "AN"+strings.ToUpper(projectID[len(projectID)-5:]))
	t.Cleanup(func() {
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM custom_fields WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		exec(`DELETE FROM users WHERE id=$1`, actorID)
	})
	day := func(offset, hour int) time.Time { return time.Date(2026, 9, 10+offset, hour, 0, 0, 0, time.UTC) }
	create := func(created time.Time, resolved *time.Time) {
		t.Helper()
		issue, _, err := st.CreateIssue(ctx, actorID, projectID, "Analysed", json.RawMessage(`{"type":"doc","version":1,"content":[]}`), "st_todo", "it_task", "pr_medium", "", nil, nil, "", "")
		if err != nil {
			t.Fatal(err)
		}
		exec(`UPDATE issues SET created_at=$2, resolved_at=$3 WHERE id=$1`, issue.ID, created, resolved)
	}
	resolvedAt := func(at time.Time) *time.Time { return &at }
	create(day(0, 9), resolvedAt(day(0, 15)))  // six hours
	create(day(0, 10), resolvedAt(day(2, 10))) // two days
	create(day(1, 9), nil)
	create(day(2, 9), resolvedAt(day(2, 11))) // two hours
	create(day(-5, 9), resolvedAt(day(1, 9))) // created before the window

	now := day(2, 20)
	flow, err := st.CreatedVsResolved(ctx, workspaceID, actorID, projectID, 3, now)
	if err != nil {
		t.Fatal(err)
	}
	type counts struct{ created, resolved, createdTotal, resolvedTotal int }
	want := []counts{{2, 1, 2, 1}, {1, 1, 3, 2}, {1, 2, 4, 4}}
	if len(flow.Days) != 3 || flow.Days[0].Date != "2026-09-10" || flow.CreatedTotal != 4 || flow.ResolvedTotal != 4 {
		t.Fatalf("created vs resolved = %+v", flow)
	}
	for index, expected := range want {
		got := flow.Days[index]
		if (counts{got.Created, got.Resolved, got.CreatedTotal, got.ResolvedTotal}) != expected {
			t.Fatalf("day %d = %+v, want %+v", index, got, expected)
		}
	}

	resolution, err := st.ResolutionTime(ctx, workspaceID, actorID, projectID, 3, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolution.Days) != 3 || resolution.Resolved != 4 {
		t.Fatalf("resolution time = %+v", resolution)
	}
	if resolution.Days[0].AverageSeconds != 6*3600 || resolution.Days[1].AverageSeconds != 6*24*3600 || resolution.Days[2].AverageSeconds != 25*3600 {
		t.Fatalf("daily resolution = %+v", resolution.Days)
	}
	if resolution.AverageSeconds != (6*3600+6*24*3600+48*3600+2*3600)/4 {
		t.Fatalf("average resolution = %d", resolution.AverageSeconds)
	}
}
