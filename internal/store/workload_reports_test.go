package store

import (
	"testing"
)

// The report refuses a field it does not group by rather than counting
// nothing, because a report that silently answers about the wrong thing is
// worse than one that says no.
func TestGroupByRefusesUnknownFields(t *testing.T) {
	store := &Store{}
	if _, err := store.GroupBy(t.Context(), "ws", "usr", "prj", "summary"); err == nil {
		t.Fatal("an unknown field was grouped")
	}
	for _, field := range GroupByFields {
		if _, known := groupByFields[field]; !known {
			t.Fatalf("the page offers %s and the report cannot group by it", field)
		}
	}
}

// Every group the report can produce has a name for work that belongs to no
// group, because a blank row says nothing.
func TestGroupByFallbacksName(t *testing.T) {
	for _, field := range GroupByFields {
		if groupByFallback(field) == "" {
			t.Fatalf("%s has no name for work with no value", field)
		}
	}
}
