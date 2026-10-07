package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/render"
	"github.com/e6qu/zzira/internal/store"
	"github.com/e6qu/zzira/internal/workflow"
)

func TestIssueControlsFollowSpecificPermissions(t *testing.T) {
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
	ws, owner, reader, project := store.NewID("ws"), store.NewID("usr"), store.NewID("usr"), store.NewID("prj")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Issue permission journeys')`, ws)
	for _, id := range []string{owner, reader} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test',$1)`, id, id+"@example.test")
		role := "member"
		if id == owner {
			role = "admin"
		}
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,$3)`, ws, id, role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, id, store.HashToken(id))
	}
	exec(`INSERT INTO projects(id,workspace_id,key,name,workflow_id) VALUES($1,$2,'IPJ','Permission journeys','wf_default')`, project, ws)
	t.Cleanup(func() {
		exec(`DELETE FROM actions WHERE workspace_id=$1`, ws)
		exec(`DELETE FROM issues WHERE workspace_id=$1`, ws)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, ws)
		exec(`DELETE FROM permission_schemes WHERE workspace_id=$1`, ws)
		exec(`DELETE FROM workflow_schemes WHERE workspace_id=$1`, ws)
		exec(`DELETE FROM workflows WHERE workspace_id=$1`, ws)
		exec(`DELETE FROM project_roles WHERE workspace_id=$1`, ws)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, ws)
		exec(`DELETE FROM workspaces WHERE id=$1`, ws)
		exec(`DELETE FROM api_tokens WHERE user_id IN ($1,$2)`, owner, reader)
		exec(`DELETE FROM users WHERE id IN ($1,$2)`, owner, reader)
	})
	svc := &commands.Service{Store: st}
	issue, _, err := svc.CreateIssue(ctx, commands.CreateIssueInput{ActorID: owner, WorkspaceID: ws, ProjectIDOrKey: project, Summary: "Permission journey", IssueTypeID: "it_task", AssigneeID: owner})
	if err != nil {
		t.Fatal(err)
	}
	ownAttachment, _, err := st.CreateAttachment(ctx, reader, ws, issue.ID, "mine.txt", "text/plain", 1, "permission-audit-own")
	if err != nil {
		t.Fatal(err)
	}
	otherAttachment, _, err := st.CreateAttachment(ctx, owner, ws, issue.ID, "other.txt", "text/plain", 1, "permission-audit-other")
	if err != nil {
		t.Fatal(err)
	}
	ownLog, _, err := st.CreateWorklog(ctx, reader, ws, issue.ID, nil, 60)
	if err != nil {
		t.Fatal(err)
	}
	otherLog, _, err := st.CreateWorklog(ctx, owner, ws, issue.ID, nil, 60)
	if err != nil {
		t.Fatal(err)
	}
	scheme, err := st.CreatePermissionScheme(ctx, ws, owner, "Reader journey", "", []store.PermissionGrantInput{{Permission: "BROWSE_PROJECTS", HolderType: "anyone"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.AssignPermissionScheme(ctx, ws, owner, project, scheme.ID); err != nil {
		t.Fatal(err)
	}
	h := &Handler{Store: st, Commands: svc, WorkspaceSlug: ws}
	request := func(method string, body url.Values) *http.Request {
		t.Helper()
		r := httptest.NewRequest(method, "/browse/"+issue.Key, strings.NewReader(body.Encode()))
		r.SetBasicAuth(reader+"@example.test", reader)
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("HX-Request", "true")
		return r
	}
	read := func() (*models.IssueView, string) {
		t.Helper()
		view, err := h.buildIssueView(request("GET", nil), &models.User{ID: reader}, ws, issue.Key)
		if err != nil {
			t.Fatal(err)
		}
		var html strings.Builder
		if err := render.Fragment(&html, "issue_view", *view); err != nil {
			t.Fatal(err)
		}
		return view, html.String()
	}
	controls := func(html string, offered bool, fragments ...string) {
		t.Helper()
		for _, fragment := range fragments {
			if strings.Contains(html, fragment) != offered {
				t.Fatalf("control %q offered = %v, want %v", fragment, !offered, offered)
			}
		}
	}
	grant := func(permission string) {
		t.Helper()
		if _, err := st.CreatePermissionGrant(ctx, ws, owner, scheme.ID, store.PermissionGrantInput{Permission: permission, HolderType: "user", HolderValue: reader}); err != nil {
			t.Fatal(err)
		}
	}
	_, html := read()
	controls(html, false, `id="edit-issue-button"`, `class="inline-field"`, `class="comment-form"`, `class="worklog-form"`, `class="upload-form"`, `Delete issue`, `Link work item`, `Delete attachment`, `Delete work log`)
	controls(html, true, `/watch"`, `/vote"`)
	response := httptest.NewRecorder()
	h.EditDialog(response, request("GET", nil), issue.Key)
	if response.Code != http.StatusForbidden {
		t.Fatalf("reader edit dialog: %d", response.Code)
	}
	_, _, err = svc.AddComment(ctx, commands.AddCommentInput{ActorID: reader, WorkspaceID: ws, IssueIDOrKey: issue.Key, PlainText: "forbidden"})
	if !errors.Is(err, commands.ErrPermission) {
		t.Fatalf("reader comment command: %v", err)
	}
	response = httptest.NewRecorder()
	h.AddComment(response, request("POST", url.Values{"body": {"forbidden"}}), issue.Key)
	if response.Code != http.StatusForbidden {
		t.Fatalf("reader comment form: %d %s", response.Code, response.Body.String())
	}
	comments, err := st.CommentsByIssue(ctx, issue.ID)
	if err != nil || len(comments) != 0 {
		t.Fatalf("refused comments persisted: %v %v", comments, err)
	}
	grant("ADD_COMMENTS")
	_, html = read()
	controls(html, true, `class="comment-form"`)
	controls(html, false, `id="edit-issue-button"`)
	response = httptest.NewRecorder()
	h.AddComment(response, request("POST", url.Values{"body": {"allowed"}}), issue.Key)
	if response.Code != http.StatusOK {
		t.Fatalf("commenter submit: %d %s", response.Code, response.Body.String())
	}
	role, err := st.CreateProjectRole(ctx, ws, owner, "Private comments", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddProjectRoleActors(ctx, ws, owner, project, role.ID, store.ProjectRoleActorInput{Users: []string{owner}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.CreateCommentWithVisibility(ctx, owner, ws, issue.ID, []byte(`{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"Restricted journey secret"}]}]}`), store.CommentVisibility{Type: "role", Value: strconv.FormatInt(role.ID, 10)}); err != nil {
		t.Fatal(err)
	}
	_, html = read()
	controls(html, false, "Restricted journey secret")
	ownerView, err := h.buildIssueView(request("GET", nil), &models.User{ID: owner}, ws, issue.Key)
	if err != nil || len(ownerView.Comments) != 2 {
		t.Fatalf("role member's comments: %v %v", ownerView, err)
	}
	grant("ASSIGN_ISSUES")
	_, html = read()
	controls(html, true, `id="field-assignee"`)
	controls(html, false, `id="field-priority"`)
	grant("EDIT_ISSUES")
	_, html = read()
	controls(html, true, `id="edit-issue-button"`, `id="field-priority"`)
	controls(html, false, `id="field-duedate"`, `Delete issue`)
	exec(`DELETE FROM permission_scheme_grants WHERE scheme_id=$1 AND permission_key='ASSIGN_ISSUES'`, scheme.ID)
	response = httptest.NewRecorder()
	h.EditDialog(response, request("GET", nil), issue.Key)
	if response.Code != http.StatusOK {
		t.Fatalf("editor dialog: %d", response.Code)
	}
	controls(response.Body.String(), false, `id="edit-assignee"`)
	response = httptest.NewRecorder()
	h.EditIssue(response, request("POST", url.Values{"summary": {"Editor without assignment permission"}, "description": {"Changed description"}}), issue.Key)
	if response.Code != http.StatusOK {
		t.Fatalf("editor save: %d %s", response.Code, response.Body.String())
	}
	saved, err := st.IssueByIDOrKey(ctx, ws, issue.ID)
	if err != nil || saved.Assignee == nil || saved.Assignee.ID != owner {
		t.Fatalf("edit cleared assignee: %+v %v", saved, err)
	}
	grant("ASSIGN_ISSUES")
	grant("SCHEDULE_ISSUES")
	grant("DELETE_ISSUES")
	grant("WORK_ON_ISSUES")
	grant("LINK_ISSUES")
	grant("CREATE_ATTACHMENTS")
	_, html = read()
	controls(html, true, `id="field-duedate"`, `Delete issue`, `class="worklog-form"`, `Link work item`, `class="upload-form"`)
	grant("DELETE_OWN_ATTACHMENTS")
	grant("DELETE_OWN_WORKLOGS")
	view, _ := read()
	if !view.DeletableAttachments[ownAttachment.ID] || view.DeletableAttachments[otherAttachment.ID] {
		t.Fatal("own attachment permission did not distinguish authors")
	}
	assertLogs := func(view *models.IssueView, own, other bool) {
		t.Helper()
		for _, item := range view.Activity {
			if item.ID == ownLog.ID && item.CanDelete != own || item.ID == otherLog.ID && item.CanDelete != other {
				t.Fatalf("delete worklog %s = %v", item.ID, item.CanDelete)
			}
		}
	}
	assertLogs(view, true, false)
	grant("DELETE_ALL_ATTACHMENTS")
	grant("DELETE_ALL_WORKLOGS")
	view, _ = read()
	if !view.DeletableAttachments[otherAttachment.ID] {
		t.Fatal("all attachment permission cannot delete another author's attachment")
	}
	assertLogs(view, true, true)
	frozen := workflow.Default()
	frozen.ID = store.NewID("wf")
	frozen.Name = "Frozen fields"
	frozen.Statuses = []workflow.StatusLayout{{StatusReference: issue.Status.ID, Properties: map[string]string{workflow.PropertyIssueEditable: "false"}}}
	if err := st.CreateWorkflow(ctx, ws, frozen); err != nil {
		t.Fatal(err)
	}
	if err := st.AssignWorkflowToProject(ctx, ws, project, frozen.ID); err != nil {
		t.Fatal(err)
	}
	view, html = read()
	controls(html, false, `id="edit-issue-button"`, `class="inline-field"`, `class="worklog-form"`, `Delete work log`)
	controls(html, true, `class="comment-form"`, `class="upload-form"`, `Link work item`)
	assertLogs(view, false, false)
	response = httptest.NewRecorder()
	h.EditDialog(response, request("GET", nil), issue.Key)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("frozen editor: %d", response.Code)
	}
	exec(`UPDATE issues SET archived_at=now() WHERE id=$1`, issue.ID)
	view, html = read()
	controls(html, false, `id="edit-issue-button"`, `class="inline-field"`, `class="comment-form"`, `class="worklog-form"`, `class="upload-form"`, `Delete issue`, `Link work item`, `Delete attachment`, `Delete work log`, `/watch"`, `/vote"`)
	assertLogs(view, false, false)
}
