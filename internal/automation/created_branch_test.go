package automation

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/store"
)

// Jira's "for all created issues": the rule raises work and then acts on it,
// which is the only way to reach work that did not exist when the rule began.
func TestBranchesRunOverTheWorkTheRuleCreated(t *testing.T) {
	fx := newAutomationFixture(t)
	projectID := store.NewID("prj")
	if _, err := fx.store.Pool.Exec(fx.ctx,
		`INSERT INTO projects(id,workspace_id,key,name,workflow_id,lead_account_id) VALUES($1,$2,'CRB','Created branch','wf_default',$3)`,
		projectID, fx.ws, fx.admin); err != nil {
		t.Fatal(err)
	}
	trigger, _, err := fx.store.CreateIssue(fx.ctx, fx.admin, projectID, "Release train",
		json.RawMessage(`{"type":"doc","version":1,"content":[]}`), "st_todo", "it_task", "pr_medium", "", nil, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	components := []map[string]any{
		{"component": "ACTION", "type": "jira.issue.create", "value": map[string]string{
			"issueTypeId": "it_task", "projectId": projectID, "summary": "Write the release notes"}},
		{"component": "ACTION", "type": "jira.issue.create", "value": map[string]string{
			"issueTypeId": "it_task", "projectId": projectID, "summary": "Tell the customers"}},
		{"component": "BRANCH", "type": "jira.issue.related", "value": map[string]any{"relatedType": "created"},
			"children": []map[string]any{
				{"component": "ACTION", "type": "jira.issue.add-label", "value": map[string]string{"label": "raised-by-{{triggerIssue.key}}"}},
			}},
	}
	raw, _ := json.Marshal(ruleBody("Raise and label", fx.admin, "ENABLED", "key = "+trigger.Key, components))
	uuid, err := fx.service.CreateRule(fx.ctx, fx.ws, fx.admin, raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := fx.service.EnqueueNow(fx.ctx, fx.ws, uuid); err != nil {
		t.Fatal(err)
	}
	runner := &Runner{Service: fx.service}
	for range 3 {
		if err := runner.DrainOnce(fx.ctx, fx.ws); err != nil {
			t.Fatal(err)
		}
	}
	labelled := func(summary string) string {
		t.Helper()
		var labels []string
		if err := fx.store.Pool.QueryRow(fx.ctx,
			`SELECT COALESCE(labels,'{}') FROM issues WHERE workspace_id=$1 AND summary=$2`, fx.ws, summary).Scan(&labels); err != nil {
			t.Fatalf("%s: %v", summary, err)
		}
		return strings.Join(labels, ",")
	}
	want := "raised-by-" + trigger.Key
	if got := labelled("Write the release notes"); got != want {
		t.Fatalf("the first work item the rule raised = %q, want %q", got, want)
	}
	if got := labelled("Tell the customers"); got != want {
		t.Fatalf("the second work item the rule raised = %q, want %q", got, want)
	}
	// The work item the rule ran for is not work the rule created.
	if got := labelled("Release train"); got != "" {
		t.Fatalf("the trigger work item was labelled as created work: %q", got)
	}

	// Running again raises nothing -- the work is already there -- so there
	// is nothing to branch over, and the rule does not fail over it.
	if err := fx.service.EnqueueNow(fx.ctx, fx.ws, uuid); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := runner.DrainOnce(fx.ctx, fx.ws); err != nil {
			t.Fatal(err)
		}
	}
	runs, err := fx.service.Runs(fx.ctx, fx.ws, uuid, 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, run := range runs {
		if run.State == "FAILED" {
			t.Fatalf("a run failed: %s", run.Detail)
		}
	}
}
