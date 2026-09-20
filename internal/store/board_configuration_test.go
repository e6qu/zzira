package store

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/jql"
	"github.com/e6qu/zzira/internal/models"
)

func TestNormalizeBoardConfiguration(t *testing.T) {
	input := BoardConfigurationUpdate{
		QuickFilters: []models.BoardQuickFilter{
			{ID: " mine ", Name: " My work ", Description: " Assigned here ", JQL: " assignee = currentUser() "},
			{ID: "open", Name: "Open", JQL: "status != Done"},
		},
		SwimlaneStrategy: "assignee",
		CardFields:       []string{"labels", "priority"},
		Columns: []models.BoardColumn{
			{Name: " To Do ", StatusIDs: []string{"todo"}, Limit: 4},
			{Name: "Done", StatusIDs: []string{"done"}},
		},
		FilterJQL: " project = ZZ ",
	}
	normalized, err := normalizeBoardConfiguration(input, []string{"todo", "done"}, nil)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if normalized.QuickFilters[0].ID != "mine" || normalized.QuickFilters[0].Name != "My work" || normalized.QuickFilters[0].Position != 0 {
		t.Fatalf("first quick filter was not normalized: %+v", normalized.QuickFilters[0])
	}
	if normalized.QuickFilters[1].Position != 1 {
		t.Fatalf("second quick filter position = %d, want 1", normalized.QuickFilters[1].Position)
	}
	if !reflect.DeepEqual(normalized.CardFields, []string{"labels", "priority"}) {
		t.Fatalf("card fields = %#v", normalized.CardFields)
	}
	wantColumns := []models.BoardColumn{
		{Name: "To Do", StatusIDs: []string{"todo"}, Limit: 4},
		{Name: "Done", StatusIDs: []string{"done"}},
	}
	if !reflect.DeepEqual(normalized.Columns, wantColumns) {
		t.Fatalf("columns = %#v", normalized.Columns)
	}
	if normalized.FilterJQL != "project = ZZ" {
		t.Fatalf("filter = %q", normalized.FilterJQL)
	}
}

func TestNormalizeBoardConfigurationRejectsInvalidInput(t *testing.T) {
	// A valid configuration apart from the one thing each case breaks.
	valid := func(update func(*BoardConfigurationUpdate)) BoardConfigurationUpdate {
		input := BoardConfigurationUpdate{
			SwimlaneStrategy: "none",
			Columns:          []models.BoardColumn{{Name: "To Do", StatusIDs: []string{"todo"}}},
			FilterJQL:        "project = ZZ",
		}
		update(&input)
		return input
	}
	tests := []struct {
		name  string
		input BoardConfigurationUpdate
	}{
		{"swimlanes", valid(func(i *BoardConfigurationUpdate) { i.SwimlaneStrategy = "epic" })},
		{"duplicate quick filter", valid(func(i *BoardConfigurationUpdate) {
			i.QuickFilters = []models.BoardQuickFilter{{ID: "same", Name: "One", JQL: "status = Done"}, {ID: "same", Name: "Two", JQL: "status = Done"}}
		})},
		{"invalid JQL", valid(func(i *BoardConfigurationUpdate) {
			i.QuickFilters = []models.BoardQuickFilter{{ID: "bad", Name: "Bad", JQL: "status ="}}
		})},
		{"unknown card field", valid(func(i *BoardConfigurationUpdate) { i.CardFields = []string{"story_points"} })},
		{"duplicate card field", valid(func(i *BoardConfigurationUpdate) { i.CardFields = []string{"labels", "labels"} })},
		{"unknown status", valid(func(i *BoardConfigurationUpdate) {
			i.Columns = []models.BoardColumn{{Name: "To Do", StatusIDs: []string{"missing"}}}
		})},
		{"status in two columns", valid(func(i *BoardConfigurationUpdate) {
			i.Columns = []models.BoardColumn{{Name: "One", StatusIDs: []string{"todo"}}, {Name: "Two", StatusIDs: []string{"todo"}}}
		})},
		{"no columns", valid(func(i *BoardConfigurationUpdate) { i.Columns = nil })},
		{"unnamed column", valid(func(i *BoardConfigurationUpdate) {
			i.Columns = []models.BoardColumn{{Name: "  ", StatusIDs: []string{"todo"}}}
		})},
		{"negative limit", valid(func(i *BoardConfigurationUpdate) { i.Columns[0].Limit = -1 })},
		{"invalid filter", valid(func(i *BoardConfigurationUpdate) { i.FilterJQL = "project =" })},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := normalizeBoardConfiguration(test.input, []string{"todo"}, nil)
			if !errors.Is(err, ErrBoardValidation) {
				t.Fatalf("error = %v, want ErrBoardValidation", err)
			}
		})
	}
}

func TestBoardFilterQueryCombinesBaseQuickAndAssigneeFilters(t *testing.T) {
	board := &models.Board{
		FilterJQL: "priority = Medium",
		QuickFilters: []models.BoardQuickFilter{
			{ID: "mine", Name: "Mine", JQL: "reporter = currentUser()"},
		},
	}
	query, err := boardFilterQuery(board, []string{"mine", "mine"}, "unassigned")
	if err != nil {
		t.Fatal(err)
	}
	compiled := jql.CompileAt(query, "usr_me", jql.DefaultResolver(), 3)
	if compiled.Err != nil {
		t.Fatal(compiled.Err)
	}
	if !strings.Contains(compiled.Where, "COALESCE(pro.name, pr2.name) = $3") || !strings.Contains(compiled.Where, "i.reporter_id = $4") || !strings.Contains(compiled.Where, "i.assignee_id IS NULL") {
		t.Fatalf("compiled filter = %q", compiled.Where)
	}
	if !reflect.DeepEqual(compiled.Args, []any{"Medium", "usr_me"}) {
		t.Fatalf("args = %#v", compiled.Args)
	}
	if _, err := boardFilterQuery(board, []string{"missing"}, ""); !errors.Is(err, ErrBoardValidation) {
		t.Fatalf("unknown quick filter error = %v", err)
	}
}

func TestBoardConfigurationPersistsAndEmitsAction(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	st, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)

	original, err := st.BoardByIDInWorkspace(ctx, "ws_default", "brd_default")
	if err != nil {
		t.Fatal(err)
	}
	restore := BoardConfigurationUpdate{
		QuickFilters: original.QuickFilters, SwimlaneStrategy: original.SwimlaneStrategy,
		CardFields: original.CardFields, Columns: original.Columns,
		FilterJQL: original.FilterJQL, EstimationFieldID: original.EstimationFieldID,
	}
	t.Cleanup(func() {
		if _, _, err := st.UpdateBoardConfiguration(ctx, "test-cleanup", "ws_default", original.ID, restore); err != nil {
			t.Errorf("restore board configuration: %v", err)
		}
	})

	// Two statuses share one column, which is what a column is for.
	input := BoardConfigurationUpdate{
		QuickFilters:     []models.BoardQuickFilter{{ID: "medium", Name: "Medium priority", JQL: "priority = Medium"}},
		SwimlaneStrategy: "assignee",
		CardFields:       []string{"assignee", "labels"},
		Columns: []models.BoardColumn{
			{Name: "To Do", StatusIDs: []string{"st_todo"}},
			{Name: "Under way", StatusIDs: []string{"st_inprogress", "st_done"}, Limit: 3},
		},
		FilterJQL: original.FilterJQL,
	}
	updated, action, err := st.UpdateBoardConfiguration(ctx, "test-actor", "ws_default", original.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	if action.EntityType != models.EntityBoard || action.EntityID != original.ID || action.Op != models.OpUpsert {
		t.Fatalf("action = %+v", action)
	}
	if updated.SwimlaneStrategy != "assignee" || len(updated.Columns) != 2 || updated.Columns[1].Limit != 3 {
		t.Fatalf("updated board = %+v", updated)
	}
	if !reflect.DeepEqual(updated.StatusIDs(), []string{"st_todo", "st_inprogress", "st_done"}) {
		t.Fatalf("board statuses = %#v", updated.StatusIDs())
	}
	if updated.ColumnOfStatus("st_done") != 1 || updated.ColumnOfStatus("missing") != -1 {
		t.Fatalf("column of status = %d", updated.ColumnOfStatus("st_done"))
	}
	loaded, err := st.BoardByID(ctx, original.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded.QuickFilters, updated.QuickFilters) || !reflect.DeepEqual(loaded.CardFields, updated.CardFields) || !reflect.DeepEqual(loaded.Columns, updated.Columns) {
		t.Fatalf("loaded board configuration = %+v, want %+v", loaded, updated)
	}
}
