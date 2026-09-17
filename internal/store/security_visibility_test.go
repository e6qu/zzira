package store

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/jql"
)

// TestSecurityVisibilityAcrossReadPaths walks every read path with a
// restricted issue: the project list, JQL search, a board, the bootstrap
// snapshot and the dashboard. One plain member is granted the issue's security
// level and one is not; workspace administrators would see everything, so
// neither is one.
func TestSecurityVisibilityAcrossReadPaths(t *testing.T) {
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

	workspaceID, adminID, grantedID, deniedID, projectID := NewID("ws"), NewID("usr"), NewID("usr"), NewID("usr"), NewID("prj")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Security visibility')`, workspaceID)
	for _, user := range []struct{ id, role string }{{adminID, "admin"}, {grantedID, "member"}, {deniedID, "member"}} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test',$1)`, user.id, user.id+"@example.invalid")
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,$3)`, workspaceID, user.id, user.role)
	}
	key := "SV" + strings.ToUpper(projectID[len(projectID)-5:])
	exec(`INSERT INTO projects(id,workspace_id,key,name,workflow_id,lead_account_id) VALUES($1,$2,$3,'Security visibility','wf_default',$4)`, projectID, workspaceID, key, adminID)
	t.Cleanup(func() {
		drop := func(query string, args ...any) { _, _ = st.Pool.Exec(ctx, query, args...) }
		drop(`DELETE FROM boards WHERE project_id=$1`, projectID)
		drop(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		drop(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		drop(`UPDATE projects SET security_scheme_id=NULL WHERE id=$1`, projectID)
		drop(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		drop(`DELETE FROM issue_security_level_members WHERE workspace_id=$1`, workspaceID)
		drop(`DELETE FROM security_schemes WHERE workspace_id=$1`, workspaceID)
		drop(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		drop(`DELETE FROM custom_fields WHERE workspace_id=$1`, workspaceID)
		drop(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		drop(`DELETE FROM users WHERE id IN ($1,$2,$3)`, adminID, grantedID, deniedID)
	})

	scheme, err := st.CreateIssueSecurityScheme(ctx, workspaceID, adminID, "Confidential "+key, "Restricted work", []SecurityLevelInput{{
		Name: "Private", Members: []SecurityLevelMemberInput{{Type: "user", Parameter: grantedID}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(scheme.Levels) != 1 {
		t.Fatalf("scheme levels = %+v", scheme.Levels)
	}
	levelID := scheme.Levels[0].ID
	exec(`UPDATE projects SET security_scheme_id=$2 WHERE id=$1`, projectID, scheme.ID)

	restricted, _, err := st.CreateIssue(ctx, adminID, projectID, "Confidential work", json.RawMessage(`{"type":"doc","version":1,"content":[]}`), "st_todo", "it_task", "pr_medium", "", nil, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	open, _, err := st.CreateIssue(ctx, adminID, projectID, "Public work", json.RawMessage(`{"type":"doc","version":1,"content":[]}`), "st_todo", "it_task", "pr_medium", "", nil, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE issues SET security_level_id=$2 WHERE id=$1`, restricted.ID, levelID)
	board, err := st.CreateBoard(ctx, adminID, workspaceID, BoardCreate{Name: "Security board", Type: "kanban", ProjectID: projectID})
	if err != nil {
		t.Fatal(err)
	}

	visibleTo := func(userID string) map[string]bool {
		t.Helper()
		out := map[string]bool{}
		issues, err := st.IssuesByProject(ctx, workspaceID, projectID, userID)
		if err != nil {
			t.Fatal(err)
		}
		for _, i := range issues {
			out[i.ID] = true
		}
		return out
	}
	// The project list: the unrestricted work is visible to both, which
	// proves the denied member reads the project at all.
	if !visibleTo(deniedID)[open.ID] {
		t.Fatal("the denied member cannot read the project, so the test proves nothing")
	}
	if visibleTo(deniedID)[restricted.ID] {
		t.Fatal("the denied member sees the restricted issue in IssuesByProject")
	}
	if !visibleTo(grantedID)[restricted.ID] {
		t.Fatal("the granted member lost the restricted issue in IssuesByProject")
	}

	// JQL search, per user.
	q, err := jql.Parse(`key = "` + restricted.Key + `"`)
	if err != nil {
		t.Fatal(err)
	}
	grantedIssues, grantedTotal, err := st.Search(ctx, workspaceID, grantedID, jql.CompileAt(q, grantedID, jql.DefaultResolver(), 2), 10, 0)
	if err != nil || grantedTotal != 1 || len(grantedIssues) != 1 {
		t.Fatalf("granted search total=%d n=%d err=%v", grantedTotal, len(grantedIssues), err)
	}
	_, deniedTotal, err := st.Search(ctx, workspaceID, deniedID, jql.CompileAt(q, deniedID, jql.DefaultResolver(), 2), 10, 0)
	if err != nil || deniedTotal != 0 {
		t.Fatalf("denied search total=%d err=%v (must be 0)", deniedTotal, err)
	}

	// The board.
	columns, err := st.BoardIssues(ctx, board.ID, deniedID)
	if err != nil {
		t.Fatal(err)
	}
	for _, column := range columns {
		for _, i := range column {
			if i.ID == restricted.ID {
				t.Fatal("the denied member's board contains the restricted issue")
			}
		}
	}

	// Bootstrap snapshots are user-shaped.
	deniedSnap, err := st.BootstrapSnapshot(ctx, workspaceID, deniedID)
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range deniedSnap.Issues {
		if i.ID == restricted.ID {
			t.Fatal("the denied member's snapshot contains the restricted issue")
		}
	}
	for _, c := range deniedSnap.Comments {
		if c.IssueID == restricted.ID {
			t.Fatal("the denied member's snapshot contains comments on the restricted issue")
		}
	}
	for _, a := range deniedSnap.Attachments {
		if a.IssueID == restricted.ID {
			t.Fatal("the denied member's snapshot contains attachments on the restricted issue")
		}
	}
	for _, w := range deniedSnap.Worklogs {
		if w.IssueID == restricted.ID {
			t.Fatal("the denied member's snapshot contains worklogs on the restricted issue")
		}
	}
	grantedSnap, err := st.BootstrapSnapshot(ctx, workspaceID, grantedID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, i := range grantedSnap.Issues {
		found = found || i.ID == restricted.ID
	}
	if !found {
		t.Fatal("the granted member's snapshot lost the restricted issue")
	}

	// Dashboard aggregates have the same boundary as the navigator and search;
	// otherwise counts and activity leak confidential work.
	stats, err := st.DashboardData(ctx, workspaceID, deniedID)
	if err != nil {
		t.Fatal(err)
	}
	var visibleCount int
	if err := st.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM issues i WHERE i.workspace_id=$1 AND `+VisibleIssuePredicate("i", "$2"), workspaceID, deniedID).Scan(&visibleCount); err != nil {
		t.Fatal(err)
	}
	if visibleCount != 1 {
		t.Fatalf("the denied member sees %d issues, want only the public one", visibleCount)
	}
	dashboardCount := 0
	for _, count := range stats.StatusCounts {
		dashboardCount += int(count.Count)
	}
	if dashboardCount != visibleCount {
		t.Fatalf("denied dashboard count=%d, want visible count %d", dashboardCount, visibleCount)
	}
	for _, activity := range stats.Recent {
		if activity.IssueKey == restricted.Key {
			t.Fatal("the denied member's dashboard activity contains the restricted issue")
		}
	}
}
