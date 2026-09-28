package store

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"
)

// A list of work items asks project permission once per project and asks
// only the rest of its work items about the holders that read the work item.
// Both halves must admit exactly what jira_has_project_permission admits: in
// a project that grants Browse projects only to each work item's reporter, a
// member sees the work items they reported and no others, and the project
// lead sees them all through a grant that names no work item.
func TestVisibleIssuePredicateAdmitsPerIssueHolders(t *testing.T) {
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
	workspaceID, _, err := st.DefaultWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := st.FirstAdminID(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	reporter, stranger := NewID("usr"), NewID("usr")
	projectID := NewID("prj")
	key := "VP" + strings.ToUpper(projectID[len(projectID)-5:])
	reported, other := NewID("issue"), NewID("issue")
	for _, user := range []string{reporter, stranger} {
		if _, err := st.Pool.Exec(ctx, `INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$1 || '@example.test','test','Visibility test')`, user); err != nil {
			t.Fatal(err)
		}
		if _, err := st.Pool.Exec(ctx, `INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'member')`, workspaceID, user); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.Pool.Exec(ctx, `INSERT INTO projects(id,workspace_id,key,name,workflow_id,lead_account_id) VALUES($1,$2,$3,'Reporter browse','wf_default',$4)`, projectID, workspaceID, key, admin); err != nil {
		t.Fatal(err)
	}
	for i, issue := range []struct{ id, reporter string }{{reported, reporter}, {other, admin}} {
		if _, err := st.Pool.Exec(ctx, `INSERT INTO issues(id,workspace_id,project_id,key,summary,status_id,issuetype_id,reporter_id,updated_seq) VALUES($1,$2,$3,$4,'Visibility','st_todo','it_task',$5,0)`,
			issue.id, workspaceID, projectID, key+"-"+string(rune('1'+i)), issue.reporter); err != nil {
			t.Fatal(err)
		}
	}
	scheme, err := st.CreatePermissionScheme(ctx, workspaceID, admin, "Reporter browse "+key, "", []PermissionGrantInput{
		{Permission: "BROWSE_PROJECTS", HolderType: "reporter"},
		{Permission: "BROWSE_PROJECTS", HolderType: "projectLead"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM issues WHERE project_id=$1`, projectID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM project_permission_schemes WHERE project_id=$1`, projectID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, projectID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM permission_scheme_grants WHERE scheme_id=$1`, scheme.ID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM permission_schemes WHERE id=$1`, scheme.ID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM memberships WHERE user_id=ANY($1)`, []string{reporter, stranger})
		_, _ = st.Pool.Exec(ctx, `DELETE FROM users WHERE id=ANY($1)`, []string{reporter, stranger})
	})
	if _, _, err := st.AssignPermissionScheme(ctx, workspaceID, admin, projectID, scheme.ID); err != nil {
		t.Fatal(err)
	}

	visible := func(user string) []string {
		t.Helper()
		rows, err := st.Pool.Query(ctx, `SELECT i.id FROM issues i WHERE i.project_id=$1 AND `+VisibleIssuePredicate("i", "$2")+` ORDER BY i.id`, projectID, user)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var ids []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			ids = append(ids, id)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return ids
	}
	permitted := func(user string) []string {
		t.Helper()
		var ids []string
		if err := st.Pool.QueryRow(ctx, `SELECT COALESCE(array_agg(i.id ORDER BY i.id),'{}') FROM issues i WHERE i.project_id=$1
			AND jira_has_project_permission(i.workspace_id,i.project_id,$2,i.id,'BROWSE_PROJECTS')`, projectID, user).Scan(&ids); err != nil {
			t.Fatal(err)
		}
		return ids
	}
	all := []string{reported, other}
	slices.Sort(all)
	for _, tc := range []struct {
		name, user string
		want       []string
	}{
		{"the reporter sees the work item they reported", reporter, []string{reported}},
		{"a member who reported nothing sees none", stranger, nil},
		{"the project lead sees every work item", admin, all},
	} {
		if got := visible(tc.user); !slices.Equal(got, tc.want) {
			t.Errorf("%s: the list admitted %v, want %v", tc.name, got, tc.want)
		}
		if got := permitted(tc.user); !slices.Equal(got, tc.want) && !(len(got) == 0 && len(tc.want) == 0) {
			t.Errorf("%s: jira_has_project_permission admitted %v, want %v", tc.name, got, tc.want)
		}
	}
}
