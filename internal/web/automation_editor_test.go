package web

import (
	"encoding/json"
	"testing"
)

func TestAutomationEditorKeepsWhatItCannotShow(t *testing.T) {
	for payload, want := range map[string]string{
		`{"trigger":{"type":"jira.jql.scheduled"},"components":[{"component":"CONDITION","type":"jira.issue.condition"},{"component":"ACTION","type":"jira.issue.edit"},{"component":"ACTION","type":"jira.issue.comment"}]}`: "",
		`{"trigger":{"type":"jira.manual.trigger.issue.action"},"components":[{"component":"ACTION","type":"jira.issue.comment"}]}`:                                                                                           "its trigger",
		`{"trigger":{"type":"jira.issue.event.trigger:created"},"components":[{"component":"BRANCH","type":"jira.issue.related"}]}`:                                                                                           "its branches",
		`{"trigger":{"type":"jira.issue.event.trigger:created"},"components":[{"component":"CONDITION","type":"jira.jql.condition"}]}`:                                                                                        "its JQL conditions",
		`{"trigger":{"type":"jira.issue.field.changed"},"components":[{"component":"ACTION","type":"jira.issue.create"}]}`:                                                                                                    "some of its actions",
	} {
		if got := automationEditorUnsupported(json.RawMessage(payload)); got != want {
			t.Fatalf("%s = %q, want %q", payload, got, want)
		}
	}
	if actions := parseAutomationActions(json.RawMessage(`{"components":[{"component":"BRANCH","type":"jira.issue.related"},{"component":"ACTION","type":"jira.issue.comment","value":{"comment":"hi"}}]}`)); len(actions) != 1 || actions[0].Value != "hi" {
		t.Fatalf("editor actions = %+v", actions)
	}
}
