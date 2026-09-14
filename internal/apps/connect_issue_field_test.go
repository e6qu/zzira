package apps

import (
	"testing"

	"github.com/e6qu/zzira/internal/models"
)

func TestConnectIssueFieldTypes(t *testing.T) {
	for connectType, want := range map[string]string{
		"string": models.CustomFieldText, "text": models.CustomFieldText, "rich_text": models.CustomFieldText,
		"number": models.CustomFieldNumber, "date": models.CustomFieldDatetime, "datetime": models.CustomFieldDatetime,
		"single_select": models.CustomFieldSelect, "multi_select": models.CustomFieldMultiSelect,
	} {
		field := connectIssueFieldWire{Key: "platforms", Type: connectType}
		field.Name.Value = "Platforms"
		translated, err := translateConnectIssueField(field, false)
		if err != nil || translated.Type != want {
			t.Fatalf("%s: type %q err %v, want %q", connectType, translated.Type, err, want)
		}
	}
	unsupported := connectIssueFieldWire{Key: "cascade", Type: "cascading_select"}
	unsupported.Name.Value = "Cascade"
	if _, err := translateConnectIssueField(unsupported, false); err == nil {
		t.Fatal("an unsupported issue field type was accepted")
	}
}
