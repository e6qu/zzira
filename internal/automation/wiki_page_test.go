package automation

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/store"
)

func pageRuleBody(name, actor, spaceKey string) map[string]any {
	actions := []map[string]any{{"component": "ACTION", "type": WikiPageActionType,
		"value": map[string]string{"spaceKey": spaceKey}}}
	return map[string]any{"rule": map[string]any{
		"actor": map[string]string{"actor": actor, "type": "ACCOUNT_ID"}, "name": name,
		"description": "Create page", "state": "ENABLED", "labels": []string{},
		"ruleScopeARIs": []string{}, "components": actions,
		"trigger": map[string]any{"component": "TRIGGER", "type": WebhookTriggerType, "schemaVersion": 1,
			"value": map[string]any{"jql": "", "issuesFromWebhook": true}},
	}, "connections": []any{}}
}

// Jira Automation raises a page in a space, as the rule actor.
func TestCreatePageActionRaisesAPageAsTheRuleActor(t *testing.T) {
	fx := newAutomationFixture(t)
	projectID := store.NewID("prj")
	if _, err := fx.store.Pool.Exec(fx.ctx, `INSERT INTO projects(id,workspace_id,key,name,workflow_id,lead_account_id) VALUES($1,$2,'PAGE','Pages','wf_default',$3)`, projectID, fx.ws, fx.admin); err != nil {
		t.Fatal(err)
	}
	issue, _, err := fx.store.CreateIssue(fx.ctx, fx.admin, projectID, "page subject",
		json.RawMessage(`{"type":"doc","version":1,"content":[]}`), "st_todo", "it_task", "pr_medium", "", nil, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}

	spaceKey := "AUT" + strings.ToUpper(store.NewID("s")[len(store.NewID("s"))-4:])
	space, err := fx.service.Commands.CreateWikiSpace(fx.ctx, fx.ws, fx.admin, spaceKey, "Automation pages", "Raised by rules", false)
	if err != nil {
		t.Skip("the wiki space could not be created: " + err.Error())
	}

	base := "/gateway/api/automation/public/jira/" + fx.cloudID + "/rest/v1/rule"
	runner := &Runner{Service: fx.service}
	run := func(actor, name, key, issueID string) Run {
		t.Helper()
		created := fx.call(fx.admin, http.MethodPost, base, pageRuleBody(name, actor, key), http.StatusCreated)
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

	result := run(fx.admin, "Raise a page", spaceKey, issue.ID)
	if result.State == "FAILED" {
		t.Fatalf("the page run failed: %s", result.Detail)
	}
	var title, body string
	if err := fx.store.Pool.QueryRow(fx.ctx,
		`SELECT title, COALESCE(body,'') FROM wiki_pages WHERE space_id=$1 ORDER BY created_at DESC LIMIT 1`, space.ID).Scan(&title, &body); err != nil {
		t.Fatalf("no page was raised in the space: %v", err)
	}
	if !strings.Contains(title, issue.Key) {
		t.Fatalf("page title = %q, want the work item it was raised for", title)
	}
	if !strings.Contains(body, issue.Key) || !strings.Contains(body, "Raise a page") {
		t.Fatalf("page body = %q, want the rule and the work item named", body)
	}

	// A space the rule actor cannot see stops the rule.
	missing := run(fx.admin, "Raise in nothing", "NOSUCHSPACE", issue.ID)
	if missing.State != "FAILED" || !strings.Contains(missing.Detail, "no space") {
		t.Fatalf("run = %+v, want a failure naming the missing space", missing)
	}
}
