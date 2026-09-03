package web

import (
	"bytes"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/render"
)

func TestCompileNavigatorSearchAlwaysScopesProject(t *testing.T) {
	params := navigatorParams{Mode: "advanced", JQL: `project = OTHER OR status = Done`, Sort: "updated", Direction: "desc"}
	compiled, err := compileNavigatorSearch("zz", "usr_me", params)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(compiled.Where, "pr.key") {
		t.Fatalf("project predicate missing from %q", compiled.Where)
	}
	if len(compiled.Args) != 3 || compiled.Args[0] != "ZZ" || compiled.Args[1] != "OTHER" || compiled.Args[2] != "Done" {
		t.Fatalf("compiled args = %#v", compiled.Args)
	}
}

func TestEncodeWebCustomFieldPreservesTypes(t *testing.T) {
	number, err := encodeWebCustomField(models.CustomFieldNumber, "42.5")
	if err != nil || string(number) != "42.5" {
		t.Fatalf("number = %s, %v", number, err)
	}
	text, err := encodeWebCustomField(models.CustomFieldText, "42")
	if err != nil || string(text) != `"42"` {
		t.Fatalf("text = %s, %v", text, err)
	}
	if _, err := encodeWebCustomField(models.CustomFieldNumber, "NaN"); err == nil {
		t.Fatal("NaN accepted as JSON number")
	}
	if !json.Valid(number) || !json.Valid(text) {
		t.Fatal("encoded values must be valid JSON")
	}
}

func TestCompileNavigatorBasicFiltersAndSort(t *testing.T) {
	params := navigatorParams{
		Mode: "basic", Text: "release gate", Status: "In Progress", Assignee: "currentUser()",
		Sort: "assignee", Direction: "asc",
	}
	compiled, err := compileNavigatorSearch("ZZ", "usr_me", params)
	if err != nil {
		t.Fatal(err)
	}
	if compiled.OrderSQL != "a.display_name ASC" {
		t.Fatalf("order = %q", compiled.OrderSQL)
	}
	want := []any{"ZZ", "%release gate%", "%release gate%", "In Progress", "usr_me"}
	if len(compiled.Args) != len(want) {
		t.Fatalf("args = %#v", compiled.Args)
	}
	for i := range want {
		if compiled.Args[i] != want[i] {
			t.Fatalf("arg %d = %#v, want %#v", i, compiled.Args[i], want[i])
		}
	}
}

func TestParseNavigatorParamsRejectsUntrustedSortAndBoundsPage(t *testing.T) {
	params := parseNavigatorParams(url.Values{
		"mode": {"unknown"}, "sort": {"i.summary; DROP TABLE issues"}, "direction": {"sideways"}, "page": {"999999999"},
	})
	if params.Mode != "basic" || params.Sort != "updated" || params.Direction != "desc" || params.Page != 1_000_000 {
		t.Fatalf("unexpected params: %#v", params)
	}
}

func TestNavigatorChipsRemoveOnlyTheirOwnFilter(t *testing.T) {
	params := navigatorParams{Mode: "basic", Text: "release", Status: "To Do", Assignee: "usr_ana", Sort: "updated", Direction: "desc"}
	chips := navigatorChips("ZZ", params, []*models.User{{ID: "usr_ana", DisplayName: "Ana"}})
	if len(chips) != 3 || chips[2].Label != "Assignee: Ana" {
		t.Fatalf("chips = %#v", chips)
	}
	removed, err := url.Parse(chips[1].URL)
	if err != nil {
		t.Fatal(err)
	}
	query := removed.Query()
	if query.Get("status") != "" || query.Get("text") != "release" || query.Get("assignee") != "usr_ana" {
		t.Fatalf("status removal URL = %q", chips[1].URL)
	}
}

func TestProjectNavigatorRendersAccessibleWorkbench(t *testing.T) {
	issue := &models.Issue{
		ID: "iss_1", Key: "LONG-42", Summary: "Keyboard-ready results",
		IssueType: models.IssueType{Name: "Task"}, Status: models.Status{Name: "To Do", Category: "new"}, UpdatedAt: "2026-09-03T10:00:00Z",
	}
	data := projectIssuesData{
		Project: &models.Project{Key: "LONG", Name: "Long project"}, Issues: []*models.Issue{issue}, Selected: issue,
		Mode: "basic", Sort: "updated", Direction: "desc", Total: 1, ResultStart: 1, ResultEnd: 1, Page: 1, PageCount: 1,
		BasicURL: "/issues/LONG?mode=basic", AdvancedURL: "/issues/LONG?mode=advanced", SortURLs: map[string]string{
			"key": "?sort=key", "summary": "?sort=summary", "status": "?sort=status", "priority": "?sort=priority", "assignee": "?sort=assignee", "updated": "?sort=updated",
		},
	}
	var output bytes.Buffer
	if err := render.Page(&output, "page_project", pageData{Data: data, Active: "issues"}); err != nil {
		t.Fatal(err)
	}
	html := output.String()
	for _, required := range []string{
		`aria-label="Search mode"`, `aria-sort="descending"`, `aria-describedby="navigator-keyboard-help"`,
		`data-preview-url="/browse/LONG-42/preview"`, `aria-live="polite"`,
	} {
		if !strings.Contains(html, required) {
			t.Errorf("rendered navigator missing %s", required)
		}
	}
}
