package render

import (
	"bytes"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/models"
)

func TestTransitionTabsKeepValuesAndScopeControlsToTheirAction(t *testing.T) {
	view := fixtureIssue()
	transition := models.WorkflowTransition{
		ID: "31", Name: "Finish", ScreenFields: []string{"labels", "summary", "assignee", "priority", "customfield_17001", "customfield_17002"},
		ScreenTabs: []models.FieldTab{
			{Name: "Details", Fields: []string{"labels"}},
			{Name: "Text", Fields: []string{"summary", "assignee", "priority", "customfield_17001", "customfield_17002"}},
			{Name: "Hidden", Fields: []string{"environment"}},
			{Name: "Empty"},
		},
		ScreenCustomFields: []models.CustomFieldView{
			{ID: "customfield_17001", Name: "Decision", Type: "text", Value: "Saved decision", Description: "Why this decision"},
			{ID: "customfield_17002", Name: "Choices", Type: "multiselect", Value: "one,two", Options: []models.CreateFieldOption{{ID: "one", Name: "One"}, {ID: "two", Name: "Two"}}},
		},
	}
	other := transition
	other.ID, other.Name = "41", "Resolve"
	view.Transitions = []models.WorkflowTransition{transition, other}
	var output bytes.Buffer
	if err := Fragment(&output, "issue_view", view); err != nil {
		t.Fatal(err)
	}
	html := output.String()
	for _, id := range []string{"31", "41"} {
		prefix := "transition-" + id + "-" + view.Issue.ID + "-"
		for _, field := range transition.ScreenFields {
			if strings.Count(html, `id="`+prefix+field+`"`) != 1 || !strings.Contains(html, `for="`+prefix+field+`"`) {
				t.Errorf("transition %s has a missing, duplicated or mislabelled %s control", id, field)
			}
		}
		for _, marker := range []string{
			`aria-controls="` + prefix + `tabpanel-0"`,
			`aria-labelledby="` + prefix + `tab-1" hidden`,
			`aria-describedby="` + prefix + `help-customfield_17001"`,
			`id="` + prefix + `help-customfield_17001"`,
		} {
			if !strings.Contains(html, marker) {
				t.Errorf("transition %s is missing %q", id, marker)
			}
		}
		for field, value := range map[string]string{"assignee": "usr_1", "priority": "pr_medium"} {
			start := strings.Index(html, `<select id="`+prefix+field+`"`)
			if start < 0 {
				t.Fatalf("transition %s is missing its %s select", id, field)
			}
			end := strings.Index(html[start:], "</select>")
			if end < 0 || !strings.Contains(html[start:start+end], `value="`+value+`" selected`) {
				t.Errorf("transition %s lost its saved %s selection", id, field)
			}
		}
	}
	for _, marker := range []string{`value="usr_1" selected`, `value="pr_medium" selected`, `value="Saved decision"`} {
		if strings.Count(html, marker) < 2 {
			t.Errorf("transition controls lost their saved value: %s", marker)
		}
	}
	if strings.Contains(html, ">Hidden<") || strings.Contains(html, ">Empty<") {
		t.Fatal("transition tabs exposed groups with no offered fields")
	}
	if strings.Count(html, `<input type="hidden" name="field_customfield_17002" value="">`) != 2 || strings.Count(html, `value="one" selected`) != 2 || strings.Count(html, `value="two" selected`) != 2 {
		t.Fatal("transition multi-select must retain every selection and submit an explicit clear marker")
	}
	view.Transitions[0].ScreenFields = []string{"labels"}
	view.Transitions = view.Transitions[:1]
	output.Reset()
	if err := Fragment(&output, "issue_view", view); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), `role="tablist"`) || !strings.Contains(output.String(), `id="transition-31-iss_abc123-labels"`) {
		t.Fatal("one usable transition tab should remain a plain form")
	}
}
