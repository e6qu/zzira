package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/models"
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
	}), nil)
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
			operations, err := bulkEditOperations(bulkEditRequest(tc.form), nil)
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

// A custom field is set in the shapes the update command reads: an option by
// id, options by id, a list of strings, a number, or text. What a field is
// comes from the project, so a form naming a field the project does not share
// changes nothing.
func TestBulkEditOperationsSetCustomFields(t *testing.T) {
	fields := []bulkCustomField{
		{ID: "customfield_10100", Name: "Team", Kind: "singleSelect"},
		{ID: "customfield_10101", Name: "Platforms", Kind: "multiSelect"},
		{ID: "customfield_10102", Name: "Tags", Kind: "labels"},
		{ID: "customfield_10103", Name: "Score", Kind: "number"},
		{ID: "customfield_10104", Name: "Runbook", Kind: "url"},
		{ID: "customfield_10105", Name: "Reviewed on", Kind: "date"},
		{ID: "customfield_10106", Name: "Cutover at", Kind: "dateTime"},
	}
	operations, err := bulkEditOperations(bulkEditRequest(url.Values{
		"field":                         {"customfield_10100", "customfield_10101", "customfield_10102", "customfield_10103", "customfield_10104", "customfield_10105", "customfield_10106"},
		"valueCustom_customfield_10100": {"opt_platform"},
		"valueCustom_customfield_10101": {"opt_ios", "opt_android"},
		"valueCustom_customfield_10102": {" alpha , , beta "},
		"valueCustom_customfield_10103": {"12.5"},
		"valueCustom_customfield_10104": {"https://runbook.example/incident"},
		"valueCustom_customfield_10105": {"2026-03-04"},
		"valueCustom_customfield_10106": {"2026-03-04T09:30"},
	}), fields)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		`"opt_platform"`,
		`[{"id":"opt_ios"},{"id":"opt_android"}]`,
		`["alpha","beta"]`,
		`12.5`,
		`"https://runbook.example/incident"`,
		`"2026-03-04"`,
		`"2026-03-04T09:30:00Z"`,
	}
	if len(operations) != len(want) {
		t.Fatalf("operations = %+v", operations)
	}
	for index, expected := range want {
		if operations[index].Action != "SET" || string(operations[index].Value) != expected {
			t.Fatalf("operation %d = %+v, value %s, want %s", index, operations[index], operations[index].Value, expected)
		}
	}

	// An emptied box clears the field, as the single-item editor does.
	cleared, err := bulkEditOperations(bulkEditRequest(url.Values{
		"field":                         {"customfield_10100", "customfield_10103"},
		"valueCustom_customfield_10100": {""},
		"valueCustom_customfield_10103": {""},
	}), fields)
	if err != nil {
		t.Fatal(err)
	}
	if string(cleared[0].Value) != `""` || string(cleared[1].Value) != `null` {
		t.Fatalf("cleared = %s and %s", cleared[0].Value, cleared[1].Value)
	}

	// A number that is not one says so rather than being written as text.
	if _, err := bulkEditOperations(bulkEditRequest(url.Values{
		"field":                         {"customfield_10103"},
		"valueCustom_customfield_10103": {"soon"},
	}), fields); err == nil {
		t.Fatal("a number field took text")
	}

	// A field the project does not share is not editable through the form.
	if _, err := bulkEditOperations(bulkEditRequest(url.Values{
		"field":                         {"customfield_99999"},
		"valueCustom_customfield_99999": {"anything"},
	}), fields); err == nil {
		t.Fatal("a field outside the project was edited")
	}
}

// An Assets object field is set across a selection like any other: one object
// by id, or several when its context holds several. Jira reports such a field
// with no schema type of its own, so the editor knows it by its type key.
func TestBulkEditOperationsSetAssetFields(t *testing.T) {
	if kind := bulkCustomFieldKind(models.CreateFieldMeta{Type: "any", TypeKey: models.CustomFieldTypeKeys[models.CustomFieldAsset]}); kind != "asset" {
		t.Fatalf("an Assets object field is %q", kind)
	}
	if kind := bulkCustomFieldKind(models.CreateFieldMeta{Type: "any", TypeKey: "com.example.app__thing"}); kind != "" {
		t.Fatalf("an app's own field is %q, and the editor leaves it alone", kind)
	}
	fields := []bulkCustomField{
		{ID: "customfield_10200", Name: "Affected service", Kind: "asset"},
		{ID: "customfield_10201", Name: "Affected services", Kind: "multiAsset"},
	}
	if !fields[0].Chooses() || fields[0].MultiValued() {
		t.Fatal("a single Assets field is chosen from a list, one at a time")
	}
	if !fields[1].Chooses() || !fields[1].MultiValued() {
		t.Fatal("an Assets field that holds several is chosen from a list, several at a time")
	}
	operations, err := bulkEditOperations(bulkEditRequest(url.Values{
		"field":                         {"customfield_10200", "customfield_10201"},
		"valueCustom_customfield_10200": {"obj-1"},
		"valueCustom_customfield_10201": {"obj-2", "obj-3", "obj-2"},
	}), fields)
	if err != nil {
		t.Fatal(err)
	}
	if len(operations) != 2 || string(operations[0].Value) != `"obj-1"` || string(operations[1].Value) != `["obj-2","obj-3"]` {
		t.Fatalf("operations = %+v, %s and %s", operations, operations[0].Value, operations[1].Value)
	}
	cleared, err := bulkEditOperations(bulkEditRequest(url.Values{
		"field":                         {"customfield_10200", "customfield_10201"},
		"valueCustom_customfield_10200": {""},
		"valueCustom_customfield_10201": {""},
	}), fields)
	if err != nil {
		t.Fatal(err)
	}
	if string(cleared[0].Value) != `""` || string(cleared[1].Value) != `[]` {
		t.Fatalf("cleared = %s and %s", cleared[0].Value, cleared[1].Value)
	}
}
