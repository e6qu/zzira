package api3

import (
	"encoding/json"
	"testing"
)

func TestParseAndValidateBulkEditOperations(t *testing.T) {
	input := map[string]json.RawMessage{
		"singleLineTextFields":                  json.RawMessage(`[{"fieldId":"summary","text":"Changed"}]`),
		"clearableNumberFields":                 json.RawMessage(`[{"fieldId":"customfield_1","value":7}]`),
		"dateTimePickerFields":                  json.RawMessage(`[{"fieldId":"customfield_2","dateTime":{"formattedDateTime":"2026-09-09T12:00:00Z"}}]`),
		"richTextFields":                        json.RawMessage(`[{"fieldId":"description","richText":{"adfValue":{"type":"doc","version":1,"content":[]}}}]`),
		"singleSelectClearableUserPickerFields": json.RawMessage(`[{"fieldId":"assignee","user":{"accountId":"usr_1"}}]`),
		"labelsFields":                          json.RawMessage(`[{"fieldId":"labels","bulkEditMultiSelectFieldOption":"ADD","labels":[{"name":"ready"}]}]`),
	}
	parsed, err := parseBulkEditOperations(input)
	if err != nil {
		t.Fatal(err)
	}
	available := map[string]string{
		"summary": "singleLineText", "customfield_1": "number", "customfield_2": "dateTime",
		"description": "richText", "assignee": "assignee", "labels": "labels",
	}
	selected := []string{"summary", "customfield_1", "customfield_2", "description", "assignee", "labels"}
	operations, err := validateBulkEditActions(selected, parsed, available)
	if err != nil || len(operations) != len(selected) {
		t.Fatalf("operations=%#v err=%v", operations, err)
	}
	if _, err := validateBulkEditActions([]string{"summary"}, parsed, available); err == nil {
		t.Fatal("selectedActions mismatch accepted")
	}
	input["unknownFields"] = json.RawMessage(`[]`)
	if _, err := parseBulkEditOperations(input); err == nil {
		t.Fatal("unsupported Jira field family accepted")
	}
}

func TestParseBulkEditRejectsMalformedNestedValues(t *testing.T) {
	tests := []map[string]json.RawMessage{
		{"richTextFields": json.RawMessage(`[{"fieldId":"description","richText":{"adfValue":{"type":"doc","version":2}}}]`)},
		{"labelsFields": json.RawMessage(`[{"fieldId":"labels","bulkEditMultiSelectFieldOption":"ADD","labels":[{"name":""}]}]`)},
		{"singleSelectClearableUserPickerFields": json.RawMessage(`[{"fieldId":"assignee","user":{}}]`)},
		{"priority": json.RawMessage(`{"priorityId":"","extra":true}`)},
	}
	for _, input := range tests {
		if _, err := parseBulkEditOperations(input); err == nil {
			t.Fatalf("malformed input accepted: %s", input)
		}
	}
}
