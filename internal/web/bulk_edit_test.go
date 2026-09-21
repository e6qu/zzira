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

// The editor changes every field it was asked to change, in one task, and
// leaves the rest alone -- the boxes of an unticked field carry values the
// form always submits.
func TestBulkEditOperationsChangeEveryTickedField(t *testing.T) {
	operations, err := bulkEditOperations(bulkEditRequest(url.Values{
		"field":            {"assignee", "labels", "components"},
		"valueAssignee":    {"usr_ana"},
		"valuePriority":    {"pri_1"},
		"valueDueDate":     {"2026-01-01"},
		"valueLabels":      {" release , , urgent "},
		"valueComponents":  {"cmp_runtime", "cmp_api"},
		"componentAction":  {"add"},
		"valueFixVersions": {"ver_1"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(operations) != 3 {
		t.Fatalf("operations = %+v", operations)
	}
	// The command reads an account id, a label list and a component id list.
	want := []struct{ field, action, value string }{
		{"assignee", "SET", `"usr_ana"`},
		{"labels", "ADD", `["release","urgent"]`},
		{"components", "ADD", `["cmp_runtime","cmp_api"]`},
	}
	for index, expected := range want {
		got := operations[index]
		if got.FieldID != expected.field || got.Action != expected.action || string(got.Value) != expected.value {
			t.Fatalf("operation %d = %+v, value %s", index, got, got.Value)
		}
	}
}

// An empty box clears the field, the way the single-item edit clears one,
// except where Jira has no unset value to fall back to.
func TestBulkEditOperationsClearAndReject(t *testing.T) {
	for _, tc := range []struct {
		name  string
		form  url.Values
		field string
		value string
		err   string
	}{
		{name: "assignee set", form: url.Values{"field": {"assignee"}, "valueAssignee": {"usr_ana"}}, field: "assignee", value: `"usr_ana"`},
		{name: "assignee cleared", form: url.Values{"field": {"assignee"}, "valueAssignee": {""}}, field: "assignee", value: `""`},
		{name: "due date set", form: url.Values{"field": {"duedate"}, "valueDueDate": {"2026-03-04"}}, field: "duedate", value: `"2026-03-04"`},
		{name: "due date cleared", form: url.Values{"field": {"duedate"}, "valueDueDate": {""}}, field: "duedate", value: `""`},
		{name: "due date malformed", form: url.Values{"field": {"duedate"}, "valueDueDate": {"4 March"}}, err: "YYYY-MM-DD"},
		{name: "priority set", form: url.Values{"field": {"priority"}, "valuePriority": {"pri_2"}}, field: "priority", value: `"pri_2"`},
		{name: "priority missing", form: url.Values{"field": {"priority"}, "valuePriority": {""}}, err: "choose a priority"},
		{name: "labels missing", form: url.Values{"field": {"labels"}, "valueLabels": {" , "}}, err: "at least one label"},
		{name: "versions cleared", form: url.Values{"field": {"fixVersions"}, "versionAction": {"set"}}, field: "fixVersions", value: `[]`},
		{name: "nothing ticked", form: url.Values{"valueLabels": {"release"}}, err: "choose a field"},
		{name: "unknown field", form: url.Values{"field": {"nonesuch"}}, err: "choose a field"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			operations, err := bulkEditOperations(bulkEditRequest(tc.form))
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("err = %v, want %q", err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(operations) != 1 || operations[0].FieldID != tc.field {
				t.Fatalf("operations = %+v", operations)
			}
			if string(operations[0].Value) != tc.value {
				t.Fatalf("value = %s, want %s", operations[0].Value, tc.value)
			}
		})
	}
}
