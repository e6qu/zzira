package web

import (
	"encoding/json"
	"testing"
)

func TestAutomationEditorKeepsWhatItCannotShow(t *testing.T) {
	for payload, want := range map[string]string{
		`{"trigger":{"type":"jira.jql.scheduled"},"components":[{"component":"CONDITION","type":"jira.issue.condition"},{"component":"ACTION","type":"jira.issue.edit"},{"component":"ACTION","type":"jira.issue.comment"}]}`:                                                                                  "",
		`{"trigger":{"type":"jira.issue.event.trigger:created"},"components":[{"component":"CONDITION","type":"jira.jql.condition"},{"component":"ACTION","type":"jira.issue.comment"},{"component":"BRANCH","type":"jira.issue.related","children":[{"component":"ACTION","type":"jira.issue.add-label"}]}]}`: "",
		`{"trigger":{"type":"jira.manual.trigger.issue.action"},"components":[{"component":"ACTION","type":"jira.issue.comment"}]}`:                                                                                                                                                                            "its trigger",
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
}
