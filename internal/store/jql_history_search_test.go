package store

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/e6qu/zzira/internal/jql"
)

func TestSearchEvaluatesHistoryAndRelativeDateClauses(t *testing.T) {
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
	if err = Migrate(ctx, st.Pool); err != nil {
		t.Fatal(err)
	}

	workspaceID, userID, projectID := NewID("ws"), NewID("usr"), NewID("prj")
	projectKey := "JH" + projectID[len(projectID)-5:]
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'JQL history test')`, workspaceID)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','History actor')`, userID, userID+"@example.invalid")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'member')`, workspaceID, userID)
	exec(`INSERT INTO projects(id,workspace_id,key,name,workflow_id) VALUES($1,$2,$3,'History project','wf_default')`, projectID, workspaceID, projectKey)
	t.Cleanup(func() {
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		exec(`DELETE FROM users WHERE id=$1`, userID)
	})

	issue, _, err := st.CreateIssue(ctx, userID, projectID, "History query target",
		json.RawMessage(`{"type":"doc","version":1,"content":[]}`), "st_todo", "it_task", "", userID, nil, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	done := "st_done"
	if _, _, err = st.UpdateIssue(ctx, userID, workspaceID, issue.ID, IssueUpdate{StatusID: &done}); err != nil {
		t.Fatal(err)
	}

	parsed, err := jql.Parse(`status CHANGED FROM "To Do" TO Done BY currentUser() AFTER startOfYear(-1y) AND status WAS "To Do" ORDER BY updated DESC, key ASC`)
	if err != nil {
		t.Fatal(err)
	}
	compiled := jql.CompileAt(parsed, userID, jql.DefaultResolver(), 2)
	if compiled.Err != nil {
		t.Fatal(compiled.Err)
	}
	issues, total, err := st.Search(ctx, workspaceID, userID, compiled, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(issues) != 1 || issues[0].ID != issue.ID {
		t.Fatalf("history search total=%d issues=%v", total, issues)
	}
}
