package api3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/agile"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
	"github.com/e6qu/zzira/internal/web"
)

func TestIssueRelationshipsRespectReaderVisibility(t *testing.T) {
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
	ws, owner, allowed, denied, project := store.NewID("ws"), store.NewID("usr"), store.NewID("usr"), store.NewID("usr"), store.NewID("prj")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Relationship visibility')`, ws)
	for _, id := range []string{owner, allowed, denied} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test',$1)`, id, id+"@example.test")
		role := "member"
		if id == owner {
			role = "admin"
		}
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,$3)`, ws, id, role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, id, store.HashToken(id))
	}
	exec(`INSERT INTO projects(id,workspace_id,key,name,workflow_id,lead_account_id) VALUES($1,$2,'RVJ','Relationship journey','wf_default',$3)`, project, ws, owner)
	t.Cleanup(func() {
		exec(`DELETE FROM issues WHERE workspace_id=$1`, ws)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, ws)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, ws)
		exec(`DELETE FROM security_schemes WHERE workspace_id=$1`, ws)
		exec(`DELETE FROM permission_schemes WHERE workspace_id=$1`, ws)
		exec(`DELETE FROM project_roles WHERE workspace_id=$1`, ws)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, ws)
		exec(`DELETE FROM workspaces WHERE id=$1`, ws)
		for _, id := range []string{owner, allowed, denied} {
			exec(`DELETE FROM api_tokens WHERE user_id=$1`, id)
			exec(`DELETE FROM users WHERE id=$1`, id)
		}
	})
	svc := &commands.Service{Store: st}
	create := func(summary, typ, parent string) string {
		t.Helper()
		issue, _, err := svc.CreateIssue(ctx, commands.CreateIssueInput{ActorID: owner, WorkspaceID: ws, ProjectIDOrKey: project, Summary: summary, IssueTypeID: typ, ParentIDOrKey: parent, AssigneeID: owner})
		if err != nil {
			t.Fatal(err)
		}
		return issue.Key
	}
	publicEpic := create("Public epic", "it_epic", "")
	privateEpic := create("Secret epic title", "it_epic", "")
	story := create("Public story", "it_story", privateEpic)
	task := create("Public parent task", "it_task", publicEpic)
	publicChild := create("Public child", "it_subtask", task)
	privateChild := create("Secret child title", "it_subtask", task)
	scheme, err := st.CreateIssueSecurityScheme(ctx, ws, owner, "Relationship restriction", "", []store.SecurityLevelInput{{Name: "Restricted", Members: []store.SecurityLevelMemberInput{{Type: "user", Parameter: allowed}}}})
	if err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE projects SET security_scheme_id=$2 WHERE id=$1`, project, scheme.ID)
	exec(`UPDATE issues SET security_level_id=$2 WHERE workspace_id=$1 AND key IN ($3,$4)`, ws, scheme.Levels[0].ID, privateEpic, privateChild)
	permissions, err := st.CreatePermissionScheme(ctx, ws, owner, "Assignment choices", "", []store.PermissionGrantInput{
		{Permission: "BROWSE_PROJECTS", HolderType: "anyone"}, {Permission: "CREATE_ISSUES", HolderType: "anyone"}, {Permission: "EDIT_ISSUES", HolderType: "anyone"}, {Permission: "ASSIGN_ISSUES", HolderType: "anyone"}, {Permission: "ASSIGNABLE_USER", HolderType: "user", HolderValue: allowed},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.AssignPermissionScheme(ctx, ws, owner, project, permissions.ID); err != nil {
		t.Fatal(err)
	}
	role, err := st.CreateProjectRole(ctx, ws, owner, "Private discussion", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddProjectRoleActors(ctx, ws, owner, project, role.ID, store.ProjectRoleActorInput{Users: []string{owner, allowed}}); err != nil {
		t.Fatal(err)
	}
	storyIssue, err := st.IssueByIDOrKey(ctx, ws, story)
	if err != nil {
		t.Fatal(err)
	}
	commentBody := []byte(`{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"Secret discussion body"}]}]}`)
	comment, _, err := st.CreateCommentWithVisibility(ctx, owner, ws, storyIssue.ID, commentBody, store.CommentVisibility{})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.UpdateComment(ctx, owner, ws, comment.ID, commentBody, true, store.CommentVisibility{Type: "role", Value: strconv.FormatInt(role.ID, 10)}); err != nil {
		t.Fatal(err)
	}
	for _, parent := range []string{publicEpic, privateEpic} {
		if _, _, err := svc.UpdateIssue(ctx, commands.UpdateIssueInput{ActorID: owner, WorkspaceID: ws, IssueIDOrKey: story, ParentIDOrKey: &parent}); err != nil {
			t.Fatal(err)
		}
	}
	privateProject := store.NewID("prj")
	exec(`INSERT INTO projects(id,workspace_id,key,name,workflow_id,lead_account_id) VALUES($1,$2,'RVP','Secret project title','wf_default',$3)`, privateProject, ws, owner)
	privatePermissions, err := st.CreatePermissionScheme(ctx, ws, owner, "Private project", "", []store.PermissionGrantInput{{Permission: "BROWSE_PROJECTS", HolderType: "user", HolderValue: owner}, {Permission: "CREATE_ISSUES", HolderType: "user", HolderValue: owner}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.AssignPermissionScheme(ctx, ws, owner, privateProject, privatePermissions.ID); err != nil {
		t.Fatal(err)
	}
	h := &Handler{Store: st, Commands: svc, WorkspaceSlug: ws, BaseURL: "https://zzira.test"}
	software := &agile.Handler{Store: st, Commands: svc, WorkspaceSlug: ws, IssueBean: h.IssueBean, BaseURL: h.BaseURL}
	browser := &web.Handler{Store: st, Commands: svc, WorkspaceSlug: ws}
	request := func(user, method, path, body string) *http.Request {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.SetBasicAuth(user+"@example.test", user)
		r.Header.Set("Content-Type", "application/json")
		return r
	}
	read := func(user, path, body string) string {
		t.Helper()
		method := "GET"
		if body != "" {
			method = "POST"
		}
		w := httptest.NewRecorder()
		r := request(user, method, path, body)
		if strings.HasPrefix(path, "/rest/agile/") {
			software.ServeHTTP(w, r)
		} else {
			h.ServeHTTP(w, r)
		}
		if w.Code != 200 {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	for _, user := range []string{denied, allowed} {
		visible := user == allowed
		for _, path := range []string{"/rest/api/3/issue/" + story + "?fields=summary,parent", "/rest/agile/1.0/issue/" + story} {
			body := read(user, path, "")
			if strings.Contains(body, "Secret epic title") != visible {
				t.Fatalf("parent disclosure for %s at %s: %s", user, path, body)
			}
		}
		for _, test := range []struct{ path, body string }{
			{"/rest/api/3/search/jql", fmt.Sprintf(`{"jql":"key = %s","fields":["summary","parent"]}`, story)},
			{"/rest/api/3/expression/eval", fmt.Sprintf(`{"expression":"issue","context":{"issue":{"key":"%s"}}}`, story)},
		} {
			body := read(user, test.path, test.body)
			if strings.Contains(body, "Secret epic title") != visible {
				t.Fatalf("parent disclosure in %s: %s", test.path, body)
			}
		}
		body := read(user, "/rest/api/3/issue/"+task+"?fields=subtasks", "")
		if !strings.Contains(body, publicChild) || strings.Contains(body, privateChild) != visible {
			t.Fatalf("child visibility: %s", body)
		}
		for _, key := range []string{task, story} {
			r := request(user, "GET", "/browse/"+key, "")
			r.Header.Set("HX-Request", "true")
			w := httptest.NewRecorder()
			browser.BrowseIssue(w, r, key)
			if w.Code != 200 {
				t.Fatalf("browser issue %s: %d %s", key, w.Code, w.Body.String())
			}
			body = w.Body.String()
			if strings.Contains(body, "Secret epic title") != visible {
				t.Fatalf("browser parent/picker disclosure on %s: %s", key, body)
			}
			if key == task && strings.Contains(body, "Secret child title") != visible {
				t.Fatalf("browser child disclosure: %s", body)
			}
		}
		snapshot, err := st.BootstrapSnapshot(ctx, ws, user)
		if err != nil {
			t.Fatal(err)
		}
		snapshotJSON, _ := json.Marshal(snapshot)
		for _, secret := range []string{"Secret epic title", "Secret discussion body", privateEpic} {
			if strings.Contains(string(snapshotJSON), secret) != visible {
				t.Fatalf("bootstrap disclosure %q for %s", secret, user)
			}
		}
		actions, to, err := st.ActionPageSince(ctx, ws, user, 0, 10000)
		if err != nil {
			t.Fatal(err)
		}
		actionsJSON, _ := json.Marshal(actions)
		for _, secret := range []string{"Secret epic title", "Secret discussion body", privateEpic} {
			if strings.Contains(string(actionsJSON), secret) != visible {
				t.Fatalf("incremental disclosure %q for %s", secret, user)
			}
		}
		if to == 0 {
			t.Fatal("filtered sync must advance its cursor")
		}
		if !visible {
			deleted := false
			for _, action := range actions {
				if action.EntityID == comment.ID && action.Op == "delete" {
					deleted = true
				}
			}
			if !deleted {
				t.Fatal("revoked comment must be removed from a replica")
			}
		}
		history := read(user, "/rest/api/3/issue/"+story+"/changelog", "")
		if strings.Contains(history, privateEpic) != visible {
			t.Fatalf("history parent disclosure for %s: %s", user, history)
		}
		meta, err := st.IssueCreateMetadata(ctx, ws, user)
		if err != nil {
			t.Fatal(err)
		}
		createResponse := httptest.NewRecorder()
		browser.CreateDialog(createResponse, request(user, "GET", "/issues/new?project=RVJ", ""))
		if createResponse.Code != 200 || strings.Contains(createResponse.Body.String(), "Secret project title") {
			t.Fatalf("create project choices: %d %s", createResponse.Code, createResponse.Body.String())
		}
		encoded, _ := json.Marshal(meta)
		if strings.Contains(string(encoded), "Secret project title") {
			t.Fatal("metadata disclosed an inaccessible project")
		}
		if strings.Contains(string(encoded), "Secret epic title") != visible {
			t.Fatalf("create parent choices: %s", encoded)
		}
		for _, projectMeta := range meta.Projects {
			if projectMeta.Project.ID != project {
				continue
			}
			for _, field := range projectMeta.Fields {
				if field.ID != "assignee" {
					continue
				}
				if len(field.Options) != 2 || (field.Options[0].ID != allowed && field.Options[1].ID != allowed) {
					t.Fatalf("create assignees = %+v", field.Options)
				}
			}
		}
		w := httptest.NewRecorder()
		browser.EditDialog(w, request(user, "GET", "/issues/"+story+"/edit-dialog", ""), story)
		if w.Code != 200 {
			t.Fatalf("edit dialog: %d %s", w.Code, w.Body.String())
		}
		body = w.Body.String()
		start := strings.Index(body, `id="edit-assignee"`)
		end := strings.Index(body[start:], "</select>")
		selectHTML := body[start : start+end]
		if strings.Contains(selectHTML, denied) || !strings.Contains(selectHTML, allowed) || !strings.Contains(selectHTML, owner) {
			t.Fatalf("assignment candidates and current selection: %s", selectHTML)
		}
	}
	if _, err := st.CreatePermissionGrant(ctx, ws, owner, permissions.ID, store.PermissionGrantInput{Permission: "ASSIGNABLE_USER", HolderType: "reporter"}); err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE issues SET reporter_id=$2 WHERE workspace_id=$1 AND key=$3`, ws, denied, story)
	contextualMetadata := read(denied, "/rest/api/3/issue/"+story+"/editmeta", "")
	if !strings.Contains(contextualMetadata, denied) {
		t.Fatal("assignee choices must include users admitted by issue-specific holders")
	}
	exec(`DELETE FROM permission_scheme_grants WHERE scheme_id=$1 AND permission_key='CREATE_ISSUES'`, permissions.ID)
	response := httptest.NewRecorder()
	browser.CreateDialog(response, request(denied, "GET", "/issues/new", ""))
	if response.Code != http.StatusForbidden {
		t.Fatalf("no create permission: %d %s", response.Code, response.Body.String())
	}
	editMetadata := read(denied, "/rest/api/3/issue/"+story+"/editmeta", "")
	if !strings.Contains(editMetadata, `"summary"`) {
		t.Fatal("edit metadata must remain available without create permission")
	}

}
