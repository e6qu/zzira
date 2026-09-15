package store

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/e6qu/zzira/internal/models"
)

func TestProjectTimelineSchedulesEpicsAndChildWork(t *testing.T) {
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
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Timeline test')`, workspaceID)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Timeline actor')`, actorID, actorID+"@example.invalid")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'member')`, workspaceID, actorID)
	exec(`INSERT INTO projects(id,workspace_id,key,name,workflow_id) VALUES($1,$2,$3,'Timeline project','wf_default')`, projectID, workspaceID, "TL"+projectID[len(projectID)-5:])
	t.Cleanup(func() {
		exec(`UPDATE issues SET parent_id=NULL WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM custom_fields WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		exec(`DELETE FROM users WHERE id=$1`, actorID)
	})
	fieldID, err := st.StartDateFieldID(ctx, workspaceID)
	if err != nil || fieldID == "" {
		t.Fatalf("start date field = %q, %v", fieldID, err)
	}
	create := func(summary, issueType, parentID string, fields map[string]json.RawMessage) *models.Issue {
		t.Helper()
		issue, _, err := st.CreateIssue(ctx, actorID, projectID, summary, json.RawMessage(`{"type":"doc","version":1,"content":[]}`),
			"st_todo", issueType, "pr_medium", "", nil, fields, "", parentID)
		if err != nil {
			t.Fatal(err)
		}
		return issue
	}
	epic := create("Launch", "it_epic", "", map[string]json.RawMessage{fieldID: json.RawMessage(`"2026-10-01"`)})
	due := "2026-10-31"
	if _, _, err := st.UpdateIssue(ctx, actorID, workspaceID, epic.ID, IssueUpdate{DueDate: &due}); err != nil {
		t.Fatal(err)
	}
	child := create("Build", "it_task", epic.ID, map[string]json.RawMessage{fieldID: json.RawMessage(`"2026-10-05"`)})
	childDue := "2026-10-12"
	if _, _, err := st.UpdateIssue(ctx, actorID, workspaceID, child.ID, IssueUpdate{DueDate: &childDue}); err != nil {
		t.Fatal(err)
	}
	unscheduled := create("Polish", "it_task", epic.ID, nil)
	create("Detail", "it_subtask", child.ID, nil)
	other := create("Maintenance", "it_task", "", nil)
	_ = other

	timeline, err := st.ProjectTimeline(ctx, workspaceID, actorID, projectID)
	if err != nil {
		t.Fatal(err)
	}
	if timeline.StartFieldID != fieldID || len(timeline.Epics) != 1 {
		t.Fatalf("timeline = %+v", timeline)
	}
	scheduledEpic := timeline.Epics[0]
	if scheduledEpic.Issue.Key != epic.Key || scheduledEpic.StartDate != "2026-10-01" || scheduledEpic.DueDate != "2026-10-31" || len(scheduledEpic.Children) != 2 {
		t.Fatalf("epic = %+v", scheduledEpic)
	}
	byKey := map[string]models.TimelineItem{}
	for _, item := range scheduledEpic.Children {
		byKey[item.Issue.Key] = item
	}
	if scheduled := byKey[child.Key]; scheduled.StartDate != "2026-10-05" || scheduled.DueDate != "2026-10-12" {
		t.Fatalf("child = %+v", scheduled)
	}
	if loose := byKey[unscheduled.Key]; loose.Issue == nil || loose.StartDate != "" || loose.DueDate != "" {
		t.Fatalf("unscheduled = %+v", loose)
	}
}
