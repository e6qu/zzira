package web

import (
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/models"
)

func TestBoardPageURLPreservesSelectedControls(t *testing.T) {
	target := boardPageURL("board / one", []string{"mine", "urgent"}, "usr 1")
	parsed, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.EscapedPath() != "/board/board%20%2F%20one" {
		t.Fatalf("path = %q", parsed.EscapedPath())
	}
	if !reflect.DeepEqual(parsed.Query()["qf"], []string{"mine", "urgent"}) || parsed.Query().Get("assignee") != "usr 1" {
		t.Fatalf("query = %v", parsed.Query())
	}
}

func TestSelectedBoardFiltersDeduplicatesAndBoundsInput(t *testing.T) {
	values := url.Values{"qf": {"mine", "", "mine"}}
	for index := 0; index < 25; index++ {
		values.Add("qf", "filter-"+string(rune('a'+index)))
	}
	selected := selectedBoardFilters(values)
	if len(selected) != 20 {
		t.Fatalf("selected %d filters, want 20", len(selected))
	}
	if selected[0] != "mine" || selected[1] != "filter-a" {
		t.Fatalf("selected = %#v", selected)
	}
}

func TestBoardConfigurationFormParsesColumnsAndQuickFilterRows(t *testing.T) {
	form := url.Values{
		"swimlanes":              {"assignee"},
		"cardField":              {"priority", "labels"},
		"filterJQL":              {"project = ZZ"},
		"estimationField":        {"customfield_10001"},
		"columnKey":              {"c0", "c1", "c2"},
		"columnName":             {"To Do", "Working", "Gone"},
		"columnLimit":            {"0", "5", "0"},
		"deleteColumn":           {"c2"},
		"newColumnName":          {"Waiting"},
		"statusID":               {"todo", "doing", "review", "done", "cancelled"},
		"statusColumn_todo":      {"c0"},
		"statusColumn_doing":     {"c1"},
		"statusColumn_review":    {"c1"},
		"statusColumn_done":      {"new"},
		"statusColumn_cancelled": {"c2"},
		"quickFilterID":          {"existing", "", "remove-me"},
		"quickFilterName":        {"Open", "Mine", "Removed"},
		"quickFilterJQL":         {"status != Done", "assignee = currentUser()", "status = Done"},
		"quickFilterDescription": {"Still moving", "My work", "Delete this"},
		"deleteQuickFilter":      {"remove-me"},
	}
	request := httptest.NewRequest("POST", "/board/brd/settings", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	board := &models.Board{Columns: []models.BoardColumn{{Name: "To Do", StatusIDs: []string{"todo"}}}}

	input, err := boardConfigurationForm(request, board)
	if err != nil {
		t.Fatal(err)
	}
	if input.SwimlaneStrategy != "assignee" || !reflect.DeepEqual(input.CardFields, []string{"priority", "labels"}) {
		t.Fatalf("layout input = %+v", input)
	}
	if input.FilterJQL != "project = ZZ" || input.EstimationFieldID != "customfield_10001" {
		t.Fatalf("scope input = %+v", input)
	}
	// The deleted column goes, the new one is added last, and a column keeps
	// every status that named it -- while the status that named the deleted
	// column leaves the board rather than following it.
	want := []models.BoardColumn{
		{Name: "To Do", StatusIDs: []string{"todo"}},
		{Name: "Working", StatusIDs: []string{"doing", "review"}, Limit: 5},
		{Name: "Waiting", StatusIDs: []string{"done"}},
	}
	if !reflect.DeepEqual(input.Columns, want) {
		t.Fatalf("columns = %#v", input.Columns)
	}
	if len(input.QuickFilters) != 2 || input.QuickFilters[0].ID != "existing" || input.QuickFilters[1].Name != "Mine" {
		t.Fatalf("quick filters = %+v", input.QuickFilters)
	}
}

func TestBoardConfigurationFormRejectsMalformedRowsAndLimits(t *testing.T) {
	board := &models.Board{Columns: []models.BoardColumn{{Name: "To Do", StatusIDs: []string{"todo"}}}}
	for _, encoded := range []string{
		"swimlanes=none&columnKey=c0&columnName=To+Do&columnLimit=1.5",
		"swimlanes=none&columnKey=c0&columnName=To+Do",
		"swimlanes=none&quickFilterID=one&quickFilterName=One",
	} {
		request := httptest.NewRequest("POST", "/board/brd/settings", strings.NewReader(encoded))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if _, err := boardConfigurationForm(request, board); err == nil {
			t.Fatalf("form %q should fail", encoded)
		}
	}
}
