package web

import (
	"encoding/json"
	"testing"
)

func TestAutomationEditorKeepsWhatItCannotShow(t *testing.T) {
	for payload, want := range map[string]string{
		`{"trigger":{"type":"jira.jql.scheduled"},"components":[{"component":"CONDITION","type":"jira.issue.condition"},{"component":"ACTION","type":"jira.issue.edit"},{"component":"ACTION","type":"jira.issue.comment"}]}`:                                                                                  "",
		`{"trigger":{"type":"jira.issue.event.trigger:created"},"components":[{"component":"CONDITION","type":"jira.jql.condition"},{"component":"ACTION","type":"jira.issue.comment"},{"component":"BRANCH","type":"jira.issue.related","children":[{"component":"ACTION","type":"jira.issue.add-label"}]}]}`: "",
		`{"trigger":{"type":"jira.manual.trigger.issue.action"},"components":[{"component":"ACTION","type":"jira.issue.comment"}]}`:                                                                                                                                                                            "",
		`{"trigger":{"type":"jira.some.trigger.nobody.has"},"components":[{"component":"ACTION","type":"jira.issue.comment"}]}`:                                                                                                                                                                                "its trigger",
		`{"trigger":{"type":"jira.issue.event.trigger:created"},"components":[{"component":"BRANCH","type":"jira.issue.related","children":[{"component":"CONDITION","type":"jira.jql.condition"},{"component":"ACTION","type":"jira.issue.add-label"}]}]}`:                                                    "",
		`{"trigger":{"type":"jira.issue.event.trigger:created"},"components":[{"component":"BRANCH","type":"jira.issue.related","children":[{"component":"ACTION","type":"jira.issue.add-label"},{"component":"CONDITION","type":"jira.jql.condition"}]}]}`:                                                    "its branches",
		`{"trigger":{"type":"jira.issue.event.trigger:created"},"components":[{"component":"BRANCH","type":"jira.issue.related","children":[{"component":"CONDITION","type":"jira.issue.unknown"},{"component":"ACTION","type":"jira.issue.add-label"}]}]}`:                                                    "its branches",
		`{"trigger":{"type":"jira.issue.event.trigger:created"},"components":[{"component":"BRANCH","type":"jira.issue.related","children":[]},{"component":"ACTION","type":"jira.issue.comment"}]}`:                                                                                                           "its branches",
		`{"trigger":{"type":"jira.issue.event.trigger:created"},"components":[{"component":"CONDITION","type":"jira.issue.unknown"}]}`:                                                                                                                                                                         "some of its conditions",
		`{"trigger":{"type":"jira.issue.field.changed"},"components":[{"component":"ACTION","type":"jira.issue.unknown"}]}`:                                                                                                                                                                                    "some of its actions",
		`{"trigger":{"type":"jira.issue.field.changed"},"components":[{"component":"ACTION","type":"jira.issue.create","value":{"issueTypeId":"it_task","summary":"x"}}]}`:                                                                                                                                     "",
	} {
		if got := automationEditorUnsupported(json.RawMessage(payload)); got != want {
			t.Fatalf("%s = %q, want %q", payload, got, want)
		}
	}
	// A manual rule's questions come back as the editor's rows, with a blank
	// one at the end to add another.
	manual := parseAutomationTrigger(json.RawMessage(`{"trigger":{"type":"jira.manual.trigger.issue.action","value":{"inputPrompts":[{"displayName":"Why?","variableName":"reason","inputType":"TEXT","required":true}]}}}`))
	if len(manual.Prompts) != 2 || manual.Prompts[0].VariableName != "reason" || !manual.Prompts[0].Required || manual.Prompts[1].DisplayName != "" {
		t.Fatalf("manual prompts = %+v", manual.Prompts)
	}
	// A question needs a name and a variable the rule can read an answer by.
	if _, err := automationFormPrompts([]string{"Why?"}, []string{"a reason"}, []string{"TEXT"}, []string{"required"}); err == nil {
		t.Fatal("a variable with a space was accepted")
	}
	if _, err := automationFormPrompts([]string{"Why?", "Again"}, []string{"reason", "reason"}, []string{"TEXT", "TEXT"}, []string{"required", "optional"}); err == nil {
		t.Fatal("two questions shared a variable")
	}
	prompts, err := automationFormPrompts([]string{"Why?", ""}, []string{"reason", ""}, []string{"TEXT", "TEXT"}, []string{"required", "optional"})
	if err != nil || len(prompts) != 1 || prompts[0]["variableName"] != "reason" || prompts[0]["required"] != true {
		t.Fatalf("prompts = %+v, %v", prompts, err)
	}

	branch := parseAutomationBranch(json.RawMessage(`{"components":[{"component":"BRANCH","type":"jira.issue.related","value":{"relatedType":"linked","linkTypes":["blocks","relates to"]},"children":[{"component":"ACTION","type":"jira.issue.edit","value":{"field":"summary","value":"x"}}]}]}`))
	if branch.RelatedType != "linked" || branch.LinkTypes != "blocks, relates to" || len(branch.Actions) != 1 || branch.Actions[0].Type != "jira.issue.edit:summary" {
		t.Fatalf("branch = %+v", branch)
	}
	conditioned := parseAutomationBranch(json.RawMessage(`{"components":[{"component":"BRANCH","type":"jira.issue.related","value":{"relatedType":"sub-tasks"},"children":[{"component":"CONDITION","type":"jira.jql.condition","value":{"jql":"status != Done"}},{"component":"CONDITION","type":"jira.issue.condition","value":{"field":"labels","operator":"CONTAINS","value":"urgent"}},{"component":"ACTION","type":"jira.issue.add-label","value":{"label":"x"}}]}]}`))
	if len(conditioned.Conditions) != 2 || conditioned.Conditions[0] != (automationConditionView{Field: "jql", Value: "status != Done"}) || conditioned.Conditions[1].Field != "labels" || len(conditioned.Actions) != 1 {
		t.Fatalf("branch conditions = %+v", conditioned)
	}
	if conditions := parseAutomationConditions(json.RawMessage(`{"components":[{"component":"CONDITION","type":"jira.jql.condition","value":{"jql":"priority = High"}}]}`)); len(conditions) != 1 || conditions[0].Field != "jql" || conditions[0].Value != "priority = High" {
		t.Fatalf("conditions = %+v", conditions)
	}
	if actions := parseAutomationActions(json.RawMessage(`{"components":[{"component":"BRANCH","type":"jira.issue.related"},{"component":"ACTION","type":"jira.issue.comment","value":{"comment":"hi"}}]}`)); len(actions) != 1 || actions[0].Value != "hi" {
		t.Fatalf("editor actions = %+v", actions)
	}

	// A branch over a query reads its query back, and a create variable
	// action reads back the name it gave and what it holds.
	queried := parseAutomationBranch(json.RawMessage(`{"components":[{"component":"BRANCH","type":"jira.issue.related","value":{"relatedType":"jql","jql":"labels = late"},"children":[{"component":"ACTION","type":"jira.create.variable","value":{"variableName":"note","variableValue":"{{issue.key}}"}}]}]}`))
	if queried.RelatedType != "jql" || queried.JQL != "labels = late" || len(queried.Actions) != 1 {
		t.Fatalf("queried branch = %+v", queried)
	}
	if action := queried.Actions[0]; action.Type != "jira.create.variable" || action.Variable != "note" || action.Value != "{{issue.key}}" {
		t.Fatalf("variable action = %+v", action)
	}
	// The editor writes what the runner reads: the name in its own column,
	// the value in the row's value, and a variable holding nothing is a
	// variable all the same.
	components, err := automationFormActions(automationActionRows{
		Types:     []string{"jira.create.variable", "jira.issue.comment"},
		Values:    []string{"", "Noted {{note}}"},
		Variables: []string{"note", ""},
	})
	if err != nil || len(components) != 2 {
		t.Fatalf("form actions = %+v, %v", components, err)
	}
	if value := components[0]["value"].(map[string]string); value["variableName"] != "note" || value["variableValue"] != "" {
		t.Fatalf("variable action = %+v", value)
	}
}
