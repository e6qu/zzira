package api3

import (
	"github.com/e6qu/zzira/internal/models"
	"testing"
)

func TestReporterCreateMetadataUsesAccountIDs(t *testing.T) {
	h := &Handler{}
	field := models.CreateFieldMeta{ID: "reporter", Name: "Reporter", Type: "user", Required: true, Default: `"usr_current"`, Options: []models.CreateFieldOption{{ID: "usr_current", Name: "Current user"}, {ID: "usr_colleague", Name: "Colleague"}}}
	if bulkEditableFieldType(field) != "reporter" {
		t.Fatal("Reporter bulk metadata needs its own wire type")
	}
	for _, bean := range []map[string]any{h.createFieldBean(field), h.legacyCreateFieldBean(field)} {
		value, ok := bean["defaultValue"].(map[string]any)
		if !ok || value["accountId"] != "usr_current" || bean["hasDefaultValue"] != true {
			t.Fatalf("reporter default=%+v", bean)
		}
		values := bean["allowedValues"].([]map[string]any)
		if len(values) != 2 || values[1]["accountId"] != "usr_colleague" || values[1]["displayName"] != "Colleague" {
			t.Fatalf("reporter options=%+v", values)
		}
		schema := bean["schema"].(map[string]any)
		if schema["type"] != "user" || schema["system"] != "reporter" {
			t.Fatalf("reporter schema=%+v", schema)
		}
	}
}
