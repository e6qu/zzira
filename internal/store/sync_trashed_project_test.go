package store

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/e6qu/zzira/internal/models"
)

// A comment outlives the project its work item belongs to: trashing or
// archiving the project leaves the comment's action in the log, and the work
// item can no longer be loaded. The sync page must answer with the comment
// removed rather than fail, because the action stays in the log and every
// later read reaches it again -- one trashed project with one comment used to
// break every replica's sync permanently.
func TestSyncPageSurvivesCommentsOnTrashedAndArchivedProjects(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	st, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	workspaceID, memberID := NewID("ws"), NewID("usr")
	if _, err := st.Pool.Exec(ctx, `INSERT INTO workspaces (id,slug,name) VALUES ($1,$1,'Lifecycle sync')`, workspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, `INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$1 || '@example.test','test','Lifecycle reader')`, memberID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, `INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'admin')`, workspaceID, memberID); err != nil {
		t.Fatal(err)
	}
	// Registered after st.Close so the deferred order runs cleanup first: a
	// t.Cleanup callback would reach a pool that is already closed.
	defer func() {
		for _, statement := range []string{
			`DELETE FROM actions WHERE workspace_id=$1`,
			`DELETE FROM comments WHERE workspace_id=$1`,
			`DELETE FROM issues WHERE workspace_id=$1`,
			`DELETE FROM projects WHERE workspace_id=$1`,
			`DELETE FROM memberships WHERE workspace_id=$1`,
			`DELETE FROM workspaces WHERE id=$1`,
		} {
			if _, err := st.Pool.Exec(ctx, statement, workspaceID); err != nil {
				t.Errorf("cleanup %q: %v", statement, err)
			}
		}
		if _, err := st.Pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, memberID); err != nil {
			t.Errorf("cleanup users: %v", err)
		}
	}()

	for state, key := range map[string]string{"TRASHED": "TRA", "ARCHIVED": "ARC"} {
		projectID := NewID("prj")
		if _, err := st.Pool.Exec(ctx, `INSERT INTO projects (id,workspace_id,key,name,lead_account_id) VALUES ($1,$2,$3,'Lifecycle project',$4)`,
			projectID, workspaceID, key, memberID); err != nil {
			t.Fatal(err)
		}
		issue, _, err := st.CreateIssue(ctx, memberID, projectID, "Work with a comment",
			json.RawMessage(`{"type":"doc","version":1}`), "st_todo", "it_task", "", "", nil, nil, "", "")
		if err != nil {
			t.Fatal(err)
		}
		comment, _, err := st.CreateComment(ctx, memberID, workspaceID, issue.ID, json.RawMessage(`{"type":"doc","version":1}`))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.Pool.Exec(ctx, `UPDATE projects SET lifecycle_state=$2 WHERE id=$1`, projectID, state); err != nil {
			t.Fatal(err)
		}

		actions, _, err := st.ActionPageSince(ctx, workspaceID, memberID, 0, 100)
		if err != nil {
			t.Fatalf("%s project broke the sync page: %v", state, err)
		}
		found := false
		for _, action := range actions {
			if action.EntityType != models.EntityComment || action.EntityID != comment.ID {
				continue
			}
			found = true
			if action.Op != models.OpDelete {
				t.Fatalf("%s project: comment action is %q, want %q so a replica drops it", state, action.Op, models.OpDelete)
			}
		}
		if !found {
			t.Fatalf("%s project: the comment's action left the sync page entirely: %+v", state, actions)
		}
		if _, err := st.Pool.Exec(ctx, `DELETE FROM actions WHERE workspace_id=$1`, workspaceID); err != nil {
			t.Fatal(err)
		}
	}
}
