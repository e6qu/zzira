package web

import (
	"testing"

	"github.com/e6qu/zzira/internal/models"
)

func TestCustomFieldFormValuesRetainSelectionsAndExplicitClears(t *testing.T) {
	for _, test := range []struct {
		name, kind string
		values     []string
		want       string
		invalid    bool
	}{
		{"marker and selections", models.CustomFieldMultiSelect, []string{"", "one", "two"}, `["one","two"]`, false},
		{"selections without marker", models.CustomFieldMultiSelect, []string{"one", "two"}, `["one","two"]`, false},
		{"clear selections", models.CustomFieldMultiSelect, []string{""}, `null`, false},
		{"clear number", models.CustomFieldNumber, []string{""}, `null`, false},
		{"typed number", models.CustomFieldNumber, []string{"", "12.5"}, `12.5`, false},
		{"invalid number", models.CustomFieldNumber, []string{"", "wrong"}, "", true},
		{"text containing commas", models.CustomFieldText, []string{"keep, this text"}, `"keep, this text"`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := encodeWebCustomFieldValues(test.kind, test.values)
			if test.invalid {
				if err == nil {
					t.Fatal("invalid form value was accepted")
				}
				return
			}
			if err != nil || string(got) != test.want {
				t.Fatalf("got %s, %v; want %s", got, err, test.want)
			}
		})
	}
}
