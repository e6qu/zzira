package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func bulkEditRequest(form url.Values) *http.Request {
	r, _ := http.NewRequest(http.MethodPost, "/issues/ZZ/bulk/edit", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := r.ParseForm(); err != nil {
		panic(err)
	}
	return r
}

// The navigator's editor carries every field's box at once, so the operation
// must come from the box the chosen field names -- not from whichever box
// happens to hold something.
func TestBulkEditOperationReadsOnlyTheChosenField(t *testing.T) {
	form := url.Values{
		"field":         {"labels"},
		"valueAssignee": {"usr_other"},
		"valuePriority": {"pri_1"},
		"valueDueDate":  {"2026-01-01"},
		"valueLabels":   {" release , , urgent "},
	}
	operation, err := bulkEditOperation(bulkEditRequest(form))
	if err != nil {
		t.Fatal(err)
	}
	if operation.FieldID != "labels" || operation.Action != "ADD" {
		t.Fatalf("operation = %+v", operation)
	}
	if string(operation.Value) != `["release","urgent"]` {
		t.Fatalf("labels = %s", operation.Value)
	}
}

func TestBulkEditOperationRemovesLabels(t *testing.T) {
	operation, err := bulkEditOperation(bulkEditRequest(url.Values{
		"field": {"labels"}, "valueLabels": {"stale"}, "labelAction": {"remove"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if operation.Action != "REMOVE" || string(operation.Value) != `["stale"]` {
		t.Fatalf("operation = %+v, value = %s", operation, operation.Value)
	}
}

// An empty box clears the field, the way the single-item edit clears one,
// except where Jira has no unset value to fall back to.
func TestBulkEditOperationClearsAndRejects(t *testing.T) {
	for _, tc := range []struct {
		name  string
		form  url.Values
		value string
		err   string
	}{
		{name: "assignee set", form: url.Values{"field": {"assignee"}, "valueAssignee": {"usr_ana"}}, value: `{"accountId":"usr_ana"}`},
		{name: "assignee cleared", form: url.Values{"field": {"assignee"}, "valueAssignee": {""}}, value: "null"},
		{name: "due date set", form: url.Values{"field": {"duedate"}, "valueDueDate": {"2026-03-04"}}, value: `"2026-03-04"`},
		{name: "due date cleared", form: url.Values{"field": {"duedate"}, "valueDueDate": {""}}, value: "null"},
		{name: "due date malformed", form: url.Values{"field": {"duedate"}, "valueDueDate": {"4 March"}}, err: "YYYY-MM-DD"},
		{name: "priority set", form: url.Values{"field": {"priority"}, "valuePriority": {"pri_2"}}, value: `{"id":"pri_2"}`},
		{name: "priority missing", form: url.Values{"field": {"priority"}, "valuePriority": {""}}, err: "choose a priority"},
		{name: "labels missing", form: url.Values{"field": {"labels"}, "valueLabels": {" , "}}, err: "at least one label"},
		{name: "no field", form: url.Values{"valueLabels": {"release"}}, err: "choose a field"},
		{name: "unknown field", form: url.Values{"field": {"summary"}, "valueLabels": {"release"}}, err: "choose a field"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			operation, err := bulkEditOperation(bulkEditRequest(tc.form))
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("err = %v, want %q", err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if string(operation.Value) != tc.value {
				t.Fatalf("value = %s, want %s", operation.Value, tc.value)
			}
			if operation.Action != "SET" {
				t.Fatalf("action = %q", operation.Action)
			}
		})
	}
}
