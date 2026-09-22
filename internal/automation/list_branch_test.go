package automation

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/store"
)

// A list is read as Jira reads one: a JSON array by its elements, anything
// else comma-separated, and an object element keeps its JSON so paths into it
// can be read.
func TestBranchListItems(t *testing.T) {
	for rendered, want := range map[string]string{
		``:                                  "",
		`  `:                                "",
		`urgent, blocked , ,release`:        "urgent | blocked | release",
		`["one","two"]`:                     "one | two",
		`[1, 2.5, true, null]`:              "1 | 2.5 | true | ",
		`one`:                               "one",
		`[{"name":"Ada"},{"name":"Linus"}]`: `{"name":"Ada"} | {"name":"Linus"}`,
	} {
		items := branchListItems(rendered)
		texts := make([]string, 0, len(items))
		for _, item := range items {
			texts = append(texts, item.Text)
		}
		if got := strings.Join(texts, " | "); got != want {
			t.Fatalf("%q = %q, want %q", rendered, got, want)
		}
	}
	// An object element carries its JSON; a string element does not.
	objects := branchListItems(`[{"name":"Ada"},"plain"]`)
	if len(objects) != 2 || len(objects[0].Data) == 0 || len(objects[1].Data) != 0 {
		t.Fatalf("object items = %+v", objects)
	}
}

// An item that names a work item the actor can see is the work item the
// branch runs for; one that names none leaves the work item as it was.
func TestListBranchRunsForTheWorkAnItemNames(t *testing.T) {
	fx := newAutomationFixture(t)
	projectID := store.NewID("prj")
	if _, err := fx.store.Pool.Exec(fx.ctx,
		`INSERT INTO projects(id,workspace_id,key,name,workflow_id,lead_account_id) VALUES($1,$2,'LSI','List items','wf_default',$3)`,
		projectID, fx.ws, fx.admin); err != nil {
		t.Fatal(err)
	}
	issue, _, err := fx.store.CreateIssue(fx.ctx, fx.admin, projectID, "Named by a list",
		json.RawMessage(`{"type":"doc","version":1,"content":[]}`), "st_todo", "it_task", "pr_medium", "", nil, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	runner := &Runner{Service: fx.service}
	run := &claimedRun{WorkspaceID: fx.ws, ActorID: fx.admin}
	named := listItem{Text: issue.Key, Data: json.RawMessage(`{"key":"` + issue.Key + `"}`)}
	found, err := runner.listItemIssue(fx.ctx, run, named)
	if err != nil || found == nil || found.ID != issue.ID {
		t.Fatalf("an item naming a work item read as %+v, %v", found, err)
	}
	for _, item := range []listItem{
		{Text: "urgent"},
		{Text: `{"name":"Ada"}`, Data: json.RawMessage(`{"name":"Ada"}`)},
		{Text: "ZZZ-404", Data: json.RawMessage(`{"key":"ZZZ-404"}`)},
	} {
		got, err := runner.listItemIssue(fx.ctx, run, item)
		if err != nil || got != nil {
			t.Fatalf("%s read as a work item: %+v, %v", item.Text, got, err)
		}
	}
}

// A smart value that is one name and nothing else names a variable, which is
// how a branch over {{lookupIssues}} reads the list rather than its text.
func TestWholeSmartValueNamesAVariable(t *testing.T) {
	for text, want := range map[string]string{
		"{{lookupIssues}}":       "lookupIssues",
		"  {{ lookupIssues }}  ": "lookupIssues",
		"{{issue.labels}}":       "issue.labels",
		"late: {{lookupIssues}}": "",
		"{{a}} {{b}}":            "",
		"{{}}":                   "",
		"labels = late":          "",
	} {
		name, found := wholeSmartValue(text)
		if want == "" && found {
			t.Fatalf("%q read as the variable %q", text, name)
		}
		if want != "" && (!found || name != want) {
			t.Fatalf("%q = %q, want %q", text, name, want)
		}
	}
}

// A branch over a list needs something to run over and a name for each item.
func TestListBranchValidation(t *testing.T) {
	refused := []branchValue{
		{RelatedType: listBranchType, VariableName: "item"},
		{RelatedType: listBranchType, SmartValue: "{{issue.labels}}"},
		{RelatedType: listBranchType, SmartValue: "{{issue.labels}}", VariableName: "an item"},
		{RelatedType: listBranchType, SmartValue: "{{issue.labels}}", VariableName: "issue"},
	}
	for _, value := range refused {
		if err := validateListBranch(value); err == nil {
			t.Fatalf("%+v was accepted", value)
		}
	}
	if err := validateListBranch(branchValue{RelatedType: listBranchType, SmartValue: "{{issue.labels}}", VariableName: "label"}); err != nil {
		t.Fatalf("a branch over the work item's labels was refused: %v", err)
	}
	// A rule the runner cannot execute says so where it is read.
	payload := `{"rule":{"components":[{"component":"BRANCH","type":"jira.issue.related","value":{"relatedType":"smart-values","smartValue":"{{issue.labels}}"},"children":[{"component":"ACTION","type":"jira.issue.comment","value":{"comment":"x"}}]}]}}`
	if _, err := ruleComponents(json.RawMessage(payload)); err == nil {
		t.Fatal("a branch over a list with no name for its items was accepted")
	}
}

// An item that is an object is readable by its parts, not only as JSON.
func TestBranchItemsReadTheirParts(t *testing.T) {
	fx := newAutomationFixture(t)
	runner := &Runner{Service: fx.service}
	items := branchListItems(`[{"name":"Ada","team":{"key":"CORE"}}]`)
	if len(items) != 1 || len(items[0].Data) == 0 {
		t.Fatalf("items = %+v", items)
	}
	run := &claimedRun{
		WorkspaceID: fx.ws, ActorID: fx.admin, RuleName: "Each owner",
		Variables:    map[string]string{"owner": items[0].Text},
		VariableData: map[string]json.RawMessage{"owner": items[0].Data},
	}
	rendered, err := runner.renderSmartValues(fx.ctx, run, nil, "{{owner.name}} of {{owner.team.key}} ({{owner.missing}})")
	if err != nil {
		t.Fatal(err)
	}
	if rendered != "Ada of CORE ()" {
		t.Fatalf("rendered %q", rendered)
	}
}

// The rule runs its actions once for each item, reading the item by the name
// the branch gave it, and what the branch names does not outlive it.
func TestBranchesRunOverAList(t *testing.T) {
	fx := newAutomationFixture(t)
	projectID := store.NewID("prj")
	if _, err := fx.store.Pool.Exec(fx.ctx,
		`INSERT INTO projects(id,workspace_id,key,name,workflow_id,lead_account_id) VALUES($1,$2,'LST','Lists','wf_default',$3)`,
		projectID, fx.ws, fx.admin); err != nil {
		t.Fatal(err)
	}
	issue, _, err := fx.store.CreateIssue(fx.ctx, fx.admin, projectID, "Release train",
		json.RawMessage(`{"type":"doc","version":1,"content":[]}`), "st_todo", "it_task", "pr_medium", "",
		[]string{"docs", "api"}, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	components := []map[string]any{
		{"component": "ACTION", "type": VariableActionType, "value": map[string]string{"variableName": "topic", "variableValue": "none"}},
		{"component": "BRANCH", "type": "jira.issue.related",
			"value": map[string]any{"relatedType": listBranchType, "smartValue": "{{issue.labels}}", "variableName": "topic"},
			"children": []map[string]any{
				{"component": "ACTION", "type": "jira.issue.comment", "value": map[string]string{"comment": "Checking {{topic}} on {{issue.key}}"}},
			}},
		{"component": "ACTION", "type": "jira.issue.comment", "value": map[string]string{"comment": "Done with {{topic}}"}},
	}
	raw, _ := json.Marshal(ruleBody("Each topic", fx.admin, "ENABLED", "key = "+issue.Key, components))
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
	comment := func(text string) int {
		t.Helper()
		var count int
		if err := fx.store.Pool.QueryRow(fx.ctx,
			`SELECT count(*) FROM comments WHERE issue_id=$1 AND body::text LIKE '%'||$2||'%'`, issue.ID, text).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	if comment("Checking docs on "+issue.Key) != 1 || comment("Checking api on "+issue.Key) != 1 {
		t.Fatal("the branch did not run once for each label")
	}
	// The branch named the item; outside it the rule's own value is back.
	if comment("Done with none") != 1 {
		t.Fatal("an item named inside the branch outlived it")
	}
	// The work item did not change inside the branch: it is a branch over
	// values, so {{issue.key}} is still the work item the rule ran for.
	if comment("Checking docs on "+issue.Key) != 1 {
		t.Fatal("the branch changed which work item its actions ran on")
	}
}
