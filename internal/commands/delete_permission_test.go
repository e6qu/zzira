package commands

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/store"
)

// Jira gates deleting work on the project's Delete issues permission. Work a
// person can merely see is not work they may delete.
func TestDeleteIssueRequiresTheDeletePermission(t *testing.T) {
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
	ensureCommandTestActor(t, ctx, st, "usr_delete_gate")
	var admin string
	if err := st.Pool.QueryRow(ctx, `SELECT user_id FROM memberships WHERE workspace_id='ws_default' AND role='admin' ORDER BY user_id LIMIT 1`).Scan(&admin); err != nil {
		t.Skip("no workspace administrator to assign a scheme")
	}

	projectID := store.NewID("prj")
	key := "DG" + strings.ToUpper(projectID[len(projectID)-5:])
	if _, err := st.Pool.Exec(ctx, `INSERT INTO projects(id,workspace_id,key,name,workflow_id,lead_account_id) VALUES($1,'ws_default',$2,'Delete gate','wf_default',$3)`, projectID, key, admin); err != nil {
		t.Fatal(err)
	}

	// A scheme that lets anyone see and raise work, but names nobody who may
	// delete it.
	grants := []store.PermissionGrantInput{}
	for _, permission := range []string{"BROWSE_PROJECTS", "CREATE_ISSUES", "EDIT_ISSUES", "ASSIGNABLE_USER"} {
		grants = append(grants, store.PermissionGrantInput{Permission: permission, HolderType: "anyone"})
	}
	scheme, err := st.CreatePermissionScheme(ctx, "ws_default", admin, "Delete gate "+key, "No delete", grants)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM issues WHERE project_id=$1`, projectID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM project_permission_schemes WHERE project_id=$1`, projectID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, projectID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM permission_scheme_grants WHERE scheme_id=$1`, scheme.ID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM permission_schemes WHERE id=$1`, scheme.ID)
	})
	if _, _, err := st.AssignPermissionScheme(ctx, "ws_default", admin, projectID, scheme.ID); err != nil {
		t.Fatal(err)
	}

	svc := &Service{Store: st}
	issue, _, err := svc.CreateIssue(ctx, CreateIssueInput{
		ActorID: "usr_delete_gate", WorkspaceID: "ws_default", ProjectIDOrKey: projectID,
		Summary: "gated work", IssueTypeID: "it_task",
	})
	if err != nil {
		t.Fatal(err)
	}

	// The work item is visible, and still may not be deleted.
	if _, err := svc.visibleIssue(ctx, "usr_delete_gate", "ws_default", issue.ID); err != nil {
		t.Fatalf("the work item should be visible: %v", err)
	}
	if _, err := svc.DeleteIssue(ctx, "usr_delete_gate", "ws_default", issue.ID, "test"); !errors.Is(err, ErrIssueDeletePermission) {
		t.Fatalf("delete without the permission = %v, want ErrIssueDeletePermission", err)
	}
	var remaining int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM issues WHERE id=$1`, issue.ID).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 1 {
		t.Fatal("the refused delete removed the work item anyway")
	}

	// The site's own cleanup deletes regardless, because a request whose setup
	// failed is the site undoing its own work.
	other, _, err := svc.CreateIssue(ctx, CreateIssueInput{
		ActorID: "usr_delete_gate", WorkspaceID: "ws_default", ProjectIDOrKey: projectID,
		Summary: "rolled back work", IssueTypeID: "it_task",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.deleteIssueUnchecked(ctx, "usr_delete_gate", "ws_default", other.ID, "rollback"); err != nil {
		t.Fatalf("the site's own cleanup was refused: %v", err)
	}

	// Naming someone who may delete lets them.
	if _, err := st.CreatePermissionGrant(ctx, "ws_default", admin, scheme.ID, store.PermissionGrantInput{Permission: "DELETE_ISSUES", HolderType: "anyone"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DeleteIssue(ctx, "usr_delete_gate", "ws_default", issue.ID, "test"); err != nil {
		t.Fatalf("delete with the permission: %v", err)
	}
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM issues WHERE id=$1`, issue.ID).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatal("the permitted delete left the work item")
	}
}
