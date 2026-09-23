package aql

import (
	"strings"
	"testing"
)

// The part of AQL a form filter writes: an object's type, name and key, its
// attributes by name, and the words between them.
func TestFiltersCompileToConditions(t *testing.T) {
	for _, tc := range []struct {
		query    string
		contains []string
		args     []any
	}{
		{query: `objectType = "Business services"`, contains: []string{"lower(COALESCE(s.name,''))=lower($1)"}, args: []any{"Business services"}},
		{query: `Name LIKE checkout`, contains: []string{"o.label ILIKE $1"}, args: []any{"%checkout%"}},
		{query: `Key = "SVC-1"`, contains: []string{"o.object_key"}, args: []any{"SVC-1"}},
		{query: `"Owner" = Payments`, contains: []string{"o.values->>'owner'"}, args: []any{"Payments"}},
		{query: `Tier IN ("1", "2")`, contains: []string{"o.values->>'tier'", " OR "}, args: []any{"1", "2"}},
		{query: `Tier NOT IN ("3")`, contains: []string{"(NOT ("}, args: []any{"3"}},
		{query: `Runbook IS EMPTY`, contains: []string{"COALESCE(o.values->>'runbook','')=''"}},
		{query: `Runbook IS NOT EMPTY`, contains: []string{"<>''"}},
		{query: `objectType = Vendors AND "Contact" != "ops@bank.test"`, contains: []string{" AND ", "(NOT ("}, args: []any{"Vendors", "ops@bank.test"}},
		{query: `objectType = Vendors OR objectType = Infrastructure`, contains: []string{" OR "}, args: []any{"Vendors", "Infrastructure"}},
		{query: `NOT (objectType = Vendors)`, contains: []string{"(NOT ("}, args: []any{"Vendors"}},
		// A quote inside a quoted value is doubled, as it is in JQL; the
		// other quote character is simply a character.
		{query: `"Owner" = "O""Brien"`, contains: []string{"o.values->>'owner'"}, args: []any{`O"Brien`}},
		{query: `"Owner" = "O'Brien"`, contains: []string{"o.values->>'owner'"}, args: []any{"O'Brien"}},
	} {
		t.Run(tc.query, func(t *testing.T) {
			query, err := Parse(tc.query)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			compiled := query.Compile(DefaultColumns(), 1)
			for _, want := range tc.contains {
				if !strings.Contains(compiled.Where, want) {
					t.Fatalf("where = %q, want it to contain %q", compiled.Where, want)
				}
			}
			if len(compiled.Args) != len(tc.args) {
				t.Fatalf("args = %v, want %v", compiled.Args, tc.args)
			}
			for index, want := range tc.args {
				if compiled.Args[index] != want {
					t.Fatalf("arg %d = %v, want %v", index, compiled.Args[index], want)
				}
			}
		})
	}
}

// A filter this package cannot read says so, rather than matching everything.
func TestUnreadableFiltersAreRefused(t *testing.T) {
	for _, tc := range []struct{ query, says string }{
		{`objectType`, "compared with nothing"},
		{`objectType = `, "expected a value"},
		{`objectType == Vendors`, "only = and != compare"},
		{`objectType ~ Vendors`, "is not something that compares"},
		{`objectType > Vendors`, "only = and != compare"},
		{`(objectType = Vendors`, "never closed"},
		{`objectType = "Vendors`, "never closed"},
		{`objectType IN Vendors`, "bracketed list"},
		{`objectType IN (Vendors`, "never closed"},
		{`objectType = Vendors AND`, "ends where a condition was expected"},
		{`objectType = Vendors NOT IN (1)`, "after the filter"},
		{`objectType IS Vendors`, "expected EMPTY after IS"},
	} {
		t.Run(tc.query, func(t *testing.T) {
			_, err := Parse(tc.query)
			if err == nil {
				t.Fatal("the filter was accepted")
			}
			if !strings.Contains(err.Error(), tc.says) {
				t.Fatalf("error %q does not say %q", err, tc.says)
			}
		})
	}
}

// No filter is every object, which is what an unfiltered field offers.
func TestAnEmptyFilterSelectsEverything(t *testing.T) {
	query, err := Parse("   ")
	if err != nil {
		t.Fatal(err)
	}
	if !query.Empty() {
		t.Fatal("an empty filter is not empty")
	}
	if compiled := query.Compile(DefaultColumns(), 1); compiled.Where != "TRUE" || len(compiled.Args) != 0 {
		t.Fatalf("compiled = %+v", compiled)
	}
}
