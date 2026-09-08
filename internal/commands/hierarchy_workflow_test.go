package commands

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/e6qu/zzira/internal/adf"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
	"github.com/e6qu/zzira/internal/workflow"
)

func TestIssueHierarchyPersistsAndBlocksWorkflowTransitions(t *testing.T) {
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
	workspaceID, actorID, projectID, workflowID := store.NewID("ws"), store.NewID("usr"), store.NewID("project"), store.NewID("wf")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Hierarchy rules')`, workspaceID)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Hierarchy actor')`, actorID, actorID+"@example.test")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'admin')`, workspaceID, actorID)
	exec(`INSERT INTO projects(id,workspace_id,key,name) VALUES($1,$2,'HIE','Hierarchy project')`, projectID, workspaceID)
	t.Cleanup(func() {
		exec(`DELETE FROM webhook_deliveries WHERE webhook_id IN (SELECT id FROM webhooks WHERE workspace_id=$1)`, workspaceID)
		exec(`DELETE FROM webhooks WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workflow_schemes WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workflows WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM organization_audit_events WHERE actor_id=$1`, actorID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		exec(`DELETE FROM users WHERE id=$1`, actorID)
	})

	wf := workflow.Workflow{ID: workflowID, Name: "Hierarchy gates", Transitions: []workflow.Transition{{
		ID: "complete", Name: "Complete", From: []string{"st_todo", "st_inprogress"}, To: "st_done",
		Conditions: &workflow.ConditionGroup{Operation: "ALL", Conditions: []workflow.Rule{{
			ID: "children", RuleKey: workflow.RuleParentChildCondition,
			Parameters: map[string]string{"blocker": "CHILD", "statusIds": "st_todo"},
		}}},
		Validators: []workflow.Rule{{
			ID: "parent", RuleKey: workflow.RuleParentChildValidator,
			Parameters: map[string]string{"blocker": "PARENT", "statusIds": "st_todo"},
		}},
	}}}
	if err := st.CreateWorkflow(ctx, workspaceID, wf); err != nil {
		t.Fatal(err)
	}
	if err := st.AssignWorkflowToProject(ctx, workspaceID, projectID, workflowID); err != nil {
		t.Fatal(err)
	}
	service := &Service{Store: st}
	parent, _, err := service.CreateIssue(ctx, CreateIssueInput{ActorID: actorID, WorkspaceID: workspaceID, ProjectIDOrKey: projectID, Summary: "Parent", IssueTypeID: "it_task"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.CreateIssue(ctx, CreateIssueInput{ActorID: actorID, WorkspaceID: workspaceID, ProjectIDOrKey: projectID, Summary: "Missing parent", IssueTypeID: "it_subtask"}); err == nil {
		t.Fatal("sub-task creation accepted no parent")
	}
	child, _, err := service.CreateIssue(ctx, CreateIssueInput{ActorID: actorID, WorkspaceID: workspaceID, ProjectIDOrKey: projectID, Summary: "Child", IssueTypeID: "it_subtask", ParentIDOrKey: parent.Key})
	if err != nil {
		t.Fatal(err)
	}
	if child.Parent == nil || child.Parent.ID != parent.ID || child.Parent.Key != parent.Key {
		t.Fatalf("persisted parent = %+v", child.Parent)
	}
	if _, _, err := service.TransitionIssue(ctx, actorID, workspaceID, parent.Key, "complete"); err == nil {
		t.Fatal("parent transitioned while a child was blocked")
	}
	if _, _, err := service.TransitionIssue(ctx, actorID, workspaceID, child.Key, "complete"); err == nil {
		t.Fatal("child transitioned while its parent was blocked")
	}
	inProgress := "st_inprogress"
	parent, _, err = st.UpdateIssue(ctx, actorID, workspaceID, parent.ID, store.IssueUpdate{StatusID: &inProgress})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.TransitionIssue(ctx, actorID, workspaceID, child.Key, "complete"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.TransitionIssue(ctx, actorID, workspaceID, parent.Key, "complete"); err != nil {
		t.Fatal(err)
	}
	wf.Transitions[0].Actions = []workflow.Rule{{
		ID: "copy-parent", RuleKey: workflow.RuleCopyFieldValue,
		Parameters: map[string]string{"sourceFieldKey": "summary", "targetFieldKey": "description", "issueSource": "PARENT"},
	}}
	if err := st.CreateWorkflow(ctx, workspaceID, wf); err != nil {
		t.Fatal(err)
	}
	copyChild, _, err := service.CreateIssue(ctx, CreateIssueInput{ActorID: actorID, WorkspaceID: workspaceID, ProjectIDOrKey: projectID, Summary: "Copy child", IssueTypeID: "it_subtask", ParentIDOrKey: parent.Key})
	if err != nil {
		t.Fatal(err)
	}
	copyChild, _, err = service.TransitionIssue(ctx, actorID, workspaceID, copyChild.Key, "complete")
	if err != nil {
		t.Fatal(err)
	}
	if got := adf.PlainText(copyChild.Description); got != parent.Summary {
		t.Fatalf("parent summary copied to description = %q", got)
	}

	webhook, err := st.CreateWebhook(ctx, workspaceID, "https://example.invalid/transition", []string{"jira:issue_created"}, `summary = "will not match"`)
	if err != nil {
		t.Fatal(err)
	}
	wf.Transitions[0].Actions = []workflow.Rule{{
		ID: "notify", RuleKey: workflow.RuleTriggerWebhook, Parameters: map[string]string{"webhookId": webhook.ID},
	}}
	if err := st.CreateWorkflow(ctx, workspaceID, wf); err != nil {
		t.Fatal(err)
	}
	notified, _, err := service.CreateIssue(ctx, CreateIssueInput{ActorID: actorID, WorkspaceID: workspaceID, ProjectIDOrKey: projectID, Summary: "Notify", IssueTypeID: "it_task"})
	if err != nil {
		t.Fatal(err)
	}
	_, action, err := service.TransitionIssue(ctx, actorID, workspaceID, notified.Key, "complete")
	if err != nil {
		t.Fatal(err)
	}
	var payload models.IssueUpdatePayload
	if action == nil || json.Unmarshal(action.Payload, &payload) != nil || len(payload.TriggeredWebhookIDs) != 1 || payload.TriggeredWebhookIDs[0] != webhook.ID {
		t.Fatalf("triggered webhook action = %+v payload=%+v", action, payload)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE webhooks SET active=false WHERE id=$1`, webhook.ID); err != nil {
		t.Fatal(err)
	}
	blocked, _, err := service.CreateIssue(ctx, CreateIssueInput{ActorID: actorID, WorkspaceID: workspaceID, ProjectIDOrKey: projectID, Summary: "Inactive hook", IssueTypeID: "it_task"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.TransitionIssue(ctx, actorID, workspaceID, blocked.Key, "complete"); err == nil {
		t.Fatal("transition accepted an inactive webhook registration")
	}
}
