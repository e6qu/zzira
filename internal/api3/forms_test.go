package api3

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
	"github.com/e6qu/zzira/internal/web"
	"github.com/e6qu/zzira/internal/workflow"
)

func TestIssueFormsLifecycleDrivesWorkflowValidators(t *testing.T) {
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
	workspaceID, actorID, projectID, workflowID := store.NewID("ws"), store.NewID("usr"), store.NewID("project"), store.NewID("workflow")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Forms test')`, workspaceID)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Forms user')`, actorID, actorID+"@example.test")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'admin')`, workspaceID, actorID)
	exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, actorID, store.HashToken(actorID))
	exec(`INSERT INTO projects(id,workspace_id,key,name) VALUES($1,$2,'FRM','Forms project')`, projectID, workspaceID)
	t.Cleanup(func() {
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workflow_schemes WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workflows WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM organization_audit_events WHERE actor_id=$1`, actorID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		exec(`DELETE FROM api_tokens WHERE user_id=$1`, actorID)
		exec(`DELETE FROM users WHERE id=$1`, actorID)
	})

	wf := workflow.Workflow{ID: workflowID, Name: "Forms gate", Transitions: []workflow.Transition{{
		ID: "complete", Name: "Complete", From: []string{"st_todo"}, To: "st_done", Validators: []workflow.Rule{
			{ID: "attached", RuleKey: workflow.RuleFormsAttachedValidator, Parameters: map[string]string{}},
			{ID: "submitted", RuleKey: workflow.RuleFormsSubmittedValidator, Parameters: map[string]string{}},
		},
	}}}
	if err := st.CreateWorkflow(ctx, workspaceID, wf); err != nil {
		t.Fatal(err)
	}
	if err := st.AssignWorkflowToProject(ctx, workspaceID, projectID, workflowID); err != nil {
		t.Fatal(err)
	}
	service := &commands.Service{Store: st}
	issue, _, err := service.CreateIssue(ctx, commands.CreateIssueInput{ActorID: actorID, WorkspaceID: workspaceID, ProjectIDOrKey: projectID, Summary: "Complete onboarding", IssueTypeID: "it_task"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.TransitionIssue(ctx, actorID, workspaceID, issue.Key, "complete"); err == nil || !strings.Contains(err.Error(), "at least one form") {
		t.Fatalf("missing-form transition error = %v", err)
	}

	handler := &Handler{Store: st, Commands: service, WorkspaceSlug: workspaceID, BaseURL: "https://zzira.test"}
	call := func(method, path, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.SetBasicAuth(actorID+"@example.test", actorID)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s: %d want %d: %s", method, path, response.Code, want, response.Body.String())
		}
		return response
	}
	base := "/jira/forms/cloud/cloud-id/issue/" + issue.Key + "/form"
	attached := call("POST", base, `{"formTemplate":{"id":"onboarding","name":"Employee onboarding"}}`, 200)
	var indexEntry struct {
		ID        string `json:"id"`
		Submitted bool   `json:"submitted"`
	}
	if err := json.Unmarshal(attached.Body.Bytes(), &indexEntry); err != nil || indexEntry.ID == "" || indexEntry.Submitted {
		t.Fatalf("attached form = %+v, %v", indexEntry, err)
	}
	index := call("GET", base, "", 200)
	if !strings.Contains(index.Body.String(), `"name":"Employee onboarding"`) {
		t.Fatal(index.Body.String())
	}
	if _, _, err := service.TransitionIssue(ctx, actorID, workspaceID, issue.Key, "complete"); err == nil || !strings.Contains(err.Error(), "all attached forms") {
		t.Fatalf("open-form transition error = %v", err)
	}
	formPath := base + "/" + indexEntry.ID
	saved := call("PUT", formPath, `{"answers":{"employee":{"text":"Ada"}}}`, 200)
	if !strings.Contains(saved.Body.String(), `"employee":{"text":"Ada"}`) {
		t.Fatal(saved.Body.String())
	}
	call("PUT", formPath+"/action/external", "", 200)
	call("PUT", formPath+"/action/submit", "", 200)
	call("PUT", formPath, `{"answers":{}}`, 412)
	if _, _, err := service.TransitionIssue(ctx, actorID, workspaceID, issue.Key, "complete"); err != nil {
		t.Fatal(err)
	}
	call("PUT", formPath+"/action/reopen", "", 200)
	call("PUT", formPath+"/action/internal", "", 200)
	call("DELETE", formPath, "", 200)
	for _, missing := range []struct{ method, path string }{
		{"DELETE", formPath},
		{"PUT", formPath + "/action/submit"},
	} {
		if response := call(missing.method, missing.path, "", http.StatusNotFound); !strings.Contains(response.Body.String(), "Form does not exist.") {
			t.Fatalf("missing form error = %s", response.Body.String())
		}
	}
	if got := strings.TrimSpace(call("GET", base, "", 200).Body.String()); got != "[]" {
		t.Fatalf("form index after delete = %s", got)
	}
	var actionCount int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM actions WHERE workspace_id=$1 AND entity_type='issue_form' AND actor_id=$2 AND payload->>'issueId'=$3`, workspaceID, actorID, issue.ID).Scan(&actionCount); err != nil {
		t.Fatal(err)
	}
	if actionCount != 7 {
		t.Fatalf("form actions = %d, want seven committed changes", actionCount)
	}

	// A reader may inspect the attached form, but cannot change it through
	// either edge or by calling the command directly.
	readerID := store.NewID("usr")
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Forms reader')`, readerID, readerID+"@example.test")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'member')`, workspaceID, readerID)
	exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, readerID, store.HashToken(readerID))
	defer func() {
		exec(`DELETE FROM api_tokens WHERE user_id=$1`, readerID)
		exec(`DELETE FROM memberships WHERE user_id=$1`, readerID)
		exec(`DELETE FROM users WHERE id=$1`, readerID)
	}()
	scheme, err := st.CreatePermissionScheme(ctx, workspaceID, actorID, "Form editors", "", []store.PermissionGrantInput{
		{Permission: "BROWSE_PROJECTS", HolderType: "anyone"},
		{Permission: "EDIT_ISSUES", HolderType: "user", HolderParameter: actorID},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		exec(`DELETE FROM project_permission_schemes WHERE project_id=$1`, projectID)
		exec(`DELETE FROM permission_scheme_grants WHERE scheme_id=$1`, scheme.ID)
		exec(`DELETE FROM permission_schemes WHERE id=$1`, scheme.ID)
	}()
	if _, _, err := st.AssignPermissionScheme(ctx, workspaceID, actorID, projectID, scheme.ID); err != nil {
		t.Fatal(err)
	}
	attached = call("POST", base, `{"formTemplate":{"id":"restricted"}}`, 200)
	if err := json.Unmarshal(attached.Body.Bytes(), &indexEntry); err != nil {
		t.Fatal(err)
	}
	formPath = base + "/" + indexEntry.ID
	for _, test := range []struct{ method, path, body string }{
		{"GET", formPath, ""},
		{"POST", base, `{"formTemplate":{"id":"forbidden"}}`},
		{"PUT", formPath, `{"answers":{"changed":true}}`},
		{"PUT", formPath + "/action/submit", ""},
		{"PUT", formPath + "/action/reopen", ""},
		{"PUT", formPath + "/action/internal", ""},
		{"PUT", formPath + "/action/external", ""},
		{"DELETE", formPath, ""},
	} {
		request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
		request.SetBasicAuth(readerID+"@example.test", readerID)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		want := http.StatusForbidden
		if test.method == "GET" {
			want = http.StatusOK
		}
		if response.Code != want {
			t.Fatalf("reader %s %s: %d want %d: %s", test.method, test.path, response.Code, want, response.Body.String())
		}
	}
	for _, action := range []string{"attach", "answers", "submit", "reopen", "internal", "external", "delete"} {
		if _, err := service.ChangeIssueForm(ctx, readerID, workspaceID, issue.ID, indexEntry.ID, store.IssueFormChange{Action: action}); !errors.Is(err, commands.ErrPermission) {
			t.Fatalf("reader command %s = %v, want permission refusal", action, err)
		}
	}
	webHandler := &web.Handler{Store: st, Commands: service, WorkspaceSlug: workspaceID}
	for _, userID := range []string{readerID, actorID} {
		request := httptest.NewRequest("GET", "/browse/"+issue.Key, nil)
		request.Header.Set("HX-Request", "true")
		request.SetBasicAuth(userID+"@example.test", userID)
		response := httptest.NewRecorder()
		webHandler.BrowseIssue(response, request, issue.Key)
		if response.Code != http.StatusOK {
			t.Fatalf("form page for %s: %d: %s", userID, response.Code, response.Body.String())
		}
		if offered := strings.Contains(response.Body.String(), "Attach form</summary>"); offered != (userID == actorID) {
			t.Fatalf("form controls offered to %s = %v", userID, offered)
		}
	}
	for _, action := range []string{"attach", "submit", "reopen", "delete"} {
		body := url.Values{"template": {"forbidden"}}.Encode()
		request := httptest.NewRequest("POST", "/issues/"+issue.Key+"/forms", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.SetBasicAuth(readerID+"@example.test", readerID)
		response := httptest.NewRecorder()
		if action == "attach" {
			webHandler.AttachIssueForm(response, request, issue.Key)
		} else {
			webHandler.UpdateIssueForm(response, request, issue.Key, indexEntry.ID, action)
		}
		if response.Code != http.StatusForbidden {
			t.Fatalf("reader browser %s: %d: %s", action, response.Code, response.Body.String())
		}
	}
	forms, err := st.IssueForms(ctx, workspaceID, issue.ID)
	if err != nil || len(forms) != 1 || forms[0].Submitted || !forms[0].Internal || string(forms[0].Answers) != "{}" {
		t.Fatalf("refused mutations changed the form: %+v, %v", forms, err)
	}
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM actions WHERE workspace_id=$1 AND entity_type='issue_form'`, workspaceID).Scan(&actionCount); err != nil || actionCount != 8 {
		t.Fatalf("refused mutations wrote actions: %d, %v", actionCount, err)
	}
	call("PUT", formPath+"/action/submit", "", 200)
}
