package automation

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
)

// assignmentFixture gives the workspace a project and two people who may be
// assigned its work, and returns the project and the issues it creates.
func assignmentRuleBody(name, actor, method string) map[string]any {
	actions := []map[string]any{{"component": "ACTION", "type": "jira.issue.assign", "value": map[string]string{"method": method}}}
	return map[string]any{"rule": map[string]any{
		"actor": map[string]string{"actor": actor, "type": "ACCOUNT_ID"}, "name": name,
		"description": "Assignment", "state": "ENABLED", "labels": []string{},
		"ruleScopeARIs": []string{}, "components": actions,
		"trigger": map[string]any{"component": "TRIGGER", "type": WebhookTriggerType, "schemaVersion": 1,
			"value": map[string]any{"jql": "", "issuesFromWebhook": true}},
	}, "connections": []any{}}
}

// Jira Automation picks who to assign work to when the rule names a method
// rather than one person.
func TestAssignmentMethodsPickFromAssignablePeople(t *testing.T) {
	fx := newAutomationFixture(t)
	projectID := store.NewID("prj")
	if _, err := fx.store.Pool.Exec(fx.ctx, `INSERT INTO projects(id,workspace_id,key,name,workflow_id,lead_account_id) VALUES($1,$2,'ASGN','Assignment','wf_default',$3)`, projectID, fx.ws, fx.admin); err != nil {
		t.Fatal(err)
	}
	newIssue := func(summary string) string {
		t.Helper()
		issue, _, err := fx.store.CreateIssue(fx.ctx, fx.admin, projectID, summary,
			json.RawMessage(`{"type":"doc","version":1,"content":[]}`), "st_todo", "it_task", "pr_medium", "", nil, nil, "", "")
		if err != nil {
			t.Fatal(err)
		}
		return issue.ID
	}

	candidates, err := fx.store.AssignableUsersForProject(fx.ctx, fx.ws, projectID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) == 0 {
		t.Skip("the project offers nobody to assign work to")
	}

	runner := &Runner{Service: fx.service}
	base := "/gateway/api/automation/public/jira/" + fx.cloudID + "/rest/v1/rule"
	assignBy := func(method, issueID string) string {
		t.Helper()
		created := fx.call(fx.admin, http.MethodPost, base, assignmentRuleBody(method+" "+issueID[len(issueID)-6:], fx.admin, method), http.StatusCreated)
		uuid, _ := created["ruleUuid"].(string)
		rule, err := fx.service.Rule(fx.ctx, fx.ws, uuid)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fx.service.TriggerWebhook(fx.ctx, rule.WebhookToken, "", json.RawMessage(`{"issues":["`+issueID+`"]}`)); err != nil {
			t.Fatal(err)
		}
		if err := runner.DrainOnce(fx.ctx, fx.ws); err != nil {
			t.Fatal(err)
		}
		runs, err := fx.service.Runs(fx.ctx, fx.ws, uuid, 5)
		if err != nil || len(runs) != 1 {
			t.Fatalf("runs = %+v, err=%v", runs, err)
		}
		if runs[0].State == "FAILED" {
			t.Fatalf("%s run failed: %s", method, runs[0].Detail)
		}
		issue, err := fx.store.IssueByIDOrKey(fx.ctx, fx.ws, issueID)
		if err != nil {
			t.Fatal(err)
		}
		if issue.Assignee == nil {
			t.Fatalf("%s left the work item unassigned", method)
		}
		return issue.Assignee.ID
	}

	assignable := map[string]bool{}
	for _, candidate := range candidates {
		assignable[candidate.ID] = true
	}

	// Every method picks from the people the project may assign work to.
	for _, method := range []string{AssignRoundRobin, AssignBalanced, AssignRandom} {
		picked := assignBy(method, newIssue("assign by "+method))
		if !assignable[picked] {
			t.Fatalf("%s picked %q, who the project cannot assign work to", method, picked)
		}
	}

	// Balanced workload gives the work to whoever carries the least.
	if len(candidates) > 1 {
		heavy := candidates[0].ID
		for index := 0; index < 3; index++ {
			issueID := newIssue("existing load")
			if _, _, err := fx.service.Commands.UpdateIssue(fx.ctx, commands.UpdateIssueInput{
				ActorID: fx.admin, WorkspaceID: fx.ws, IssueIDOrKey: issueID, AssigneeID: &heavy,
			}); err != nil {
				t.Fatal(err)
			}
		}
		if picked := assignBy(AssignBalanced, newIssue("balance me")); picked == heavy {
			counts, _ := fx.store.OpenWorkByAssignee(fx.ctx, projectID)
			t.Fatalf("balanced picked the busiest person: counts=%v", counts)
		}
	}
}

// A method the site does not offer stops the rule.
func TestAssignmentRefusesAnUnknownMethod(t *testing.T) {
	fx := newAutomationFixture(t)
	projectID := store.NewID("prj")
	if _, err := fx.store.Pool.Exec(fx.ctx, `INSERT INTO projects(id,workspace_id,key,name,workflow_id,lead_account_id) VALUES($1,$2,'ASGX','Assignment x','wf_default',$3)`, projectID, fx.ws, fx.admin); err != nil {
		t.Fatal(err)
	}
	issue, _, err := fx.store.CreateIssue(fx.ctx, fx.admin, projectID, "unknown method",
		json.RawMessage(`{"type":"doc","version":1,"content":[]}`), "st_todo", "it_task", "pr_medium", "", nil, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	base := "/gateway/api/automation/public/jira/" + fx.cloudID + "/rest/v1/rule"
	created := fx.call(fx.admin, http.MethodPost, base, assignmentRuleBody("Unknown method", fx.admin, "alphabetical"), http.StatusCreated)
	uuid, _ := created["ruleUuid"].(string)
	rule, err := fx.service.Rule(fx.ctx, fx.ws, uuid)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fx.service.TriggerWebhook(fx.ctx, rule.WebhookToken, "", json.RawMessage(`{"issues":["`+issue.ID+`"]}`)); err != nil {
		t.Fatal(err)
	}
	_ = (&Runner{Service: fx.service}).DrainOnce(fx.ctx, fx.ws)
	runs, err := fx.service.Runs(fx.ctx, fx.ws, uuid, 5)
	if err != nil || len(runs) != 1 || runs[0].State != "FAILED" {
		t.Fatalf("runs = %+v, err=%v", runs, err)
	}
	if !strings.Contains(runs[0].Detail, "cannot pick by") {
		t.Fatalf("detail = %q, want the unknown method refusal", runs[0].Detail)
	}
}
