package commands

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/store"
)

// Every work item command checks the project permission Jira checks for it.
// The project's scheme starts with Browse projects only; each step shows the
// command refused, grants the permission, and shows it allowed.
func TestWorkItemCommandsCheckProjectPermissions(t *testing.T) {
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
	actor, bystander := store.NewID("usr"), store.NewID("usr")
	ensureCommandTestActor(t, ctx, st, actor)
	ensureCommandTestActor(t, ctx, st, bystander)
	var admin string
	if err := st.Pool.QueryRow(ctx, `SELECT user_id FROM memberships WHERE workspace_id='ws_default' AND role='admin' ORDER BY user_id LIMIT 1`).Scan(&admin); err != nil {
		t.Skip("no workspace administrator to assign a scheme")
	}

	projectID := store.NewID("prj")
	key := "PE" + strings.ToUpper(projectID[len(projectID)-5:])
	if _, err := st.Pool.Exec(ctx, `INSERT INTO projects(id,workspace_id,key,name,workflow_id,lead_account_id) VALUES($1,'ws_default',$2,'Permission gate','wf_default',$3)`, projectID, key, admin); err != nil {
		t.Fatal(err)
	}
	scheme, err := st.CreatePermissionScheme(ctx, "ws_default", admin, "Permission gate "+key, "Browse only",
		[]store.PermissionGrantInput{{Permission: "BROWSE_PROJECTS", HolderType: "anyone"}})
	if err != nil {
		t.Fatal(err)
	}
	board, err := st.CreateBoard(ctx, admin, "ws_default", store.BoardCreate{Name: "Gate board", Type: "scrum", ProjectID: projectID})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, statement := range []string{
			`DELETE FROM issue_links WHERE inward_issue_id IN (SELECT id FROM issues WHERE project_id=$1) OR outward_issue_id IN (SELECT id FROM issues WHERE project_id=$1)`,
			`DELETE FROM sprints WHERE board_id IN (SELECT id FROM boards WHERE project_id=$1)`,
			`DELETE FROM boards WHERE project_id=$1`,
			`DELETE FROM issues WHERE project_id=$1`,
			`DELETE FROM project_permission_schemes WHERE project_id=$1`,
			`DELETE FROM projects WHERE id=$1`,
		} {
			_, _ = st.Pool.Exec(ctx, statement, projectID)
		}
		_, _ = st.Pool.Exec(ctx, `DELETE FROM permission_scheme_grants WHERE scheme_id=$1`, scheme.ID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM permission_schemes WHERE id=$1`, scheme.ID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM memberships WHERE user_id = ANY($1)`, []string{actor, bystander})
		_, _ = st.Pool.Exec(ctx, `DELETE FROM users WHERE id = ANY($1)`, []string{actor, bystander})
	})
	if _, _, err := st.AssignPermissionScheme(ctx, "ws_default", admin, projectID, scheme.ID); err != nil {
		t.Fatal(err)
	}
	grant := func(permission, holderType, holder string) {
		t.Helper()
		if _, err := st.CreatePermissionGrant(ctx, "ws_default", admin, scheme.ID, store.PermissionGrantInput{Permission: permission, HolderType: holderType, HolderValue: holder}); err != nil {
			t.Fatal(err)
		}
	}
	refused := func(err error, permission string) {
		t.Helper()
		var refusal *PermissionError
		if !errors.As(err, &refusal) || refusal.Permission != permission {
			t.Fatalf("got %v, want a %s refusal", err, permission)
		}
	}
	allowed := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	svc := &Service{Store: st}
	create := CreateIssueInput{ActorID: actor, WorkspaceID: "ws_default", ProjectIDOrKey: projectID, Summary: "gated", IssueTypeID: "it_task"}

	// Create issues, then Schedule issues for a due date.
	_, _, err = svc.CreateIssue(ctx, create)
	refused(err, "CREATE_ISSUES")
	// A portal customer raises a request without any project permission.
	customer := create
	customer.customerRequest = true
	_, _, err = svc.CreateIssue(ctx, customer)
	allowed(err)
	grant("CREATE_ISSUES", "anyone", "")
	dated := create
	dated.DueDate = "2030-01-01"
	_, _, err = svc.CreateIssue(ctx, dated)
	refused(err, "SCHEDULE_ISSUES")
	issue, _, err := svc.CreateIssue(ctx, create)
	allowed(err)
	other, _, err := svc.CreateIssue(ctx, create)
	allowed(err)

	// Edit issues.
	summary := "edited"
	edit := UpdateIssueInput{ActorID: actor, WorkspaceID: "ws_default", IssueIDOrKey: issue.ID, Summary: &summary}
	_, _, err = svc.UpdateIssue(ctx, edit)
	refused(err, "EDIT_ISSUES")
	grant("EDIT_ISSUES", "anyone", "")
	_, _, err = svc.UpdateIssue(ctx, edit)
	allowed(err)

	// Assign issues, and the assignee must be assignable.
	assign := UpdateIssueInput{ActorID: actor, WorkspaceID: "ws_default", IssueIDOrKey: issue.ID, AssigneeID: &bystander}
	_, _, err = svc.UpdateIssue(ctx, assign)
	refused(err, "ASSIGN_ISSUES")
	grant("ASSIGN_ISSUES", "anyone", "")
	_, _, err = svc.UpdateIssue(ctx, assign)
	if !errors.Is(err, ErrNotAssignable) {
		t.Fatalf("assigning someone who is not assignable = %v", err)
	}
	grant("ASSIGNABLE_USER", "user", bystander)
	_, _, err = svc.UpdateIssue(ctx, assign)
	allowed(err)

	// Transition issues, also when a board drag asks for a new column.
	_, _, err = svc.TransitionIssue(ctx, actor, "ws_default", issue.ID, "21")
	refused(err, "TRANSITION_ISSUES")
	grant("SCHEDULE_ISSUES", "anyone", "")
	refused(svc.SetIssueRank(ctx, actor, "ws_default", issue.ID, "", "", "st_inprogress"), "TRANSITION_ISSUES")
	grant("TRANSITION_ISSUES", "anyone", "")
	allowed(svc.SetIssueRank(ctx, actor, "ws_default", issue.ID, "", "", "st_inprogress"))
	moved, err := st.IssueByIDOrKey(ctx, "ws_default", issue.ID)
	allowed(err)
	if moved.Status.ID != "st_inprogress" {
		t.Fatalf("the drag left the status at %s", moved.Status.ID)
	}
	// A move no transition makes is refused rather than written directly.
	if err := svc.SetIssueRank(ctx, actor, "ws_default", issue.ID, "", "", "st_missing"); !errors.Is(err, ErrNoTransitionToStatus) {
		t.Fatalf("a drag to a status no transition reaches = %v", err)
	}

	// Link issues.
	_, _, err = svc.LinkIssue(ctx, actor, "ws_default", issue.ID, blocksLinkTypeID(t, svc), other.ID)
	refused(err, "LINK_ISSUES")
	grant("LINK_ISSUES", "anyone", "")
	_, _, err = svc.LinkIssue(ctx, actor, "ws_default", issue.ID, blocksLinkTypeID(t, svc), other.ID)
	allowed(err)

	// Manage watchers, for anyone but oneself.
	_, err = svc.SetWatcher(ctx, actor, "ws_default", issue.ID, bystander, true)
	refused(err, "MANAGE_WATCHERS")
	grant("MANAGE_WATCHERS", "anyone", "")
	_, err = svc.SetWatcher(ctx, actor, "ws_default", issue.ID, bystander, true)
	allowed(err)

	// Manage sprints, then planning needs Edit and Schedule issues (both held).
	_, err = svc.CreateSprint(ctx, actor, "ws_default", board.ID, "Gate sprint", "")
	refused(err, "MANAGE_SPRINTS_PERMISSION")
	grant("MANAGE_SPRINTS_PERMISSION", "anyone", "")
	sprint, err := svc.CreateSprint(ctx, actor, "ws_default", board.ID, "Gate sprint", "")
	allowed(err)
	allowed(svc.PlanIssue(ctx, actor, "ws_default", board.ID, issue.ID, sprint.ID, "", ""))

	// Modify reporter guards reporting work for someone else.
	reporter := create
	reporter.ReporterID = bystander
	_, _, err = svc.CreateIssue(ctx, reporter)
	refused(err, "MODIFY_REPORTER")
}
