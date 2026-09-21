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

func TestPlanWorkGathersSourcesExclusionsDatesAndAccess(t *testing.T) {
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
	workspaceID, leadID, viewerID, strangerID, projectID := NewID("ws"), NewID("usr"), NewID("usr"), NewID("usr"), NewID("prj")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Plan test')`, workspaceID)
	for _, id := range []string{leadID, viewerID, strangerID} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test',$1)`, id, id+"@example.invalid")
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'member')`, workspaceID, id)
	}
	exec(`INSERT INTO projects(id,workspace_id,key,name,workflow_id) VALUES($1,$2,$3,'Plan project','wf_default')`, projectID, workspaceID, "PL"+strings.ToUpper(projectID[len(projectID)-5:]))
	otherProjectID := NewID("prj")
	otherKey := "PF" + strings.ToUpper(otherProjectID[len(otherProjectID)-5:])
	exec(`INSERT INTO projects(id,workspace_id,key,name,workflow_id) VALUES($1,$2,$3,'Filtered project','wf_default')`, otherProjectID, workspaceID, otherKey)
	t.Cleanup(func() {
		exec(`DELETE FROM plans WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM filters WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM boards WHERE project_id=$1`, projectID)
		exec(`UPDATE issues SET parent_id=NULL WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM custom_fields WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		for _, id := range []string{leadID, viewerID, strangerID} {
			exec(`DELETE FROM users WHERE id=$1`, id)
		}
	})
	board, err := st.CreateBoard(ctx, leadID, workspaceID, BoardCreate{Name: "Plan board", Type: "scrum", ProjectID: projectID})
	if err != nil {
		t.Fatal(err)
	}
	targetStart, err := st.siteDateFieldID(ctx, workspaceID, "Target start")
	if err != nil || targetStart == "" {
		t.Fatalf("target start field = %q, %v", targetStart, err)
	}
	targetEnd, err := st.siteDateFieldID(ctx, workspaceID, "Target end")
	if err != nil || targetEnd == "" {
		t.Fatalf("target end field = %q, %v", targetEnd, err)
	}
	create := func(summary, issueType, parentID string, fields map[string]json.RawMessage) *models.Issue {
		t.Helper()
		issue, _, err := st.CreateIssue(ctx, leadID, projectID, summary, json.RawMessage(`{"type":"doc","version":1,"content":[]}`), "st_todo", issueType, "pr_medium", "", nil, fields, "", parentID)
		if err != nil {
			t.Fatal(err)
		}
		return issue
	}
	dates := func(start, end string) map[string]json.RawMessage {
		return map[string]json.RawMessage{targetStart: json.RawMessage(`"` + start + `"`), targetEnd: json.RawMessage(`"` + end + `"`)}
	}
	// A level above the epic, so the plan has a hierarchy to roll up through.
	initiativeType := NewID("it")
	exec(`INSERT INTO issue_types(id,name,icon,subtask,workspace_id,hierarchy_level) VALUES($1,'Initiative','',false,$2,2)`, initiativeType, workspaceID)
	initiative := create("Grow the platform", initiativeType, "", dates("2026-09-01", "2027-03-31"))
	epic := create("Platform", "it_epic", initiative.ID, dates("2026-10-01", "2026-12-31"))
	story := create("Migrate", "it_story", epic.ID, dates("2026-10-05", "2026-10-30"))
	create("Detail", "it_subtask", story.ID, nil)
	loose := create("Tidy", "it_task", "", nil)
	bug := create("Crash", "it_bug", "", nil)
	stale := create("Old work", "it_task", "", nil)
	done := "st_done"
	if _, _, err := st.UpdateIssue(ctx, leadID, workspaceID, stale.ID, IssueUpdate{StatusID: &done}); err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE issues SET resolved_at=$2, updated_at=$2 WHERE id=$1`, stale.ID, time.Now().UTC().AddDate(0, 0, -60))

	filtered, _, err := st.CreateIssue(ctx, leadID, otherProjectID, "Filtered in", json.RawMessage(`{"type":"doc","version":1,"content":[]}`), "st_todo", "it_task", "pr_medium", "", nil, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	filter, err := st.CreateFilter(ctx, NewID("filter"), workspaceID, "Filtered work", "project = "+otherKey, "", leadID)
	if err != nil {
		t.Fatal(err)
	}
	var filterJiraID int64
	if err := st.Pool.QueryRow(ctx, `SELECT jira_id FROM filters WHERE id=$1`, filter.ID).Scan(&filterJiraID); err != nil {
		t.Fatal(err)
	}
	plan := Plan{
		Name: "Roadmap", LeadAccountID: leadID,
		Scheduling:     PlanScheduling{Estimation: "StoryPoints"},
		IssueSources:   []PlanIssueSource{{Type: "Board", Value: board.JiraID}, {Type: "Filter", Value: filterJiraID}},
		ExclusionRules: PlanExclusionRules{IssueTypeIDs: []int64{bug.IssueType.JiraID}, NumberOfDaysToShowCompletedIssues: 30},
		Permissions:    []PlanPermission{{Type: "View", HolderType: "AccountId", Holder: viewerID}},
	}
	NormalizePlan(&plan)
	planID, err := st.CreatePlan(ctx, workspaceID, leadID, plan)
	if err != nil {
		t.Fatal(err)
	}
	if plan, err = st.Plan(ctx, workspaceID, planID); err != nil {
		t.Fatal(err)
	}

	for _, check := range []struct {
		user       string
		view, edit bool
	}{{leadID, true, true}, {viewerID, true, false}, {strangerID, false, false}} {
		view, edit, err := st.PlanAccess(ctx, workspaceID, check.user, plan)
		if err != nil || view != check.view || edit != check.edit {
			t.Fatalf("access for %s = %v/%v, %v; want %v/%v", check.user, view, edit, err, check.view, check.edit)
		}
	}

	work, err := st.PlanWork(ctx, workspaceID, viewerID, plan, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if work.StartFieldName != "Target start" || work.EndFieldName != "Target end" {
		t.Fatalf("date fields = %q, %q", work.StartFieldName, work.EndFieldName)
	}
	// Work comes in rank order; which work and how it nests is what matters.
	items := map[string]models.TimelineItem{}
	for _, item := range work.Items {
		items[item.Issue.Key] = item
	}
	if len(work.Items) != 3 || items[initiative.Key].Issue == nil || items[loose.Key].Issue == nil || items[filtered.Key].Issue == nil {
		t.Fatalf("plan items = %+v", work.Items)
	}
	// The plan nests through every level it was given, not only the epic's.
	top := items[initiative.Key]
	if len(top.Children) != 1 || top.Children[0].Issue.Key != epic.Key {
		t.Fatalf("initiative item = %+v", top)
	}
	nested := top.Children[0]
	if nested.StartDate != "2026-10-01" || nested.DueDate != "2026-12-31" || len(nested.Children) != 1 ||
		nested.Children[0].Issue.Key != story.Key || nested.Children[0].StartDate != "2026-10-05" {
		t.Fatalf("epic item = %+v", nested)
	}
}
