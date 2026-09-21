package api3

import (
	"encoding/json"
	"testing"

	"github.com/e6qu/zzira/internal/models"
)

// TestParseRemainingBulkEditFamilies covers the Jira bulk edit field families
// beyond text, numbers, dates, users, labels, versions, components and
// priority: each parses to the value the issue update accepts.
func TestParseRemainingBulkEditFamilies(t *testing.T) {
	input := map[string]json.RawMessage{
		"cascadingSelectFields":                   json.RawMessage(`[{"fieldId":"customfield_10","parentOptionValue":{"optionId":1},"childOptionValue":{"optionId":2}}]`),
		"datePickerFields":                        json.RawMessage(`[{"fieldId":"customfield_11","date":{"formattedDate":"2026-09-20"}}]`),
		"multipleGroupPickerFields":               json.RawMessage(`[{"fieldId":"customfield_12","groups":[{"groupName":"site-admins"}]}]`),
		"singleGroupPickerFields":                 json.RawMessage(`[{"fieldId":"customfield_13","group":{"groupName":"developers"}}]`),
		"multipleSelectClearableUserPickerFields": json.RawMessage(`[{"fieldId":"customfield_14","users":[{"accountId":"usr_1"}]}]`),
		"multipleSelectFields":                    json.RawMessage(`[{"fieldId":"customfield_15","options":[{"optionId":3},{"optionId":"4"}]}]`),
		"singleVersionPickerFields":               json.RawMessage(`[{"fieldId":"customfield_16","version":{"versionId":"10001"}}]`),
		"urlFields":                               json.RawMessage(`[{"fieldId":"customfield_17","url":"https://example.test/runbook"}]`),
		"colorFields":                             json.RawMessage(`[{"fieldId":"customfield_18","color":{"name":"purple"}}]`),
		"originalEstimateField":                   json.RawMessage(`{"originalEstimateField":"2h"}`),
		"timeTrackingField":                       json.RawMessage(`{"timeRemaining":"30m"}`),
	}
	parsed, err := parseBulkEditOperations(input)
	if err != nil {
		t.Fatal(err)
	}
	if err = convertBulkEstimates(parsed, models.TimeTrackingConfiguration{}); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, item := range parsed {
		got[item.operation.FieldID] = item.fieldType + " " + string(item.operation.Value)
	}
	want := map[string]string{
		"customfield_10":       `cascadingSelect {"child":"2","parent":"1"}`,
		"customfield_11":       `datePicker "2026-09-20"`,
		"customfield_12":       `multiGroup [{"name":"site-admins"}]`,
		"customfield_13":       `singleGroup {"name":"developers"}`,
		"customfield_14":       `multiUser [{"accountId":"usr_1"}]`,
		"customfield_15":       `multiSelect [{"id":"3"},{"id":"4"}]`,
		"customfield_16":       `singleVersion {"id":"10001"}`,
		"customfield_17":       `url "https://example.test/runbook"`,
		"customfield_18":       `color "purple"`,
		"timeoriginalestimate": `originalEstimate 7200`,
		"timeestimate":         `timeTracking 1800`,
	}
	for field, value := range want {
		if got[field] != value {
			t.Errorf("%s = %q, want %q", field, got[field], value)
		}
	}

	// The type and the status are fields of the edit, named as Jira names
	// them, and each carries only its own id.
	for family, want := range map[string]string{"issueType": `issuetype "10001"`, "status": `status "10002"`} {
		body := `{"issueTypeId":"10001"}`
		if family == "status" {
			body = `{"statusId":"10002"}`
		}
		parsed, err := parseBulkEditOperations(map[string]json.RawMessage{family: json.RawMessage(body)})
		if err != nil || len(parsed) != 1 {
			t.Fatalf("%s = %v, %v", family, parsed, err)
		}
		if got := parsed[0].operation.FieldID + " " + string(parsed[0].operation.Value); got != want {
			t.Errorf("%s operation = %q, want %q", family, got, want)
		}
		if _, err := parseBulkEditOperations(map[string]json.RawMessage{family: json.RawMessage(`{"issueTypeId":"1","statusId":"1"}`)}); err == nil {
			t.Errorf("%s accepted the other field's id", family)
		}
	}
	if _, err := parseBulkEditOperations(map[string]json.RawMessage{"datePickerFields": json.RawMessage(`[{"fieldId":"customfield_11","date":{"formattedDate":"20/Sep/26"}}]`)}); err == nil {
		t.Error("an unformatted date was accepted")
	}

	// Time tracking lists as the two estimates, and the create metadata types
	// the new families edit are offered.
	sources := bulkEditableSources([]models.CreateFieldMeta{{ID: "timetracking", Type: "timetracking"}, {ID: "customfield_17", Type: "url"}})
	if len(sources) != 3 || bulkEditableFieldType(sources[0]) != "originalEstimate" || bulkEditableFieldType(sources[1]) != "timeTracking" || bulkEditableFieldType(sources[2]) != "url" {
		t.Fatalf("editable sources = %+v", sources)
	}
	for metaType, bulkType := range map[string]string{"option": "singleSelect", "options": "multiSelect", "option-with-child": "cascadingSelect", "users": "multiUser", "group": "singleGroup", "groups": "multiGroup", "version": "singleVersion", "date": "datePicker"} {
		if got := bulkEditableFieldType(models.CreateFieldMeta{Type: metaType}); got != bulkType {
			t.Errorf("bulk type of %s = %q, want %q", metaType, got, bulkType)
		}
	}
}

// TestBulkFieldCursorsBindTheirQuery covers bulk field page cursors belonging
// to the selection and search they were issued for.
func TestBulkFieldCursorsBindTheirQuery(t *testing.T) {
	issues := []*models.Issue{{ID: "iss_b"}, {ID: "iss_a"}}
	query := bulkPageQuery(issues, "sum")
	if reordered := bulkPageQuery([]*models.Issue{{ID: "iss_a"}, {ID: "iss_b"}}, "sum"); reordered != query {
		t.Fatal("the same selection in another order fingerprints differently")
	}
	cursor := encodeBulkFieldCursor(query, 50)
	if offset, err := decodeBulkFieldCursor(query, cursor); err != nil || offset != 50 {
		t.Fatalf("own cursor = %d %v", offset, err)
	}
	if _, err := decodeBulkFieldCursor(bulkPageQuery(issues, "other"), cursor); err == nil {
		t.Fatal("a cursor paged a different search")
	}
	if _, err := decodeBulkFieldCursor(bulkPageQuery(issues[:1], "sum"), cursor); err == nil {
		t.Fatal("a cursor paged a different selection")
	}
}
