package automation

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

func deleteRuleBody(name, actor string, extra []map[string]any) map[string]any {
	actions := []map[string]any{{"component": "ACTION", "type": "jira.issue.delete", "value": map[string]any{}}}
	actions = append(actions, extra...)
	return map[string]any{"rule": map[string]any{
		"actor": map[string]string{"actor": actor, "type": "ACCOUNT_ID"}, "name": name,
		"description": "Delete", "state": "ENABLED", "labels": []string{},
		"ruleScopeARIs": []string{}, "components": actions,
		"trigger": map[string]any{"component": "TRIGGER", "type": WebhookTriggerType, "schemaVersion": 1,
			"value": map[string]any{"jql": "", "issuesFromWebhook": true}},
	}, "connections": []any{}}
}

// Jira deletes as the rule actor, and nothing after the delete runs for a work
// item that no longer exists.
func TestDeleteActionRemovesTheWorkItemAndStopsThere(t *testing.T) {
	fx := newAutomationFixture(t)
	projectID := store.NewID("prj")
	if _, err := fx.store.Pool.Exec(fx.ctx, `INSERT INTO projects(id,workspace_id,key,name,workflow_id,lead_account_id) VALUES($1,$2,'DELA','Delete action','wf_default',$3)`, projectID, fx.ws, fx.admin); err != nil {
		t.Fatal(err)
	}
	newIssue := func(summary string) *models.Issue {
		t.Helper()
		issue, _, err := fx.store.CreateIssue(fx.ctx, fx.admin, projectID, summary,
			json.RawMessage(`{"type":"doc","version":1,"content":[]}`), "st_todo", "it_task", "pr_medium", "", nil, nil, "", "")
		if err != nil {
			t.Fatal(err)
		}
		return issue
	}
	base := "/gateway/api/automation/public/jira/" + fx.cloudID + "/rest/v1/rule"
	runner := &Runner{Service: fx.service}

	run := func(actor, name string, extra []map[string]any, issueID string) Run {
		t.Helper()
		created := fx.call(fx.admin, http.MethodPost, base, deleteRuleBody(name, actor, extra), http.StatusCreated)
		uuid, _ := created["ruleUuid"].(string)
		rule, err := fx.service.Rule(fx.ctx, fx.ws, uuid)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fx.service.TriggerWebhook(fx.ctx, rule.WebhookToken, "", json.RawMessage(`{"issues":["`+issueID+`"]}`)); err != nil {
			t.Fatal(err)
		}
		_ = runner.DrainOnce(fx.ctx, fx.ws)
		runs, err := fx.service.Runs(fx.ctx, fx.ws, uuid, 5)
		if err != nil || len(runs) != 1 {
			t.Fatalf("runs = %+v, err=%v", runs, err)
		}
		return runs[0]
	}

	// A rule whose actor may delete removes the work item, and the comment
	// after it never runs because there is nothing left to comment on.
	target := newIssue("delete me")
	comment := []map[string]any{{"component": "ACTION", "type": "jira.issue.comment",
		"value": map[string]string{"comment": "this must never appear"}}}
	result := run(fx.admin, "Delete then comment", comment, target.ID)
	if result.State == "FAILED" {
		t.Fatalf("the delete run failed: %s", result.Detail)
	}
	var remaining int
	if err := fx.store.Pool.QueryRow(fx.ctx, `SELECT count(*) FROM issues WHERE id=$1`, target.ID).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatal("the rule did not delete the work item")
	}
	var comments int
	if err := fx.store.Pool.QueryRow(fx.ctx, `SELECT count(*) FROM comments WHERE issue_id=$1`, target.ID).Scan(&comments); err != nil {
		t.Fatal(err)
	}
	if comments != 0 {
		t.Fatal("an action ran after the work item was deleted")
	}

	// A project whose scheme names nobody who may delete refuses the rule,
	// exactly as it refuses a person.
	guardedID := store.NewID("prj")
	if _, err := fx.store.Pool.Exec(fx.ctx, `INSERT INTO projects(id,workspace_id,key,name,workflow_id,lead_account_id) VALUES($1,$2,'DELG','Delete guarded','wf_default',$3)`, guardedID, fx.ws, fx.admin); err != nil {
		t.Fatal(err)
	}
	grants := []store.PermissionGrantInput{}
	for _, permission := range []string{"BROWSE_PROJECTS", "CREATE_ISSUES", "EDIT_ISSUES", "ASSIGNABLE_USER"} {
		grants = append(grants, store.PermissionGrantInput{Permission: permission, HolderType: "anyone"})
	}
	scheme, err := fx.store.CreatePermissionScheme(fx.ctx, fx.ws, fx.admin, "No delete "+guardedID[len(guardedID)-6:], "No delete", grants)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := fx.store.AssignPermissionScheme(fx.ctx, fx.ws, fx.admin, guardedID, scheme.ID); err != nil {
		t.Fatal(err)
	}
	survivor, _, err := fx.store.CreateIssue(fx.ctx, fx.admin, guardedID, "keep me",
		json.RawMessage(`{"type":"doc","version":1,"content":[]}`), "st_todo", "it_task", "pr_medium", "", nil, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	refused := run(fx.member, "Delete without permission", nil, survivor.ID)
	if refused.State != "FAILED" || !strings.Contains(refused.Detail, "permission to delete") {
		t.Fatalf("run = %+v, want a failure naming the delete permission", refused)
	}
	if err := fx.store.Pool.QueryRow(fx.ctx, `SELECT count(*) FROM issues WHERE id=$1`, survivor.ID).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 1 {
		t.Fatal("the refused rule deleted the work item anyway")
	}
}
