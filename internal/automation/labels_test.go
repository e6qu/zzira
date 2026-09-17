package automation

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"github.com/e6qu/zzira/internal/store"
)

// Jira's edit sets the labels the rule names, replacing what the work item
// carries, which is what separates it from the add label action.
func TestEditLabelsReplacesWhatTheWorkItemCarries(t *testing.T) {
	fx := newAutomationFixture(t)
	projectID := store.NewID("prj")
	if _, err := fx.store.Pool.Exec(fx.ctx, `INSERT INTO projects(id,workspace_id,key,name,workflow_id,lead_account_id) VALUES($1,$2,'LBLS','Labels','wf_default',$3)`, projectID, fx.ws, fx.admin); err != nil {
		t.Fatal(err)
	}
	issue, _, err := fx.store.CreateIssue(fx.ctx, fx.admin, projectID, "label target",
		json.RawMessage(`{"type":"doc","version":1,"content":[]}`), "st_todo", "it_task", "pr_medium", "", []string{"stale", "triage"}, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}

	base := "/gateway/api/automation/public/jira/" + fx.cloudID + "/rest/v1/rule"
	actions := []map[string]any{{"component": "ACTION", "type": "jira.issue.edit",
		"value": map[string]string{"field": "labels", "value": "reviewed, shipped"}}}
	created := fx.call(fx.admin, http.MethodPost, base, webhookRuleBody("Set labels", fx.admin, "ENABLED", true, actions), http.StatusCreated)
	uuid, _ := created["ruleUuid"].(string)
	rule, err := fx.service.Rule(fx.ctx, fx.ws, uuid)
	if err != nil {
		t.Fatal(err)
	}
	runner := &Runner{Service: fx.service}
	run := func() {
		t.Helper()
		if _, err := fx.service.TriggerWebhook(fx.ctx, rule.WebhookToken, "", json.RawMessage(`{"issues":["`+issue.Key+`"]}`)); err != nil {
			t.Fatal(err)
		}
		if err := runner.DrainOnce(fx.ctx, fx.ws); err != nil {
			t.Fatal(err)
		}
	}

	run()
	updated, err := fx.store.IssueByIDOrKey(fx.ctx, fx.ws, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	held := append([]string{}, updated.Labels...)
	slices.Sort(held)
	if !slices.Equal(held, []string{"reviewed", "shipped"}) {
		t.Fatalf("labels = %v, want the rule's labels replacing what it carried", updated.Labels)
	}

	// Running again changes nothing, as the other edits do.
	run()
	runs, err := fx.service.Runs(fx.ctx, fx.ws, uuid, 5)
	if err != nil || len(runs) != 2 {
		t.Fatalf("runs = %+v, err=%v", runs, err)
	}
	if runs[0].ChangedCount != 0 {
		t.Fatalf("a second run changed %d work items, want none", runs[0].ChangedCount)
	}
}
