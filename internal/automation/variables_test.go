package automation

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

// A rule names a value, reads it back in a later action, and a branch over a
// query runs the actions it holds for the work the query matched.
func TestVariablesAndJQLBranches(t *testing.T) {
	fx := newAutomationFixture(t)
	projectID := store.NewID("prj")
	if _, err := fx.store.Pool.Exec(fx.ctx,
		`INSERT INTO projects(id,workspace_id,key,name,workflow_id,lead_account_id) VALUES($1,$2,'VAR','Variables','wf_default',$3)`,
		projectID, fx.ws, fx.admin); err != nil {
		t.Fatal(err)
	}
	create := func(summary string, labels []string) *models.Issue {
		t.Helper()
		issue, _, err := fx.store.CreateIssue(fx.ctx, fx.admin, projectID, summary, json.RawMessage(`{"type":"doc","version":1,"content":[]}`), "st_todo", "it_task", "pr_medium", "", labels, nil, "", "")
		if err != nil {
			t.Fatal(err)
		}
		return issue
	}
	release := create("Release 9.1", nil)
	firstLate := create("Late one", []string{"late"})
	secondLate := create("Late two", []string{"late"})
	action := func(actionType string, value map[string]string) map[string]any {
		return map[string]any{"component": "ACTION", "type": actionType, "value": value}
	}
	variable := func(name, value string) map[string]any {
		return action(VariableActionType, map[string]string{"variableName": name, "variableValue": value})
	}
	branch := func(value map[string]any, children ...map[string]any) map[string]any {
		return map[string]any{"component": "BRANCH", "type": "jira.issue.related", "value": value, "children": children}
	}
	scheduled := func(name, query string, components ...map[string]any) {
		t.Helper()
		raw, _ := json.Marshal(ruleBody(name, fx.admin, "ENABLED", query, components))
		uuid, err := fx.service.CreateRule(fx.ctx, fx.ws, fx.admin, raw)
		if err != nil {
			t.Fatal(err)
		}
		if err := fx.service.EnqueueNow(fx.ctx, fx.ws, uuid); err != nil {
			t.Fatal(err)
		}
	}
	// The rule names where it is running, comments with it, and then runs a
	// branch over everything labelled late -- work the trigger never named.
	// Inside the branch the variable is set again, and the outer value has to
	// survive: what a branch names belongs to the branch.
	scheduled("Release", "key = "+release.Key,
		variable("channel", "release-{{issue.key}}"),
		action("jira.issue.comment", map[string]string{"comment": "Announced on {{channel}}"}),
		branch(map[string]any{"relatedType": "jql", "jql": "labels = late ORDER BY created ASC"},
			variable("channel", "late-{{issue.key}}"),
			action("jira.issue.add-label", map[string]string{"label": "{{channel}}"})),
		action("jira.issue.comment", map[string]string{"comment": "Still on {{channel}}"}))
	runner := &Runner{Service: fx.service}
	for range 3 {
		if err := runner.DrainOnce(fx.ctx, fx.ws); err != nil {
			t.Fatal(err)
		}
	}
	labels := func(issue *models.Issue) string {
		t.Helper()
		fresh, err := fx.store.IssueByIDOrKey(fx.ctx, fx.ws, issue.ID)
		if err != nil {
			t.Fatal(err)
		}
		return strings.Join(fresh.Labels, ",")
	}
	if got := labels(firstLate); !strings.Contains(got, "late-"+firstLate.Key) {
		t.Fatalf("the branch did not run for the work its query matched: %s", got)
	}
	if got := labels(secondLate); !strings.Contains(got, "late-"+secondLate.Key) {
		t.Fatalf("the branch ran for one work item only: %s", got)
	}
	// Each work item in the branch saw its own value, not the one before it.
	if got := labels(firstLate); strings.Contains(got, "late-"+secondLate.Key) {
		t.Fatalf("a branch carried a variable to the next work item: %s", got)
	}
	comment := func(text string) int {
		t.Helper()
		var count int
		if err := fx.store.Pool.QueryRow(fx.ctx,
			`SELECT count(*) FROM comments WHERE issue_id=$1 AND body::text LIKE '%'||$2||'%'`, release.ID, text).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	if comment("Announced on release-"+release.Key) != 1 {
		t.Fatal("the rule did not read back the variable it named")
	}
	if comment("Still on release-"+release.Key) != 1 {
		t.Fatal("a variable set inside a branch replaced the one the rule named outside it")
	}
}

// A rule that cannot work says so when it is written.
func TestVariableActionsAreValidated(t *testing.T) {
	refused := []string{
		`{"variableName":"","variableValue":"x"}`,
		`{"variableName":"1channel","variableValue":"x"}`,
		`{"variableName":"with space","variableValue":"x"}`,
		`{"variableName":"issue","variableValue":"x"}`,
		`{"variableName":"webResponse","variableValue":"x"}`,
		`{"variableName":"userInputs","variableValue":"x"}`,
	}
	for _, value := range refused {
		if _, err := validateVariableAction(json.RawMessage(value)); err == nil {
			t.Fatalf("%s was accepted", value)
		}
	}
	if _, err := validateVariableAction(json.RawMessage(`{"variableName":"release_note-2","variableValue":""}`)); err != nil {
		t.Fatalf("a named variable holding nothing was refused: %v", err)
	}
	// A branch over a query needs one, and it has to parse.
	payloads := []string{
		`{"rule":{"components":[{"component":"BRANCH","type":"jira.issue.related","value":{"relatedType":"jql"},"children":[{"component":"ACTION","type":"jira.issue.add-label","value":{"label":"x"}}]}]}}`,
		`{"rule":{"components":[{"component":"BRANCH","type":"jira.issue.related","value":{"relatedType":"jql","jql":"status = "},"children":[{"component":"ACTION","type":"jira.issue.add-label","value":{"label":"x"}}]}]}}`,
		`{"rule":{"components":[{"component":"ACTION","type":"jira.create.variable","value":{"variableName":"now","variableValue":"x"}}]}}`,
	}
	for _, payload := range payloads {
		if _, err := ruleComponents(json.RawMessage(payload)); err == nil {
			t.Fatalf("%s was accepted", payload)
		}
	}
}

// A run holds only so many variables, and each only so much text.
func TestVariableLimits(t *testing.T) {
	runner := &Runner{}
	run := &claimedRun{}
	for index := range maxVariables {
		name := "v" + strings.Repeat("x", index%3) + string(rune('a'+index%26)) + string(rune('a'+index/26))
		if err := runner.setVariable(run, "value", variableActionValue{VariableName: name}); err != nil {
			t.Fatalf("variable %d of %d was refused: %v", index+1, maxVariables, err)
		}
	}
	if err := runner.setVariable(run, "value", variableActionValue{VariableName: "one-too-many"}); err == nil {
		t.Fatalf("a rule held more than %d variables", maxVariables)
	}
	// Replacing one the run already holds is not a new variable.
	if err := runner.setVariable(run, "again", variableActionValue{VariableName: "vaa"}); err != nil {
		t.Fatalf("replacing a variable was refused: %v", err)
	}
	if err := runner.setVariable(run, strings.Repeat("x", maxVariableLength+1), variableActionValue{VariableName: "vaa"}); err == nil {
		t.Fatal("a variable longer than the limit was accepted")
	}
}
