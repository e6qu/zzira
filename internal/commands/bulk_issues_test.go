package commands

import (
	"encoding/json"
	"testing"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

func TestBulkIssueUpdateNormalizesSupportedOperations(t *testing.T) {
	issue := &models.Issue{
		ID: "iss_1", WorkspaceID: "ws_1", Labels: []string{"existing"},
		Fields: map[string]json.RawMessage{
			"components": json.RawMessage(`[{"id":"cmp_1","name":"One"},{"id":"cmp_2","name":"Two"}]`),
		},
	}
	operations := []store.BulkIssueEditOperation{
		{FieldID: "summary", Action: "SET", Value: json.RawMessage(`"Changed"`)},
		{FieldID: "description", Action: "SET", Value: json.RawMessage(`{"type":"doc","version":1,"content":[]}`)},
		{FieldID: "priority", Action: "SET", Value: json.RawMessage(`"2"`)},
		{FieldID: "assignee", Action: "SET", Value: json.RawMessage(`"usr_2"`)},
		{FieldID: "security", Action: "SET", Value: json.RawMessage(`"private"`)},
		{FieldID: "labels", Action: "ADD", Value: json.RawMessage(`["existing","new"]`)},
		{FieldID: "components", Action: "REMOVE", Value: json.RawMessage(`["cmp_1"]`)},
		{FieldID: "fixVersions", Action: "ADD", Value: json.RawMessage(`["ver_1","ver_2"]`)},
		{FieldID: "customfield_20000", Action: "SET", Value: json.RawMessage(`7`)},
	}
	update, err := bulkIssueUpdate(issue, "usr_actor", operations)
	if err != nil {
		t.Fatal(err)
	}
	if update.ActorID != "usr_actor" || update.WorkspaceID != "ws_1" || update.IssueIDOrKey != "iss_1" {
		t.Fatalf("identity = %+v", update)
	}
	if update.Summary == nil || *update.Summary != "Changed" || update.PriorityID == nil || *update.PriorityID != "2" || update.AssigneeID == nil || *update.AssigneeID != "usr_2" {
		t.Fatalf("scalar values = %+v", update)
	}
	if update.Labels == nil || len(*update.Labels) != 2 || (*update.Labels)[1] != "new" {
		t.Fatalf("labels = %#v", update.Labels)
	}
	if got := string(update.Fields["components"]); got != `[{"id":"cmp_2"}]` {
		t.Fatalf("components = %s", got)
	}
	if got := string(update.Fields["customfield_20000"]); got != "7" {
		t.Fatalf("custom field = %s", got)
	}
	if got := update.VersionOperations["fixVersions"]; len(got) != 2 || string(got[0]["add"]) != `{"id":"ver_1"}` || string(got[1]["add"]) != `{"id":"ver_2"}` {
		t.Fatalf("versions = %#v", got)
	}
}

func TestApplyBulkStringOperation(t *testing.T) {
	tests := []struct {
		action string
		want   string
	}{
		{"ADD", `["one","two","three"]`},
		{"REMOVE", `["one"]`},
		{"REPLACE", `["two","three"]`},
		{"REMOVE_ALL", `[]`},
	}
	for _, test := range tests {
		got, err := applyBulkStringOperation([]string{"one", "two"}, []string{"two", "three"}, test.action)
		if err != nil {
			t.Fatalf("%s: %v", test.action, err)
		}
		encoded, _ := json.Marshal(got)
		if string(encoded) != test.want {
			t.Fatalf("%s = %s want %s", test.action, encoded, test.want)
		}
	}
	if _, err := applyBulkStringOperation(nil, nil, "UNKNOWN"); err == nil {
		t.Fatal("unknown action accepted")
	}
}
