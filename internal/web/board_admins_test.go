package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/authn"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
)

// Board settings are administered by the board's own administrators, the way
// Jira Software gates them, and not by workspace administrators alone.
func TestBoardAdministratorsMayConfigureTheirBoard(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if err := store.Migrate(ctx, st.Pool); err != nil {
		t.Fatal(err)
	}
	workspaceID, ownerID, memberID, projectID := store.NewID("ws"), store.NewID("usr"), store.NewID("usr"), store.NewID("prj")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Board gate test')`, workspaceID)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Gate owner')`, ownerID, ownerID+"@example.invalid")
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Gate member')`, memberID, memberID+"@example.invalid")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'admin')`, workspaceID, ownerID)
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'member')`, workspaceID, memberID)
	exec(`INSERT INTO projects(id,workspace_id,key,name,workflow_id) VALUES($1,$2,$3,'Gate project','wf_default')`,
		projectID, workspaceID, "BG"+strings.ToUpper(projectID[len(projectID)-5:]))
	t.Cleanup(func() {
		exec(`DELETE FROM sessions WHERE user_id IN ($1,$2)`, ownerID, memberID)
		exec(`DELETE FROM boards WHERE project_id=$1`, projectID)
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM custom_fields WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		exec(`DELETE FROM users WHERE id IN ($1,$2)`, ownerID, memberID)
	})
	board, err := st.CreateBoard(ctx, ownerID, workspaceID, store.BoardCreate{Name: "Gate board", Type: "scrum", ProjectID: projectID})
	if err != nil {
		t.Fatal(err)
	}

	h := &Handler{Store: st, Commands: &commands.Service{Store: st}, WorkspaceSlug: workspaceID}
	post := func(userID string) int {
		t.Helper()
		token, err := authn.LoginOIDC(ctx, st, userID, "id-token", "https://issuer.example.invalid", userID+"-subject", "")
		if err != nil {
			t.Fatal(err)
		}
		form := url.Values{"type": {"user"}, "accountId": {userID}}
		request := httptest.NewRequest(http.MethodPost, "/board/"+board.ID+"/settings/admins", strings.NewReader(form.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.AddCookie(&http.Cookie{Name: "zzira_session", Value: token})
		response := httptest.NewRecorder()
		h.AddBoardAdministrator(response, request, board.ID)
		return response.Code
	}

	if code := post(memberID); code != http.StatusForbidden {
		t.Fatalf("a member who administers nothing = %d, want 403", code)
	}
	if _, err := st.AddBoardAdmin(ctx, ownerID, workspaceID, board.ID, store.BoardAdminInput{Type: "user", AccountID: memberID}); err != nil {
		t.Fatal(err)
	}
	if code := post(memberID); code != http.StatusSeeOther {
		t.Fatalf("a board administrator = %d, want 303", code)
	}
	admins, err := st.BoardAdmins(ctx, board.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(admins) != 1 {
		t.Fatalf("administrators = %+v, want the one already named", admins)
	}
}
