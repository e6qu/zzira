package api3

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
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
	if got := strings.TrimSpace(call("GET", base, "", 200).Body.String()); got != "[]" {
		t.Fatalf("form index after delete = %s", got)
	}
}
