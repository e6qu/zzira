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
		ColumnLimits:     map[string]int{"todo": 4, "done": 0},
	}
	normalized, err := normalizeBoardConfiguration(input, []string{"todo", "done"})
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
	if !reflect.DeepEqual(normalized.ColumnLimits, map[string]int{"todo": 4}) {
		t.Fatalf("column limits = %#v", normalized.ColumnLimits)
	}
}

func TestNormalizeBoardConfigurationRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name  string
		input BoardConfigurationUpdate
	}{
		{"swimlanes", BoardConfigurationUpdate{SwimlaneStrategy: "epic"}},
		{"duplicate quick filter", BoardConfigurationUpdate{SwimlaneStrategy: "none", QuickFilters: []models.BoardQuickFilter{{ID: "same", Name: "One", JQL: "status = Done"}, {ID: "same", Name: "Two", JQL: "status = Done"}}}},
		{"invalid JQL", BoardConfigurationUpdate{SwimlaneStrategy: "none", QuickFilters: []models.BoardQuickFilter{{ID: "bad", Name: "Bad", JQL: "status ="}}}},
		{"unknown card field", BoardConfigurationUpdate{SwimlaneStrategy: "none", CardFields: []string{"story_points"}}},
		{"duplicate card field", BoardConfigurationUpdate{SwimlaneStrategy: "none", CardFields: []string{"labels", "labels"}}},
		{"unknown column", BoardConfigurationUpdate{SwimlaneStrategy: "none", ColumnLimits: map[string]int{"missing": 2}}},
		{"negative limit", BoardConfigurationUpdate{SwimlaneStrategy: "none", ColumnLimits: map[string]int{"todo": -1}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := normalizeBoardConfiguration(test.input, []string{"todo"})
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
	if !strings.Contains(compiled.Where, "pr2.name = $3") || !strings.Contains(compiled.Where, "i.reporter_id = $4") || !strings.Contains(compiled.Where, "i.assignee_id IS NULL") {
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
		CardFields: original.CardFields, ColumnLimits: original.ColumnLimits,
	}
	t.Cleanup(func() {
		if _, _, err := st.UpdateBoardConfiguration(ctx, "test-cleanup", "ws_default", original.ID, restore); err != nil {
			t.Errorf("restore board configuration: %v", err)
		}
	})

	input := BoardConfigurationUpdate{
		QuickFilters:     []models.BoardQuickFilter{{ID: "medium", Name: "Medium priority", JQL: "priority = Medium"}},
		SwimlaneStrategy: "assignee",
		CardFields:       []string{"assignee", "labels"},
		ColumnLimits:     map[string]int{"st_inprogress": 3},
	}
	updated, action, err := st.UpdateBoardConfiguration(ctx, "test-actor", "ws_default", original.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	if action.EntityType != models.EntityBoard || action.EntityID != original.ID || action.Op != models.OpUpsert {
		t.Fatalf("action = %+v", action)
	}
	if updated.SwimlaneStrategy != "assignee" || updated.ColumnLimits["st_inprogress"] != 3 {
		t.Fatalf("updated board = %+v", updated)
	}
	loaded, err := st.BoardByID(ctx, original.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded.QuickFilters, updated.QuickFilters) || !reflect.DeepEqual(loaded.CardFields, updated.CardFields) || !reflect.DeepEqual(loaded.ColumnLimits, updated.ColumnLimits) {
		t.Fatalf("loaded board configuration = %+v, want %+v", loaded, updated)
	}
}
