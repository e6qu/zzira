package automation

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/adf"
	"github.com/e6qu/zzira/internal/store"
)

// Jira Automation edits a work item's description and logs work against it.
func TestEditDescriptionAndLogWorkActions(t *testing.T) {
	fx := newAutomationFixture(t)
	projectID := store.NewID("prj")
	if _, err := fx.store.Pool.Exec(fx.ctx, `INSERT INTO projects(id,workspace_id,key,name,workflow_id,lead_account_id) VALUES($1,$2,'EDIT','Edits','wf_default',$3)`, projectID, fx.ws, fx.admin); err != nil {
		t.Fatal(err)
	}
	issue, _, err := fx.store.CreateIssue(fx.ctx, fx.admin, projectID, "edit target",
		json.RawMessage(`{"type":"doc","version":1,"content":[]}`), "st_todo", "it_task", "pr_medium", "", nil, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}

	base := "/gateway/api/automation/public/jira/" + fx.cloudID + "/rest/v1/rule"
	actions := []map[string]any{
		{"component": "ACTION", "type": "jira.issue.edit", "value": map[string]string{"field": "description", "value": "Handled by {{rule.name}}"}},
		{"component": "ACTION", "type": "jira.issue.log-work", "value": map[string]string{"duration": "1h 30m", "comment": "automated sweep"}},
	}
	created := fx.call(fx.admin, http.MethodPost, base, webhookRuleBody("Edit and log", fx.admin, "ENABLED", true, actions), http.StatusCreated)
	uuid, _ := created["ruleUuid"].(string)
	rule, err := fx.service.Rule(fx.ctx, fx.ws, uuid)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fx.service.TriggerWebhook(fx.ctx, rule.WebhookToken, "", json.RawMessage(`{"issues":["`+issue.Key+`"]}`)); err != nil {
		t.Fatal(err)
	}
	runner := &Runner{Service: fx.service}
	if err := runner.DrainOnce(fx.ctx, fx.ws); err != nil {
		t.Fatal(err)
	}
	runs, err := fx.service.Runs(fx.ctx, fx.ws, uuid, 5)
	if err != nil || len(runs) != 1 || runs[0].State != "SUCCESS" {
		t.Fatalf("runs = %+v, err=%v", runs, err)
	}

	updated, err := fx.store.IssueByIDOrKey(fx.ctx, fx.ws, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if text := strings.TrimSpace(adf.PlainText(updated.Description)); text != "Handled by Edit and log" {
		t.Fatalf("description = %q, want the rendered smart value", text)
	}

	// The duration is read with the site's own time tracking.
	var seconds int
	if err := fx.store.Pool.QueryRow(fx.ctx, `SELECT COALESCE(SUM(time_spent_seconds),0) FROM worklogs WHERE issue_id=$1`, issue.ID).Scan(&seconds); err != nil {
		t.Fatal(err)
	}
	if seconds != 5400 {
		t.Fatalf("logged %d seconds, want 5400 for 1h 30m", seconds)
	}

	// Running again leaves the description alone, since it already reads that
	// way, but logging work is not a state the rule can find already set.
	if _, err := fx.service.TriggerWebhook(fx.ctx, rule.WebhookToken, "", json.RawMessage(`{"issues":["`+issue.Key+`"]}`)); err != nil {
		t.Fatal(err)
	}
	if err := runner.DrainOnce(fx.ctx, fx.ws); err != nil {
		t.Fatal(err)
	}
	if err := fx.store.Pool.QueryRow(fx.ctx, `SELECT COALESCE(SUM(time_spent_seconds),0) FROM worklogs WHERE issue_id=$1`, issue.ID).Scan(&seconds); err != nil {
		t.Fatal(err)
	}
	if seconds != 10800 {
		t.Fatalf("logged %d seconds after a second run, want 10800", seconds)
	}
}

// A duration the site cannot read stops the rule rather than logging nothing.
func TestLogWorkRefusesAnUnreadableDuration(t *testing.T) {
	fx := newAutomationFixture(t)
	projectID := store.NewID("prj")
	if _, err := fx.store.Pool.Exec(fx.ctx, `INSERT INTO projects(id,workspace_id,key,name,workflow_id,lead_account_id) VALUES($1,$2,'LOGW','Log work','wf_default',$3)`, projectID, fx.ws, fx.admin); err != nil {
		t.Fatal(err)
	}
	issue, _, err := fx.store.CreateIssue(fx.ctx, fx.admin, projectID, "log target",
		json.RawMessage(`{"type":"doc","version":1,"content":[]}`), "st_todo", "it_task", "pr_medium", "", nil, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	base := "/gateway/api/automation/public/jira/" + fx.cloudID + "/rest/v1/rule"
	actions := []map[string]any{{"component": "ACTION", "type": "jira.issue.log-work", "value": map[string]string{"duration": "whenever"}}}
	created := fx.call(fx.admin, http.MethodPost, base, webhookRuleBody("Bad duration", fx.admin, "ENABLED", true, actions), http.StatusCreated)
	uuid, _ := created["ruleUuid"].(string)
	rule, err := fx.service.Rule(fx.ctx, fx.ws, uuid)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fx.service.TriggerWebhook(fx.ctx, rule.WebhookToken, "", json.RawMessage(`{"issues":["`+issue.Key+`"]}`)); err != nil {
		t.Fatal(err)
	}
	_ = (&Runner{Service: fx.service}).DrainOnce(fx.ctx, fx.ws)
	runs, err := fx.service.Runs(fx.ctx, fx.ws, uuid, 5)
	if err != nil || len(runs) != 1 || runs[0].State != "FAILED" {
		t.Fatalf("runs = %+v, err=%v", runs, err)
	}
	if !strings.Contains(runs[0].Detail, "log work action") {
		t.Fatalf("detail = %q, want the log work refusal", runs[0].Detail)
	}
}

// A rule sets the fields Jira's own edit action reaches beyond the summary:
// who the work is for, how it was resolved, and what it was estimated at.
func TestEditActionSetsAssigneeResolutionAndEstimates(t *testing.T) {
	fx := newAutomationFixture(t)
	projectID := store.NewID("prj")
	if _, err := fx.store.Pool.Exec(fx.ctx, `INSERT INTO projects(id,workspace_id,key,name,workflow_id,lead_account_id) VALUES($1,$2,'EDFLD','Edit fields','wf_default',$3)`, projectID, fx.ws, fx.admin); err != nil {
		t.Fatal(err)
	}
	issue, _, err := fx.store.CreateIssue(fx.ctx, fx.admin, projectID, "edit fields",
		json.RawMessage(`{"type":"doc","version":1,"content":[]}`), "st_todo", "it_task", "pr_medium", "", nil, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}

	base := "/gateway/api/automation/public/jira/" + fx.cloudID + "/rest/v1/rule"
	actions := []map[string]any{
		{"component": "ACTION", "type": "jira.issue.edit", "value": map[string]string{"field": "assignee", "value": "ACTOR"}},
		{"component": "ACTION", "type": "jira.issue.edit", "value": map[string]string{"field": "resolution", "value": "Done"}},
		{"component": "ACTION", "type": "jira.issue.edit", "value": map[string]string{"field": "originalestimate", "value": "2h"}},
		{"component": "ACTION", "type": "jira.issue.edit", "value": map[string]string{"field": "remainingestimate", "value": "30m"}},
	}
	created := fx.call(fx.admin, http.MethodPost, base, webhookRuleBody("Edit fields", fx.admin, "ENABLED", true, actions), http.StatusCreated)
	uuid, _ := created["ruleUuid"].(string)
	rule, err := fx.service.Rule(fx.ctx, fx.ws, uuid)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fx.service.TriggerWebhook(fx.ctx, rule.WebhookToken, "", json.RawMessage(`{"issues":["`+issue.Key+`"]}`)); err != nil {
		t.Fatal(err)
	}
	runner := &Runner{Service: fx.service}
	if err := runner.DrainOnce(fx.ctx, fx.ws); err != nil {
		t.Fatal(err)
	}
	runs, err := fx.service.Runs(fx.ctx, fx.ws, uuid, 5)
	if err != nil || len(runs) != 1 || runs[0].State != "SUCCESS" {
		t.Fatalf("runs = %+v, err=%v", runs, err)
	}
	updated, err := fx.store.IssueByIDOrKey(fx.ctx, fx.ws, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Assignee == nil || updated.Assignee.ID != fx.admin {
		t.Fatalf("assignee = %+v, want the rule's actor", updated.Assignee)
	}
	if updated.Resolution == nil || updated.Resolution.Name != "Done" {
		t.Fatalf("resolution = %+v", updated.Resolution)
	}
	if updated.OriginalEstimateSeconds == nil || *updated.OriginalEstimateSeconds != 7200 {
		t.Fatalf("original estimate = %v, want 2h", updated.OriginalEstimateSeconds)
	}
	if updated.RemainingEstimateSeconds == nil || *updated.RemainingEstimateSeconds != 1800 {
		t.Fatalf("remaining estimate = %v, want 30m", updated.RemainingEstimateSeconds)
	}

	// Running again changes nothing: every field already reads as the rule
	// would leave it.
	if _, err := fx.service.TriggerWebhook(fx.ctx, rule.WebhookToken, "", json.RawMessage(`{"issues":["`+issue.Key+`"]}`)); err != nil {
		t.Fatal(err)
	}
	if err := runner.DrainOnce(fx.ctx, fx.ws); err != nil {
		t.Fatal(err)
	}
	runs, err = fx.service.Runs(fx.ctx, fx.ws, uuid, 5)
	if err != nil || len(runs) != 2 || runs[0].State != "NO_ACTIONS" {
		t.Fatalf("second run = %+v, err=%v", runs, err)
	}

	// A blank value clears what it names, as clearing the field does.
	clearing := []map[string]any{
		{"component": "ACTION", "type": "jira.issue.edit", "value": map[string]string{"field": "resolution", "value": ""}},
		{"component": "ACTION", "type": "jira.issue.edit", "value": map[string]string{"field": "remainingestimate", "value": ""}},
	}
	cleared := fx.call(fx.admin, http.MethodPost, base, webhookRuleBody("Clear fields", fx.admin, "ENABLED", true, clearing), http.StatusCreated)
	clearedUUID, _ := cleared["ruleUuid"].(string)
	clearRule, err := fx.service.Rule(fx.ctx, fx.ws, clearedUUID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fx.service.TriggerWebhook(fx.ctx, clearRule.WebhookToken, "", json.RawMessage(`{"issues":["`+issue.Key+`"]}`)); err != nil {
		t.Fatal(err)
	}
	if err := runner.DrainOnce(fx.ctx, fx.ws); err != nil {
		t.Fatal(err)
	}
	if updated, err = fx.store.IssueByIDOrKey(fx.ctx, fx.ws, issue.ID); err != nil {
		t.Fatal(err)
	}
	if updated.Resolution != nil || updated.RemainingEstimateSeconds != nil {
		t.Fatalf("cleared fields = %+v / %v", updated.Resolution, updated.RemainingEstimateSeconds)
	}

	// A resolution the site does not have is a mistake in the rule, not a
	// silent no-op.
	unknown := []map[string]any{{"component": "ACTION", "type": "jira.issue.edit", "value": map[string]string{"field": "resolution", "value": "Teleported"}}}
	made := fx.call(fx.admin, http.MethodPost, base, webhookRuleBody("Unknown resolution", fx.admin, "ENABLED", true, unknown), http.StatusCreated)
	unknownUUID, _ := made["ruleUuid"].(string)
	unknownRule, err := fx.service.Rule(fx.ctx, fx.ws, unknownUUID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fx.service.TriggerWebhook(fx.ctx, unknownRule.WebhookToken, "", json.RawMessage(`{"issues":["`+issue.Key+`"]}`)); err != nil {
		t.Fatal(err)
	}
	if err := runner.DrainOnce(fx.ctx, fx.ws); err == nil {
		t.Fatal("a rule naming a resolution the site does not have was treated as a success")
	}
}
