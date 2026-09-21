package automation

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

func TestFieldConditionsSmartValuesAndEventMatching(t *testing.T) {
	issue := &models.Issue{Key: "EVT-7", Summary: "Fix login", Status: models.Status{ID: "st_todo", Name: "To Do"}, IssueType: models.IssueType{ID: "it_task", Name: "Task"},
		Labels: []string{"Backend"}, Assignee: &models.User{ID: "usr_1", DisplayName: "Ana"},
		Resolution: &models.Resolution{ID: "res_done", Name: "Done"}, CreatedAt: "2026-09-01T10:00:00Z",
		Parent: &models.IssueParent{ID: "iss_1", Key: "EVT-1", Summary: "Sign in epic"}}
	for _, check := range []struct {
		field, operator, value string
		want                   bool
	}{
		{"status", "EQUALS", "to do", true}, {"status", "EQUALS", "st_todo", true}, {"status", "NOT_EQUALS", "Done", true},
		{"labels", "EQUALS", "backend", true}, {"labels", "CONTAINS", "end", true}, {"summary", "CONTAINS", "LOGIN", true},
		{"assignee", "EQUALS", "usr_1", true}, {"assignee", "EQUALS", "ana", true}, {"reporter", "IS_EMPTY", "", true},
		{"priority", "IS_NOT_EMPTY", "", false}, {"duedate", "IS_EMPTY", "", true}, {"summary", "EQUALS", "fix", false},
		{"issuetype", "NOT_EQUALS", "task", false},
		{"resolution", "EQUALS", "done", true}, {"resolution", "IS_NOT_EMPTY", "", true},
		{"key", "STARTS_WITH", "evt-", true}, {"key", "ENDS_WITH", "-7", true},
		{"summary", "NOT_CONTAINS", "logout", true}, {"summary", "NOT_CONTAINS", "login", false},
		{"status", "IS_ONE_OF", "done, to do", true}, {"status", "IS_NOT_ONE_OF", "done, in progress", true},
		{"parent", "EQUALS", "evt-1", true}, {"parent", "CONTAINS", "sign in", true},
		{"created", "LESS_THAN", "2026-09-02", true}, {"created", "GREATER_THAN", "2026-09-02", false},
		{"created", "GREATER_THAN", "2026-08-31T23:00:00Z", true},
		// Ordering holds for nothing on a field that is not a date.
		{"summary", "GREATER_THAN", "a", false},
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

func TestBranchComponentsValidate(t *testing.T) {
	valid := `{"components":[{"component":"BRANCH","type":"jira.issue.related","value":{"relatedType":"linked","linkTypes":["blocks"]},"children":[{"component":"CONDITION","type":"jira.jql.condition","value":{"jql":"status != Done"}},{"component":"ACTION","type":"jira.issue.comment"}]}]}`
	if _, err := ruleComponents(json.RawMessage(valid)); err != nil {
		t.Fatalf("a branch with an action was refused: %v", err)
	}
	for _, payload := range []string{
		`{"components":[{"component":"BRANCH","type":"jira.issue.related","value":{"relatedType":"sub-tasks"},"children":[{"component":"BRANCH","type":"jira.issue.related","value":{"relatedType":"parent"},"children":[{"component":"ACTION","type":"jira.issue.comment"}]}]}]}`,
		`{"components":[{"component":"BRANCH","type":"jira.issue.related","value":{"relatedType":"epic"},"children":[{"component":"ACTION","type":"jira.issue.comment"}]}]}`,
		`{"components":[{"component":"BRANCH","type":"jira.issue.related","value":{"relatedType":"parent"},"children":[{"component":"CONDITION","type":"jira.jql.condition"}]}]}`,
		`{"components":[{"component":"BRANCH","type":"jira.jql.related","value":{"relatedType":"parent"},"children":[{"component":"ACTION","type":"jira.issue.comment"}]}]}`,
	} {
		if _, err := ruleComponents(json.RawMessage(payload)); err == nil {
			t.Fatalf("%s was accepted", payload)
		}
	}
}

func TestBranchesRunForRelatedWork(t *testing.T) {
	fx := newAutomationFixture(t)
	projectID := store.NewID("prj")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := fx.store.Pool.Exec(fx.ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO projects(id,workspace_id,key,name,workflow_id,lead_account_id) VALUES($1,$2,'BRN','Branches','wf_default',$3)`, projectID, fx.ws, fx.admin)
	exec(`SELECT provision_workspace_issue_link_types($1)`, fx.ws)
	linkType := func(jiraID int) string {
		t.Helper()
		var id string
		if err := fx.store.Pool.QueryRow(fx.ctx, `SELECT id FROM issue_link_types WHERE workspace_id=$1 AND jira_id=$2`, fx.ws, jiraID).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	create := func(summary, issueType, parentID string) *models.Issue {
		t.Helper()
		issue, _, err := fx.store.CreateIssue(fx.ctx, fx.admin, projectID, summary, json.RawMessage(`{"type":"doc","version":1,"content":[]}`), "st_todo", issueType, "pr_medium", "", nil, nil, "", parentID)
		if err != nil {
			t.Fatal(err)
		}
		return issue
	}
	train := create("Release train", "it_task", "")
	notes := create("Write notes", "it_subtask", train.ID)
	tag := create("Tag build", "it_subtask", train.ID)
	deploy := create("Deploy", "it_task", "")
	other := create("Other work", "it_task", "")
	// The train blocks the deploy, and other work only relates to the train.
	if _, _, err := fx.store.CreateIssueLink(fx.ctx, fx.admin, fx.ws, linkType(10000), deploy.ID, train.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := fx.store.CreateIssueLink(fx.ctx, fx.admin, fx.ws, linkType(10003), other.ID, train.ID); err != nil {
		t.Fatal(err)
	}
	action := func(actionType string, value map[string]string) map[string]any {
		return map[string]any{"component": "ACTION", "type": actionType, "value": value}
	}
	branch := func(value map[string]any, children ...map[string]any) map[string]any {
		return map[string]any{"component": "BRANCH", "type": "jira.issue.related", "value": value, "children": children}
	}
	scheduled := func(name, query string, components ...map[string]any) string {
		t.Helper()
		raw, _ := json.Marshal(ruleBody(name, fx.admin, "ENABLED", query, components))
		uuid, err := fx.service.CreateRule(fx.ctx, fx.ws, fx.admin, raw)
		if err != nil {
			t.Fatal(err)
		}
		if err := fx.service.EnqueueNow(fx.ctx, fx.ws, uuid); err != nil {
			t.Fatal(err)
		}
		return uuid
	}
	rules := []string{
		scheduled("Train", "key = "+train.Key,
			action("jira.issue.add-label", map[string]string{"label": "train"}),
			branch(map[string]any{"relatedType": "sub-tasks"},
				map[string]any{"component": "CONDITION", "type": "jira.issue.condition", "value": map[string]string{"field": "summary", "operator": "CONTAINS", "value": "notes"}},
				action("jira.issue.add-label", map[string]string{"label": "from-{{triggerIssue.key}}"})),
			branch(map[string]any{"relatedType": "linked", "linkTypes": []string{"blocks"}},
				action("jira.issue.comment", map[string]string{"comment": "Blocked by {{triggerIssue.key}} ({{triggerIssue.summary}}), not {{issue.key}}"}))),
		scheduled("Sub-task", "key = "+tag.Key,
			branch(map[string]any{"relatedType": "parent"}, action("jira.issue.add-label", map[string]string{"label": "child-{{triggerIssue.key}}"})),
			branch(map[string]any{"relatedType": "linked", "linkTypes": []string{"is blocked by"}}, action("jira.issue.add-label", map[string]string{"label": "never"}))),
		scheduled("Deploy", "key = "+deploy.Key,
			branch(map[string]any{"relatedType": "linked", "linkTypes": []string{"Blocks"}}, action("jira.issue.add-label", map[string]string{"label": "blocker"}))),
	}
	runner := &Runner{Service: fx.service}
	for range 6 {
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
	if got := labels(train); !strings.Contains(got, "train") || !strings.Contains(got, "child-"+tag.Key) || !strings.Contains(got, "blocker") || strings.Contains(got, "never") {
		t.Fatalf("train labels = %s", got)
	}
	if got := labels(notes); got != "from-"+train.Key {
		t.Fatalf("notes labels = %s", got)
	}
	if got := labels(tag); got != "" {
		t.Fatalf("a sub-task failing the branch condition was changed: %s", got)
	}
	comments := func(issue *models.Issue) int {
		t.Helper()
		var count int
		if err := fx.store.Pool.QueryRow(fx.ctx, `SELECT count(*) FROM comments WHERE issue_id=$1 AND body::text LIKE '%Blocked by '||$2||' (Release train), not '||$3||'%'`, issue.ID, train.Key, issue.Key).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	if comments(deploy) != 1 || comments(other) != 0 {
		t.Fatalf("blocked comments: deploy %d, other %d", comments(deploy), comments(other))
	}
	for _, uuid := range rules {
		if runs, err := fx.service.Runs(fx.ctx, fx.ws, uuid, 5); err != nil || len(runs) != 1 || runs[0].State != "SUCCESS" || runs[0].ChangedCount != 1 {
			t.Fatalf("rule %s runs = %+v, %v", uuid, runs, err)
		}
	}
}

func TestBuildTemplateRuleValidates(t *testing.T) {
	cloudID := "11111111-2222-3333-4444-555555555555"
	site, project := SiteARI(cloudID), ProjectARI(cloudID, "10000")
	for _, check := range []struct {
		template, home, state string
		parameters            map[string]TemplateValue
		code                  string
	}{
		{"nope", site, "", nil, "automation.template.invalid"},
		{"manual-assign-to-me", "ari:cloud:jira::site/elsewhere", "", nil, "automation.rule_home.invalid"},
		{"manual-assign-to-me", site, "DRAFT", nil, "automation.state.invalid"},
		{"scheduled-assign-unassigned", site, "", nil, "automation.parameter.required"},
		{"scheduled-label-stale-work", site, "", map[string]TemplateValue{"days": {Type: "NUMBER", Value: "5"}}, "automation.parameter.invalid"},
		{"manual-assign-to-me", site, "", map[string]TemplateValue{"extra": {Type: "TEXT", Value: "x"}}, "automation.parameter.unknown"},
	} {
		if _, err := BuildTemplateRule(cloudID, check.template, check.home, check.state, "", check.parameters); err == nil || err.Code != check.code {
			t.Fatalf("%+v: %v", check, err)
		}
	}
	body, err := BuildTemplateRule(cloudID, "scheduled-label-stale-work", project, "DISABLED", " Night labels ", map[string]TemplateValue{"days": {Type: "NUMBER", Value: 5.0}, "label": {Value: "old"}})
	if err != nil {
		t.Fatal(err)
	}
	var built struct {
		Rule struct {
			Name, State   string
			RuleScopeARIs []string
			Trigger       struct {
				Type  string
				Value struct{ JQL string }
			}
		}
	}
	if json.Unmarshal(body, &built) != nil || built.Rule.Name != "Night labels" || built.Rule.State != "DISABLED" || len(built.Rule.RuleScopeARIs) != 1 || built.Rule.RuleScopeARIs[0] != project ||
		built.Rule.Trigger.Type != "jira.jql.scheduled" || built.Rule.Trigger.Value.JQL != "updated <= -5d AND statusCategory != Done" {
		t.Fatalf("built rule = %s", body)
	}
	if len(Templates()) != len(ruleTemplates) || Templates()[0].Categories[0] != "Popular" {
		t.Fatalf("templates = %+v", Templates())
	}
}

func TestLinkedEventRuleStartsForTheOutwardWorkItem(t *testing.T) {
	fx := newAutomationFixture(t)
	projectID := store.NewID("prj")
	if _, err := fx.store.Pool.Exec(fx.ctx, `INSERT INTO projects(id,workspace_id,key,name,workflow_id,lead_account_id) VALUES($1,$2,'LNKE','Linked events','wf_default',$3)`, projectID, fx.ws, fx.admin); err != nil {
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
	labels := func(issue *models.Issue) []string {
		t.Helper()
		fresh, err := fx.store.IssueByIDOrKey(fx.ctx, fx.ws, issue.ID)
		if err != nil {
			t.Fatal(err)
		}
		return fresh.Labels
	}
	blocker, blocked := create("Blocking work"), create("Blocked work")
	blocksID, err := fx.store.LinkTypeIDByName(fx.ctx, fx.ws, "Blocks")
	if err != nil || blocksID == "" {
		t.Fatalf("seeded Blocks link type = %q, %v", blocksID, err)
	}

	body, _ := json.Marshal(map[string]any{"rule": map[string]any{
		"actor": map[string]string{"actor": fx.admin, "type": "ACCOUNT_ID"}, "name": "Label linked work", "state": "ENABLED",
		"components": []map[string]any{{"component": "ACTION", "type": "jira.issue.add-label", "value": map[string]string{"label": "linked"}}},
		"trigger":    map[string]any{"component": "TRIGGER", "type": "jira.issue.event.trigger:linked", "schemaVersion": 1, "value": map[string]any{}},
	}, "connections": []any{}})
	if _, err := fx.service.CreateRule(fx.ctx, fx.ws, fx.admin, body); err != nil {
		t.Fatal(err)
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
	// Work linked before the rule existed is never replayed.
	drain()

	// A link joins two work items and starts one run, for the outward side.
	if _, _, err := fx.store.CreateIssueLink(fx.ctx, fx.admin, fx.ws, blocksID, blocked.ID, blocker.ID); err != nil {
		t.Fatal(err)
	}
	drain()
	if got := labels(blocker); !slices.Contains(got, "linked") {
		t.Fatalf("the outward work item's labels = %v, want the rule to have run", got)
	}
	if got := labels(blocked); slices.Contains(got, "linked") {
		t.Fatalf("the inward work item's labels = %v, want the rule not to have run for it", got)
	}
}

func TestRelatedWorkItemsCondition(t *testing.T) {
	fx := newAutomationFixture(t)
	projectID := store.NewID("prj")
	if _, err := fx.store.Pool.Exec(fx.ctx, `INSERT INTO projects(id,workspace_id,key,name,workflow_id,lead_account_id) VALUES($1,$2,'RELC','Related conditions','wf_default',$3)`, projectID, fx.ws, fx.admin); err != nil {
		t.Fatal(err)
	}
	create := func(summary, issueType, parentID string) *models.Issue {
		t.Helper()
		issue, _, err := fx.store.CreateIssue(fx.ctx, fx.admin, projectID, summary, json.RawMessage(`{"type":"doc","version":1,"content":[]}`), "st_todo", issueType, "pr_medium", "", nil, nil, "", parentID)
		if err != nil {
			t.Fatal(err)
		}
		return issue
	}
	labels := func(issue *models.Issue) []string {
		t.Helper()
		fresh, err := fx.store.IssueByIDOrKey(fx.ctx, fx.ws, issue.ID)
		if err != nil {
			t.Fatal(err)
		}
		return fresh.Labels
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
	run := func(name, key, relatedType, jql, label string) {
		t.Helper()
		condition := map[string]any{"component": "CONDITION", "schemaVersion": 1, "type": "jira.issue.related.condition",
			"value": map[string]any{"relatedType": relatedType, "jql": jql}}
		action := map[string]any{"component": "ACTION", "type": "jira.issue.add-label", "value": map[string]string{"label": label}}
		body, _ := json.Marshal(map[string]any{"rule": map[string]any{
			"actor": map[string]string{"actor": fx.admin, "type": "ACCOUNT_ID"}, "name": name, "state": "ENABLED",
			"components": []map[string]any{condition, action},
			"trigger":    map[string]any{"component": "TRIGGER", "type": "jira.jql.scheduled", "schemaVersion": 1, "value": map[string]any{"jql": "key = " + key, "intervalMinutes": 60, "timezone": "UTC"}},
		}, "connections": []any{}})
		uuid, err := fx.service.CreateRule(fx.ctx, fx.ws, fx.admin, body)
		if err != nil {
			t.Fatal(err)
		}
		if err := fx.service.EnqueueNow(fx.ctx, fx.ws, uuid); err != nil {
			t.Fatal(err)
		}
		drain()
	}

	parent := create("Parent work", "it_task", "")
	create("Finished detail", "it_subtask", parent.ID)
	create("Open detail", "it_subtask", parent.ID)
	lonely := create("Work with no children", "it_task", "")

	// A sub-task matching the query holds the condition.
	run("Has an open sub-task", parent.Key, "sub-tasks", "summary ~ \"Open\"", "has-open")
	if got := labels(parent); !slices.Contains(got, "has-open") {
		t.Fatalf("parent labels = %v, want the condition to have held", got)
	}

	// No sub-task matching the query stops the rule.
	run("Has a blocked sub-task", parent.Key, "sub-tasks", "summary ~ \"Blocked\"", "has-blocked")
	if got := labels(parent); slices.Contains(got, "has-blocked") {
		t.Fatalf("parent labels = %v, want the condition to have failed", got)
	}

	// A blank query asks only whether related work exists.
	run("Has any sub-task", parent.Key, "sub-tasks", "", "has-children")
	if got := labels(parent); !slices.Contains(got, "has-children") {
		t.Fatalf("parent labels = %v, want any sub-task to hold", got)
	}
	run("Lonely has any sub-task", lonely.Key, "sub-tasks", "", "lonely-children")
	if got := labels(lonely); slices.Contains(got, "lonely-children") {
		t.Fatalf("childless labels = %v, want the condition to have failed", got)
	}
}

// Jira has triggers of its own for a work item being assigned and for an
// attachment arriving; both start a run for the work item they happened to.
func TestAssignedAndAttachmentTriggers(t *testing.T) {
	fx := newAutomationFixture(t)
	projectID := store.NewID("prj")
	if _, err := fx.store.Pool.Exec(fx.ctx, `INSERT INTO projects(id,workspace_id,key,name,workflow_id,lead_account_id) VALUES($1,$2,'TRIG','Trigger events','wf_default',$3)`, projectID, fx.ws, fx.admin); err != nil {
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
	labels := func(issue *models.Issue) []string {
		t.Helper()
		fresh, err := fx.store.IssueByIDOrKey(fx.ctx, fx.ws, issue.ID)
		if err != nil {
			t.Fatal(err)
		}
		return fresh.Labels
	}
	assigned, attached, untouched := create("Needs an owner"), create("Needs a file"), create("Left alone")

	for _, rule := range []struct{ name, trigger, label string }{
		{"Label assigned work", "jira.issue.event.trigger:assigned", "assigned"},
		{"Label work with files", "jira.issue.attachment.added", "has-file"},
	} {
		body, _ := json.Marshal(map[string]any{"rule": map[string]any{
			"actor": map[string]string{"actor": fx.admin, "type": "ACCOUNT_ID"}, "name": rule.name, "state": "ENABLED",
			"components": []map[string]any{{"component": "ACTION", "type": "jira.issue.add-label", "value": map[string]string{"label": rule.label}}},
			"trigger":    map[string]any{"component": "TRIGGER", "type": rule.trigger, "schemaVersion": 1, "value": map[string]any{}},
		}, "connections": []any{}})
		if _, err := fx.service.CreateRule(fx.ctx, fx.ws, fx.admin, body); err != nil {
			t.Fatalf("%s: %v", rule.name, err)
		}
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
	// Work that existed before the rules is never replayed.
	drain()

	owner := fx.admin
	if _, _, err := fx.store.UpdateIssue(fx.ctx, fx.admin, fx.ws, assigned.ID, store.IssueUpdate{AssigneeID: &owner}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := fx.store.CreateAttachment(fx.ctx, fx.admin, fx.ws, attached.ID, "plan.txt", "text/plain", 12, "blob_plan"); err != nil {
		t.Fatal(err)
	}
	drain()

	if got := labels(assigned); len(got) != 1 || got[0] != "assigned" {
		t.Fatalf("assigned work labels = %v", got)
	}
	if got := labels(attached); len(got) != 1 || got[0] != "has-file" {
		t.Fatalf("work with a file labels = %v", got)
	}
	// Neither trigger touches work nothing happened to.
	if got := labels(untouched); len(got) != 0 {
		t.Fatalf("untouched work labels = %v", got)
	}
}

// TestEventRulesRunOnWhatHappensAroundWork covers the events whose subject a
// rule cannot load when it runs: a work item that has been deleted, and the
// versions and sprints that work moves through. Each starts a rule once, with
// what it happened to.
func TestEventRulesRunOnWhatHappensAroundWork(t *testing.T) {
	fx := newAutomationFixture(t)
	projectID, otherProjectID := store.NewID("prj"), store.NewID("prj")
	if _, err := fx.store.Pool.Exec(fx.ctx, `INSERT INTO projects(id,workspace_id,key,name,workflow_id,lead_account_id) VALUES($1,$2,'ARD','Around','wf_default',$3),($4,$2,'ARE','Elsewhere','wf_default',$3)`,
		projectID, fx.ws, fx.admin, otherProjectID); err != nil {
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
	mustRule := func(name, triggerType string, value map[string]any, components ...map[string]any) string {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"rule": map[string]any{
			"actor": map[string]string{"actor": fx.admin, "type": "ACCOUNT_ID"}, "name": name, "state": "ENABLED",
			"canOtherRuleTrigger": false, "components": components,
			"trigger": map[string]any{"component": "TRIGGER", "type": triggerType, "schemaVersion": 1, "value": value},
		}, "connections": []any{}})
		uuid, err := fx.service.CreateRule(fx.ctx, fx.ws, fx.admin, body)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return uuid
	}
	// A rule that writes down what happened: creating a work item is the one
	// thing a rule can do when the event brought none.
	record := func(summary string) map[string]any {
		return map[string]any{"component": "ACTION", "type": "jira.issue.create",
			"value": map[string]string{"issueTypeId": "it_task", "projectId": projectID, "summary": summary}}
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
	summaries := func() []string {
		t.Helper()
		rows, err := fx.store.Pool.Query(fx.ctx, `SELECT summary FROM issues WHERE workspace_id=$1 ORDER BY key`, fx.ws)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		found := []string{}
		for rows.Next() {
			var summary string
			if err := rows.Scan(&summary); err != nil {
				t.Fatal(err)
			}
			found = append(found, summary)
		}
		return found
	}
	written := func(want string) bool {
		for _, summary := range summaries() {
			if summary == want {
				return true
			}
		}
		return false
	}

	// A trigger with no work item takes no JQL: there is nothing to match.
	body, _ := json.Marshal(map[string]any{"rule": map[string]any{
		"actor": map[string]string{"actor": fx.admin, "type": "ACCOUNT_ID"}, "name": "Deleted with JQL", "state": "ENABLED",
		"components": []map[string]any{record("never")},
		"trigger":    map[string]any{"component": "TRIGGER", "type": "jira.issue.event.trigger:deleted", "schemaVersion": 1, "value": map[string]any{"jql": "project = ARD"}},
	}, "connections": []any{}})
	if _, err := fx.service.CreateRule(fx.ctx, fx.ws, fx.admin, body); err == nil || !strings.Contains(err.Error(), "takes no JQL") {
		t.Fatalf("deleted trigger with JQL: %v", err)
	}

	deleted := mustRule("Note the deletion", "jira.issue.event.trigger:deleted", map[string]any{},
		record("Gone: {{deletedIssue.key}} {{deletedIssue.summary}}"))
	moved := mustRule("Note the move", "jira.issue.event.trigger:moved", map[string]any{},
		map[string]any{"component": "ACTION", "type": "jira.issue.add-label", "value": map[string]string{"label": "moved"}})
	mustRule("Note the release", "jira.version.event.trigger:released", map[string]any{},
		record("Released: {{version.name}}"))
	mustRule("Note the version", "jira.version.event.trigger:created", map[string]any{},
		record("Planned: {{version.name}}"))
	mustRule("Note the sprint", "jira.sprint.event.trigger:started", map[string]any{},
		record("Started: {{sprint.name}}"))
	drain()

	// A deletion starts a rule that can still say what went.
	goner := create("Work that goes")
	drain()
	if _, err := fx.service.Commands.DeleteIssue(fx.ctx, fx.admin, fx.ws, goner.ID, "no longer needed"); err != nil {
		t.Fatal(err)
	}
	drain()
	if !written("Gone: " + goner.Key + " Work that goes") {
		t.Fatalf("the deletion wrote nothing: %v", summaries())
	}
	if runs, err := fx.service.Runs(fx.ctx, fx.ws, deleted, 10); err != nil || len(runs) != 1 || runs[0].State != "SUCCESS" {
		t.Fatalf("deleted rule runs = %+v, %v", runs, err)
	}

	// A move starts a rule for the work item it moved, which is still there.
	traveller := create("Work that moves")
	drain()
	move := store.IssueMove{ProjectID: otherProjectID, IssueTypeID: "it_task", StatusID: "st_todo"}
	if _, _, err := fx.store.MoveIssue(fx.ctx, fx.admin, fx.ws, traveller.ID, move); err != nil {
		t.Fatal(err)
	}
	drain()
	fresh, err := fx.store.IssueByIDOrKey(fx.ctx, fx.ws, traveller.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(fresh.Labels, "moved") {
		t.Fatalf("the move did not start the rule: %v", fresh.Labels)
	}
	if runs, err := fx.service.Runs(fx.ctx, fx.ws, moved, 10); err != nil || len(runs) != 1 {
		t.Fatalf("moved rule runs = %+v, %v", runs, err)
	}

	// A version says when it is planned and when it is released, and only
	// then: saving a released version again is not another release.
	version, err := fx.store.SaveVersion(fx.ctx, fx.ws, fx.admin, projectID, "", store.VersionUpdate{Name: ptr("1.0")})
	if err != nil {
		t.Fatal(err)
	}
	drain()
	if !written("Planned: 1.0") {
		t.Fatalf("the new version wrote nothing: %v", summaries())
	}
	released := true
	if _, err := fx.store.SaveVersion(fx.ctx, fx.ws, fx.admin, projectID, version.ID, store.VersionUpdate{Released: &released}); err != nil {
		t.Fatal(err)
	}
	drain()
	if !written("Released: 1.0") {
		t.Fatalf("the release wrote nothing: %v", summaries())
	}
	description := "shipped"
	if _, err := fx.store.SaveVersion(fx.ctx, fx.ws, fx.admin, projectID, version.ID, store.VersionUpdate{Description: &description}); err != nil {
		t.Fatal(err)
	}
	drain()
	count := 0
	for _, summary := range summaries() {
		if summary == "Released: 1.0" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("saving a released version again released it again: %d notes", count)
	}

	// A sprint says when it starts, and a rename while it runs does not.
	board, err := fx.store.CreateBoard(fx.ctx, fx.admin, fx.ws, store.BoardCreate{Name: "Around board", Type: "scrum", ProjectID: projectID})
	if err != nil {
		t.Fatal(err)
	}
	sprint, _, err := fx.store.CreateSprint(fx.ctx, fx.admin, fx.ws, board.ID, "Sprint 1", "Ship it")
	if err != nil {
		t.Fatal(err)
	}
	drain()
	start := time.Now().UTC()
	end := start.Add(14 * 24 * time.Hour)
	if _, _, err := fx.store.UpdateSprint(fx.ctx, fx.admin, fx.ws, sprint.ID, store.SprintUpdate{
		Name: "Sprint 1", Goal: "Ship it", State: "active", StartDate: &start, EndDate: &end,
	}); err != nil {
		t.Fatal(err)
	}
	drain()
	if !written("Started: Sprint 1") {
		t.Fatalf("the sprint start wrote nothing: %v", summaries())
	}
	if _, _, err := fx.store.UpdateSprint(fx.ctx, fx.admin, fx.ws, sprint.ID, store.SprintUpdate{
		Name: "Sprint one", Goal: "Ship it", State: "active", StartDate: &start, EndDate: &end,
	}); err != nil {
		t.Fatal(err)
	}
	drain()
	starts := 0
	for _, summary := range summaries() {
		if strings.HasPrefix(summary, "Started: ") {
			starts++
		}
	}
	if starts != 1 {
		t.Fatalf("renaming a running sprint started it again: %d notes", starts)
	}
}

func ptr[T any](value T) *T { return &value }
