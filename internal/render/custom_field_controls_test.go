package render

import (
	"bytes"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/models"
)

func TestEditAndInlineCustomControlsHaveClearMarkersAndScopedHelp(t *testing.T) {
	field := models.CustomFieldView{ID: "customfield_17001", Name: "Platforms", Type: "multiselect", Value: "one,two", Description: "Choose platforms", Options: []models.CreateFieldOption{{ID: "one", Name: "One"}, {ID: "two", Name: "Two"}}}
	edit := models.EditDialogView{Issue: models.Issue{Key: "CF-1", Summary: "Platforms"}, CustomFields: []models.CustomFieldView{field}, EditFieldOrder: []string{field.ID}}
	view := fixtureIssue()
	view.CanEdit = true
	view.CustomFields = []models.CustomFieldView{field}
	for _, test := range []struct {
		fragment, prefix, name string
		data                   any
	}{
		{"edit_dialog", "field-", field.ID, edit},
		{"issue_view", "detail-field-", "value", view},
	} {
		var output bytes.Buffer
		if err := Fragment(&output, test.fragment, test.data); err != nil {
			t.Fatal(err)
		}
		html := output.String()
		for _, marker := range []string{
			`type="hidden" name="` + test.name + `" value=""`,
			`aria-describedby="` + test.prefix + `help-` + field.ID + `"`,
			`id="` + test.prefix + `help-` + field.ID + `"`,
			`value="one" selected`, `value="two" selected`,
		} {
			if !strings.Contains(html, marker) {
				t.Errorf("%s missing %s", test.fragment, marker)
			}
		}
	}
}
