package web

import (
	"testing"

	"github.com/e6qu/zzira/internal/models"
)

func TestApplyWikiDatabaseViewFiltersAndSortsNumbers(t *testing.T) {
	rows := []models.WikiDatabaseRow{
		{ID: "1", Values: map[string]string{"phase": "Released", "score": "2"}},
		{ID: "2", Values: map[string]string{"phase": "Planned", "score": "100"}},
		{ID: "3", Values: map[string]string{"phase": "released soon", "score": "10"}},
	}
	columns := []models.WikiDatabaseColumn{{Key: "phase", Type: "text"}, {Key: "score", Type: "number"}}
	got := applyWikiDatabaseView(rows, columns, models.WikiDatabaseView{FilterKey: "phase", FilterValue: "release", SortKey: "score", SortDirection: "desc"})
	if len(got) != 2 || got[0].ID != "3" || got[1].ID != "1" {
		t.Fatalf("view rows = %+v", got)
	}
	if rows[0].ID != "1" || rows[1].ID != "2" {
		t.Fatalf("view mutated source rows = %+v", rows)
	}
}
