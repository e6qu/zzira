package store

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

// Jira Software lets a board name its own administrators, by person and by
// group, and they configure the board without administering the site.
func TestBoardAdministratorsGovernWhoCanConfigureABoard(t *testing.T) {
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
	workspaceID, actorID, strangerID, projectID := NewID("ws"), NewID("usr"), NewID("usr"), NewID("prj")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Board admin test')`, workspaceID)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Board owner')`, actorID, actorID+"@example.invalid")
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Board colleague')`, strangerID, strangerID+"@example.invalid")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'admin')`, workspaceID, actorID)
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'member')`, workspaceID, strangerID)
	exec(`INSERT INTO projects(id,workspace_id,key,name,workflow_id) VALUES($1,$2,$3,'Board admin project','wf_default')`,
		projectID, workspaceID, "BA"+strings.ToUpper(projectID[len(projectID)-5:]))
	t.Cleanup(func() {
		exec(`DELETE FROM boards WHERE project_id=$1`, projectID)
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM custom_fields WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		exec(`DELETE FROM users WHERE id IN ($1,$2)`, actorID, strangerID)
	})
	board, err := st.CreateBoard(ctx, actorID, workspaceID, BoardCreate{Name: "Administered board", Type: "scrum", ProjectID: projectID})
	if err != nil {
		t.Fatal(err)
	}

	admins, err := st.BoardAdmins(ctx, board.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(admins) != 0 {
		t.Fatalf("a new board has %d administrators, want none as Jira leaves it", len(admins))
	}
	// The workspace administrator administers every board regardless.
	if can, err := st.CanAdministerBoard(ctx, workspaceID, actorID, board); err != nil || !can {
		t.Fatalf("workspace administrator can administer = %v, err=%v", can, err)
	}
	if can, err := st.CanAdministerBoard(ctx, workspaceID, strangerID, board); err != nil || can {
		t.Fatalf("an unnamed member can administer = %v, err=%v", can, err)
	}

	// A board administrator is a person or a group, and never neither.
	if _, err := st.AddBoardAdmin(ctx, actorID, workspaceID, board.ID, BoardAdminInput{Type: "user"}); !errors.Is(err, ErrBoardAdminValidation) {
		t.Fatalf("a person with no account = %v, want ErrBoardAdminValidation", err)
	}
	if _, err := st.AddBoardAdmin(ctx, actorID, workspaceID, board.ID, BoardAdminInput{Type: "nobody", AccountID: strangerID}); !errors.Is(err, ErrBoardAdminValidation) {
		t.Fatalf("an unknown holder = %v, want ErrBoardAdminValidation", err)
	}

	if _, err := st.AddBoardAdmin(ctx, actorID, workspaceID, board.ID, BoardAdminInput{Type: "user", AccountID: strangerID}); err != nil {
		t.Fatal(err)
	}
	if can, err := st.CanAdministerBoard(ctx, workspaceID, strangerID, board); err != nil || !can {
		t.Fatalf("a named administrator can administer = %v, err=%v", can, err)
	}
	admins, err = st.BoardAdmins(ctx, board.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(admins) != 1 || admins[0].Type != "user" || admins[0].AccountID != strangerID || admins[0].UserName != "Board colleague" {
		t.Fatalf("administrators = %+v", admins)
	}

	// Naming the same person twice leaves one administrator, not two.
	if _, err := st.AddBoardAdmin(ctx, actorID, workspaceID, board.ID, BoardAdminInput{Type: "user", AccountID: strangerID}); err != nil {
		t.Fatal(err)
	}
	if admins, err = st.BoardAdmins(ctx, board.ID); err != nil || len(admins) != 1 {
		t.Fatalf("administrators after naming twice = %+v, err=%v", admins, err)
	}

	if _, err := st.DeleteBoardAdmin(ctx, actorID, workspaceID, board.ID, admins[0].ID); err != nil {
		t.Fatal(err)
	}
	if can, err := st.CanAdministerBoard(ctx, workspaceID, strangerID, board); err != nil || can {
		t.Fatalf("a removed administrator can administer = %v, err=%v", can, err)
	}
	if _, err := st.DeleteBoardAdmin(ctx, actorID, workspaceID, board.ID, admins[0].ID); !errors.Is(err, ErrBoardAdminValidation) {
		t.Fatalf("removing an administrator twice = %v, want ErrBoardAdminValidation", err)
	}

	// A group administers the board for everyone in it.
	var directoryID string
	if err := st.Pool.QueryRow(ctx, `SELECT id::text FROM directories ORDER BY id LIMIT 1`).Scan(&directoryID); err != nil {
		t.Skip("no directory to hold a group")
	}
	var groupID string
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO groups(directory_id,name,description) VALUES($1::uuid,$2,'Board administration') RETURNING id::text`,
		directoryID, "board-admins-"+NewID("grp")).Scan(&groupID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		exec(`DELETE FROM group_members WHERE group_id::text=$1`, groupID)
		exec(`DELETE FROM groups WHERE id::text=$1`, groupID)
	})
	exec(`INSERT INTO group_members(group_id,user_id) VALUES($1::uuid,$2)`, groupID, strangerID)
	if _, err := st.AddBoardAdmin(ctx, actorID, workspaceID, board.ID, BoardAdminInput{Type: "group", GroupID: groupID}); err != nil {
		t.Fatal(err)
	}
	if can, err := st.CanAdministerBoard(ctx, workspaceID, strangerID, board); err != nil || !can {
		t.Fatalf("a member of an administering group can administer = %v, err=%v", can, err)
	}
	admins, err = st.BoardAdmins(ctx, board.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(admins) != 1 || admins[0].Type != "group" || admins[0].GroupName == "" {
		t.Fatalf("group administrators = %+v", admins)
	}
}
