package render

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/models"
)

func TestEditDialogRendersConfiguredFieldOrder(t *testing.T) {
	view := models.EditDialogView{
		Issue: models.Issue{Key: "ORD-1", Summary: "Retained summary", Fields: map[string]json.RawMessage{
			"environment": json.RawMessage(`{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"Retained environment"}]}]}`),
		}},
		EditFieldOrder:   []string{"labels", "customfield_17001", "summary", "components", "environment", "description", "priority", "labels"},
		EditFields:       map[string]bool{"labels": true, "description": true},
		CustomFields:     []models.CustomFieldView{{ID: "customfield_17001", Name: "Ordered custom text", Type: "text", Value: "Retained custom text"}},
		ChoiceFields:     []models.IssueChoiceFieldView{{Field: models.CreateFieldMeta{ID: "components", Name: "Components", Options: []models.CreateFieldOption{{ID: "cmp_one", Name: "One"}}}, Value: "cmp_one"}},
		EnvironmentField: &models.CreateFieldMeta{ID: "environment", Name: "Environment"},
	}
	var output bytes.Buffer
	if err := Fragment(&output, "edit_dialog", view); err != nil {
		t.Fatal(err)
	}
	html := output.String()
	previous := -1
	for _, id := range []string{"edit-labels", "field-customfield_17001", "edit-summary", "edit-components", "edit-environment", "edit-description"} {
		marker := `id="` + id + `"`
		position := strings.Index(html, marker)
		if position <= previous || strings.Count(html, marker) != 1 {
			t.Fatalf("field %s is missing, duplicated or out of configured order", id)
		}
		previous = position
	}
	for _, value := range []string{"Retained summary", "Retained custom text", "Retained environment", `value="cmp_one" selected`} {
		if !strings.Contains(html, value) {
			t.Errorf("ordered control lost its saved value %q", value)
		}
	}
	if strings.Contains(html, `id="edit-priority"`) {
		t.Fatal("ordering exposed a field hidden by its configuration")
	}
	view.EditFieldOrder = []string{}
	output.Reset()
	if err := Fragment(&output, "edit_dialog", view); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `id="edit-summary"`) || strings.Contains(output.String(), `id="edit-labels"`) {
		t.Fatal("empty screen must retain summary without optional fields")
	}
}

func TestEditDialogTabsHideUnusableGroupsAndRetainSummary(t *testing.T) {
	view := models.EditDialogView{
		Issue:          models.Issue{Key: "TAB-1", Summary: "Saved summary"},
		EditFieldOrder: []string{"labels", "summary", "description", "resolution"},
		EditFields:     map[string]bool{"labels": true, "description": true, "resolution": true},
		EditScreenTabs: []models.FieldTab{
			{Name: "Details", Fields: []string{"labels"}},
			{Name: "Text", Fields: []string{"summary", "description"}},
			{Name: "Restricted", Fields: []string{"resolution"}},
			{Name: "Empty"},
		},
	}
	var output bytes.Buffer
	if err := Fragment(&output, "edit_dialog", view); err != nil {
		t.Fatal(err)
	}
	html := output.String()
	for _, marker := range []string{`aria-controls="edit-tabpanel-0"`, `aria-controls="edit-tabpanel-1"`, `aria-labelledby="edit-tab-1" hidden`, `value="Saved summary"`} {
		if !strings.Contains(html, marker) {
			t.Errorf("tabbed dialog is missing %q", marker)
		}
	}
	for _, marker := range []string{">Restricted<", ">Empty<", `id="edit-resolution"`} {
		if strings.Contains(html, marker) {
			t.Errorf("tabbed dialog exposed an unusable group: %q", marker)
		}
	}
	view.EditFieldOrder = []string{"labels", "description"}
	view.EditScreenTabs[1].Fields = []string{"description"}
	output.Reset()
	if err := Fragment(&output, "edit_dialog", view); err != nil {
		t.Fatal(err)
	}
	html = output.String()
	if strings.Count(html, `id="edit-summary"`) != 1 || strings.Index(html, `id="edit-summary"`) > strings.Index(html, `id="edit-labels"`) {
		t.Fatal("summary absent from the screen must remain in the first usable tab")
	}
	view.EditScreenTabs = view.EditScreenTabs[:1]
	output.Reset()
	if err := Fragment(&output, "edit_dialog", view); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), `role="tablist"`) {
		t.Fatal("one usable tab should remain a plain ordered form")
	}
}
