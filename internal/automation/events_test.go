package automation

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

func TestFieldConditionsSmartValuesAndEventMatching(t *testing.T) {
	issue := &models.Issue{Key: "EVT-7", Summary: "Fix login", Status: models.Status{ID: "st_todo", Name: "To Do"}, IssueType: models.IssueType{ID: "it_task", Name: "Task"},
		Labels: []string{"Backend"}, Assignee: &models.User{ID: "usr_1", DisplayName: "Ana"}}
	for _, check := range []struct {
		field, operator, value string
		want                   bool
	}{
		{"status", "EQUALS", "to do", true}, {"status", "EQUALS", "st_todo", true}, {"status", "NOT_EQUALS", "Done", true},
		{"labels", "EQUALS", "backend", true}, {"labels", "CONTAINS", "end", true}, {"summary", "CONTAINS", "LOGIN", true},
		{"assignee", "EQUALS", "usr_1", true}, {"assignee", "EQUALS", "ana", true}, {"reporter", "IS_EMPTY", "", true},
		{"priority", "IS_NOT_EMPTY", "", false}, {"duedate", "IS_EMPTY", "", true}, {"summary", "EQUALS", "fix", false},
		{"issuetype", "NOT_EQUALS", "task", false},
	} {
		if got := fieldConditionHolds(issue, check.field, check.operator, check.value); got != check.want {
			t.Fatalf("%s %s %q = %v", check.field, check.operator, check.value, got)
		}
	}
	runner := &Runner{Service: &Service{}}
	rendered, err := runner.renderSmartValues(context.Background(), &claimedRun{RuleName: "Triage"}, issue, "{{issue.key}}: {{ issue.summary }} [{{issue.status.name}}] by {{issue.assignee.displayName}} for {{rule.name}}{{unknown.value}} on {{now.jiraDate}}")
	if want := "EVT-7: Fix login [To Do] by Ana for Triage on " + time.Now().UTC().Format("2006-01-02"); err != nil || rendered != want {
		t.Fatalf("rendered %q, %v; want %q", rendered, err, want)
	}

	transition := map[string]models.ChangeItem{"status": {From: "st_todo", To: "st_done"}}
	rule := eventRule{UUID: "rule-a", Event: "transitioned", fromIDs: map[string]bool{}, toIDs: map[string]bool{"st_done": true}}
	other := "rule-b"
	self := "rule-a"
	if !rule.matches("updated", transition, loggedAction{}) || rule.matches("updated", map[string]models.ChangeItem{"status": {To: "st_inprogress"}}, loggedAction{}) ||
		rule.matches("updated", transition, loggedAction{CausedBy: &other}) || rule.matches("created", nil, loggedAction{}) {
		t.Fatal("transition matching is wrong")
	}
	rule.AllowOtherRules = true
	if !rule.matches("updated", transition, loggedAction{CausedBy: &other}) || rule.matches("updated", transition, loggedAction{CausedBy: &self}) {
		t.Fatal("a rule must start from other rules only when allowed, and never from itself")
	}
	fields := eventRule{UUID: "rule-c", Event: "field_changed", fields: map[string]bool{"summary": true}}
	if !fields.matches("updated", map[string]models.ChangeItem{"Summary": {}}, loggedAction{}) || fields.matches("updated", map[string]models.ChangeItem{"labels": {}}, loggedAction{}) {
		t.Fatal("field change matching is wrong")
	}
	comment, _ := json.Marshal(models.CommentUpsertPayload{Comment: models.Comment{IssueID: "iss_1"}})
	if event, issueID, _ := actionEvent(loggedAction{EntityType: models.EntityComment, Payload: comment, First: true}); event != "commented" || issueID != "iss_1" {
		t.Fatalf("comment event = %q %q", event, issueID)
	}
	if event, _, _ := actionEvent(loggedAction{EntityType: models.EntityComment, Payload: comment}); event != "" {
		t.Fatal("an edited comment started a commented event")
	}
	for _, payload := range []string{
		`{"components":[{"component":"CONDITION","type":"jira.issue.unknown"},{"component":"ACTION","type":"jira.issue.comment"}]}`,
		`{"components":[{"component":"CONDITION","type":"jira.jql.condition"}]}`,
		`{"components":[{"component":"BRANCH","type":"jira.issue.related"},{"component":"ACTION","type":"jira.issue.comment"}]}`,
	} {
		if _, err := ruleComponents(json.RawMessage(payload)); err == nil {
			t.Fatalf("%s was accepted", payload)
		}
	}
}

func TestEventRulesRunOnWorkItemEvents(t *testing.T) {
	fx := newAutomationFixture(t)
	projectID := store.NewID("prj")
	if _, err := fx.store.Pool.Exec(fx.ctx, `INSERT INTO projects(id,workspace_id,key,name,workflow_id,lead_account_id) VALUES($1,$2,'EVT','Events','wf_default',$3)`, projectID, fx.ws, fx.admin); err != nil {
		t.Fatal(err)
	}
	create := func(summary string) *models.Issue {
		t.Helper()
		issue, _, err := fx.store.CreateIssue(fx.ctx, fx.admin, projectID, summary, json.RawMessage(`{"type":"doc","version":1,"content":[]}`), "st_todo", "it_task", "pr_medium", "", nil, nil, "", "")
		if err != nil {
			t.Fatal(err)
		}
		return issue
	}
	reload := func(issue *models.Issue) *models.Issue {
		t.Helper()
		fresh, err := fx.store.IssueByIDOrKey(fx.ctx, fx.ws, issue.ID)
		if err != nil {
			t.Fatal(err)
		}
		return fresh
	}
	rule := func(name, triggerType string, value map[string]any, allowOthers bool, components ...map[string]any) (string, error) {
		body, _ := json.Marshal(map[string]any{"rule": map[string]any{
			"actor": map[string]string{"actor": fx.admin, "type": "ACCOUNT_ID"}, "name": name, "state": "ENABLED",
			"canOtherRuleTrigger": allowOthers, "components": components,
			"trigger": map[string]any{"component": "TRIGGER", "type": triggerType, "schemaVersion": 1, "value": value},
		}, "connections": []any{}})
		return fx.service.CreateRule(fx.ctx, fx.ws, fx.admin, body)
	}
	mustRule := func(name, triggerType string, value map[string]any, allowOthers bool, components ...map[string]any) string {
		t.Helper()
		uuid, err := rule(name, triggerType, value, allowOthers, components...)
		if err != nil {
			t.Fatal(err)
		}
		return uuid
	}
	action := func(actionType string, value map[string]string) map[string]any {
		return map[string]any{"component": "ACTION", "type": actionType, "value": value}
	}
	condition := func(conditionType string, value map[string]string) map[string]any {
		return map[string]any{"component": "CONDITION", "type": conditionType, "value": value}
	}
	if _, err := rule("No fields", "jira.issue.field.changed", map[string]any{}, false, action("jira.issue.add-label", map[string]string{"label": "x"})); err == nil || !strings.Contains(err.Error(), "between 1 and 20 fields") {
		t.Fatalf("field change trigger without fields: %v", err)
	}
	runner := &Runner{Service: fx.service}
	drain := func() {
		t.Helper()
		for range 25 {
			if err := runner.DrainOnce(fx.ctx, fx.ws); err != nil {
				t.Fatal(err)
			}
		}
	}

	// Work created before the rules could run is never replayed.
	earlier := create("Existing work")
	created := mustRule("Label new work", "jira.issue.event.trigger:created", map[string]any{"jql": "project = EVT"}, false,
		condition("jira.issue.condition", map[string]string{"field": "status", "operator": "EQUALS", "value": "To Do"}),
		action("jira.issue.add-label", map[string]string{"label": "new-{{issue.key}}"}))
	unmatched := mustRule("Never labels", "jira.issue.event.trigger:created", map[string]any{}, false,
		condition("jira.jql.condition", map[string]string{"jql": "labels = nothing-here"}),
		action("jira.issue.add-label", map[string]string{"label": "never"}))
	mustRule("Rename on comment", "jira.issue.event.trigger:commented", map[string]any{}, false,
		action("jira.issue.edit", map[string]string{"field": "summary", "value": "{{issue.summary}} (commented by {{initiator.displayName}})"}))
	mustRule("Label renamed work", "jira.issue.field.changed", map[string]any{"fields": []string{"summary"}}, false,
		action("jira.issue.add-label", map[string]string{"label": "renamed"}))
	mustRule("Label chained rename", "jira.issue.field.changed", map[string]any{"fields": []string{"Summary"}}, true,
		action("jira.issue.add-label", map[string]string{"label": "chained"}))
	mustRule("Comment on start", "jira.issue.event.trigger:transitioned", map[string]any{"toStatusIds": []string{"st_inprogress"}}, false,
		action("jira.issue.comment", map[string]string{"comment": "Started by {{initiator.displayName}} for {{rule.name}}"}))
	drain()

	issue := create("Fresh work")
	drain()
	issue = reload(issue)
	if strings.Join(issue.Labels, ",") != "new-"+issue.Key {
		t.Fatalf("created work labels = %v", issue.Labels)
	}
	if labels := reload(earlier).Labels; len(labels) != 0 {
		t.Fatalf("earlier work was replayed: %v", labels)
	}
	if runs, err := fx.service.Runs(fx.ctx, fx.ws, created, 10); err != nil || len(runs) != 1 || runs[0].State != "SUCCESS" || runs[0].MatchedCount != 1 {
		t.Fatalf("created rule runs = %+v, %v", runs, err)
	}
	if runs, err := fx.service.Runs(fx.ctx, fx.ws, unmatched, 10); err != nil || len(runs) != 1 || runs[0].State != "NO_ACTIONS" {
		t.Fatalf("unmatched rule runs = %+v, %v", runs, err)
	}

	// A comment renames the work; only the rule that allows other rules to
	// trigger it labels the rename.
	if _, _, err := fx.service.Commands.AddComment(fx.ctx, commands.AddCommentInput{ActorID: fx.member, WorkspaceID: fx.ws, IssueIDOrKey: issue.ID, PlainText: "Looks good"}); err != nil {
		t.Fatal(err)
	}
	drain()
	issue = reload(issue)
	if issue.Summary != "Fresh work (commented by "+fx.member+")" || !strings.Contains(strings.Join(issue.Labels, ","), "chained") || strings.Contains(strings.Join(issue.Labels, ","), "renamed") {
		t.Fatalf("after comment: summary %q labels %v", issue.Summary, issue.Labels)
	}

	// Starting the work comments on it, and that comment starts no rule.
	workflow, err := fx.store.WorkflowForProjectAndIssueType(fx.ctx, projectID, issue.IssueType.ID)
	if err != nil {
		t.Fatal(err)
	}
	started := false
	for _, transition := range workflow.Available(issue.Status.ID) {
		if transition.To == "st_inprogress" {
			if _, _, err := fx.service.Commands.TransitionIssue(fx.ctx, fx.admin, fx.ws, issue.ID, transition.ID); err != nil {
				t.Fatal(err)
			}
			started = true
			break
		}
	}
	if !started {
		t.Fatal("no transition to In Progress")
	}
	drain()
	var startComments int
	if err := fx.store.Pool.QueryRow(fx.ctx, `SELECT count(*) FROM comments WHERE issue_id=$1 AND body::text LIKE '%Started by `+fx.admin+` for Comment on start%'`, issue.ID).Scan(&startComments); err != nil {
		t.Fatal(err)
	}
	issue = reload(issue)
	if startComments != 1 || strings.Count(issue.Summary, "commented by") != 1 {
		t.Fatalf("after transition: %d start comments, summary %q", startComments, issue.Summary)
	}
	var caused int
	if err := fx.store.Pool.QueryRow(fx.ctx, `SELECT count(*) FROM actions WHERE workspace_id=$1 AND automation_rule_uuid IS NOT NULL`, fx.ws).Scan(&caused); err != nil || caused < 4 {
		t.Fatalf("actions caused by rules = %d, %v", caused, err)
	}
}
