package web

import (
	"fmt"
	"testing"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

func TestEncodeServicePortalField(t *testing.T) {
	options := []store.ServiceRequestFieldOption{{ID: "1", Value: "Web"}, {ID: "2", Value: "Mobile", Children: []store.ServiceRequestFieldOption{{ID: "3", Value: "iOS"}}}}
	member := func(email string) (string, error) {
		if email == "ana@example.test" {
			return "usr_ana", nil
		}
		return "", fmt.Errorf("No active member of this site uses %s.", email)
	}
	for _, check := range []struct {
		kind          string
		submitted     []string
		child         string
		want, problem string
	}{
		{models.CustomFieldSelect, []string{"2"}, "", `"2"`, ""},
		{models.CustomFieldSelect, []string{"9"}, "", "", "Choose one of the options for Platform."},
		{models.CustomFieldMultiSelect, []string{"1", "2", "1"}, "", `["1","2"]`, ""},
		{models.CustomFieldMultiSelect, []string{"1", "7"}, "", "", "Choose from the options for Platform."},
		{models.CustomFieldCascadingSelect, []string{"2"}, "3", `{"child":"3","parent":"2"}`, ""},
		{models.CustomFieldCascadingSelect, []string{"2"}, "", `{"parent":"2"}`, ""},
		{models.CustomFieldCascadingSelect, []string{"1"}, "3", "", "Choose a Platform detail that belongs to Web."},
		{models.CustomFieldLabels, []string{"urgent, customer  facing"}, "", `["urgent","customer","facing"]`, ""},
		{models.CustomFieldDate, []string{"2026-09-20"}, "", `"2026-09-20"`, ""},
		{models.CustomFieldDate, []string{"soon"}, "", "", "Platform must be a date."},
		{models.CustomFieldUser, []string{"ana@example.test"}, "", `"usr_ana"`, ""},
		{models.CustomFieldUser, []string{"ana@example.test, bob@example.test"}, "", "", "Enter one email address for Platform."},
		{models.CustomFieldMultiUser, []string{"ana@example.test,ana@example.test"}, "", `["usr_ana"]`, ""},
		{models.CustomFieldMultiUser, []string{"ghost@example.test"}, "", "", "No active member of this site uses ghost@example.test."},
		{models.CustomFieldNumber, []string{"42.5"}, "", `42.5`, ""},
		{models.CustomFieldURL, []string{"https://status.example.test"}, "", `"https://status.example.test"`, ""},
	} {
		field := models.ServiceRequestTypeField{ID: "customfield_1", Name: "Platform", Type: check.kind}
		raw, present, err := encodeServicePortalField(field, check.submitted, check.child, options, member)
		problem := ""
		if err != nil {
			problem = err.Error()
		}
		if !present || string(raw) != check.want || problem != check.problem {
			t.Fatalf("%s %v %q = %s, %v, %q; want %s, %q", check.kind, check.submitted, check.child, raw, present, problem, check.want, check.problem)
		}
	}
	if _, present, err := encodeServicePortalField(models.ServiceRequestTypeField{Type: models.CustomFieldMultiSelect}, []string{" ", ""}, "", options, member); present || err != nil {
		t.Fatalf("a blank answer was present: %v, %v", present, err)
	}
	catalog := store.CustomFieldValueCatalog{Options: map[string]models.CustomFieldOption{"2": {ID: "2", Value: "Mobile"}, "3": {ID: "3", Value: "iOS"}}, Users: map[string]*models.User{"usr_ana": {ID: "usr_ana", DisplayName: "Ana"}}}
	for fieldType, want := range map[string]string{models.CustomFieldCascadingSelect: "Mobile - iOS", models.CustomFieldMultiUser: "Ana", models.CustomFieldLabels: "a, b", models.CustomFieldNumber: "42.5"} {
		value := map[string]any{models.CustomFieldCascadingSelect: map[string]any{"parent": "2", "child": "3"}, models.CustomFieldMultiUser: []any{"usr_ana"}, models.CustomFieldLabels: []any{"a", "b"}, models.CustomFieldNumber: 42.5}[fieldType]
		if got := serviceFieldDisplay(fieldType, value, catalog); got != want {
			t.Fatalf("%s display = %q, want %q", fieldType, got, want)
		}
	}
}
