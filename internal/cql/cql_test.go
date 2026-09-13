package cql

import (
	"strings"
	"testing"
	"time"
)

// TestParse pins the shape of a query: what it reads, and what it refuses.
func TestParse(t *testing.T) {
	for _, valid := range []string{
		`type = page`,
		`type=page`,
		`type in (page, blogpost)`,
		`type not in (page, blogpost)`,
		`title ~ "release notes"`,
		`title ~ 'release notes'`,
		`space = DEV and type = page`,
		`space = DEV AND (type = page OR type = blogpost)`,
		`not label = archived`,
		`creator = currentUser()`,
		`created >= now("-7d")`,
		`space in favouriteSpaces()`,
		`type = page order by lastmodified desc`,
		`type = page ORDER BY title ASC, created DESC`,
		``,
	} {
		if _, err := Parse(valid); err != nil {
			t.Fatalf("%s: %v", valid, err)
		}
	}
	for _, invalid := range []string{
		`type =`,
		`type = page and`,
		`(type = page`,
		`type = "unterminated`,
		`type = page order by`,
		`= page`,
		`type in page`,
		`type = page extra`,
	} {
		if _, err := Parse(invalid); err == nil {
			t.Fatalf("%s should not parse", invalid)
		}
	}
}

// TestParseStructure checks a query is read as the tree it describes, because
// precedence decides what a reader gets back.
func TestParseStructure(t *testing.T) {
	query, err := Parse(`a = 1 or b = 2 and c = 3`)
	if err != nil {
		t.Fatal(err)
	}
	// AND binds tighter than OR, so this is a = 1 OR (b = 2 AND c = 3).
	or, ok := query.Root.(Or)
	if !ok || len(or.Terms) != 2 {
		t.Fatalf("root: %#v", query.Root)
	}
	if _, ok := or.Terms[0].(Clause); !ok {
		t.Fatalf("first term: %#v", or.Terms[0])
	}
	and, ok := or.Terms[1].(And)
	if !ok || len(and.Terms) != 2 {
		t.Fatalf("second term: %#v", or.Terms[1])
	}
	// Brackets change it.
	query, err = Parse(`(a = 1 or b = 2) and c = 3`)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := query.Root.(And); !ok {
		t.Fatalf("bracketed root: %#v", query.Root)
	}
}

func compile(t *testing.T, source string, ctx Context) (string, []any, string) {
	t.Helper()
	query, err := Parse(source)
	if err != nil {
		t.Fatalf("%s: %v", source, err)
	}
	predicate, args, order, err := Compile(query, ctx, 1)
	if err != nil {
		t.Fatalf("%s: %v", source, err)
	}
	return predicate, args, order
}

// TestCompile pins what a query becomes: the column it reads, and the value it
// compares against arriving as an argument rather than as text in the SQL.
func TestCompile(t *testing.T) {
	ctx := Context{Actor: "usr_1", Now: time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)}

	predicate, args, order := compile(t, `type = page`, ctx)
	if predicate != "entity_type = $1" || len(args) != 1 || args[0] != "page" {
		t.Fatalf("type: %s %v", predicate, args)
	}
	if !strings.HasPrefix(order, "last_modified DESC") {
		t.Fatalf("default order: %s", order)
	}

	// A reader's words never reach the SQL, so a value that looks like SQL is
	// still only a value.
	predicate, args, _ = compile(t, `title ~ "'; DROP TABLE wiki_pages; --"`, ctx)
	if strings.Contains(predicate, "DROP") {
		t.Fatalf("value reached the statement: %s", predicate)
	}
	// An underscore is a single-character wildcard to LIKE, so it is escaped
	// too; the value that reaches the database is still only a value.
	if args[0] != `%'; DROP TABLE wiki\_pages; --%` {
		t.Fatalf("value: %v", args)
	}

	// A wildcard a reader typed is a character they are looking for, not a
	// wildcard, or a search for "100%" would match everything.
	_, args, _ = compile(t, `title ~ "100%"`, ctx)
	if args[0] != `%100\%%` {
		t.Fatalf("escaping: %v", args)
	}

	// currentUser() is answered here, so the query text never carries an
	// account id a reader could change.
	_, args, _ = compile(t, `creator = currentUser()`, ctx)
	if args[0] != "usr_1" {
		t.Fatalf("currentUser: %v", args)
	}

	// A label is one of several a page carries, so it is matched against the
	// set rather than compared with it.
	predicate, _, _ = compile(t, `label = release`, ctx)
	if !strings.Contains(predicate, "= ANY(labels)") {
		t.Fatalf("label: %s", predicate)
	}
	predicate, _, _ = compile(t, `label in (release, draft)`, ctx)
	if !strings.Contains(predicate, "labels && ") {
		t.Fatalf("label in: %s", predicate)
	}

	// A bare date names a day, so equality is the whole day.
	predicate, args, _ = compile(t, `created = "2026-09-01"`, ctx)
	if !strings.Contains(predicate, ">=") || !strings.Contains(predicate, "<") {
		t.Fatalf("date equality: %s", predicate)
	}
	from, ok := args[0].(time.Time)
	if !ok || !from.Equal(time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("day start: %v", args)
	}
	to, ok := args[1].(time.Time)
	if !ok || !to.Equal(time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("day end: %v", args)
	}

	// Relative dates are resolved against when the search was made.
	_, args, _ = compile(t, `lastmodified >= now("-7d")`, ctx)
	if moment := args[0].(time.Time); !moment.Equal(time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("now offset: %v", moment)
	}
	_, args, _ = compile(t, `created >= startOfMonth()`, ctx)
	if moment := args[0].(time.Time); !moment.Equal(time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("startOfMonth: %v", moment)
	}
	_, args, _ = compile(t, `created >= startOfWeek()`, ctx)
	if moment := args[0].(time.Time); !moment.Equal(time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("startOfWeek: %v", moment)
	}

	// A named set is compiled to the set rather than read out into values.
	predicate, args, _ = compile(t, `space in favouriteSpaces()`, ctx)
	if !strings.Contains(predicate, "wiki_relations") || args[0] != "usr_1" {
		t.Fatalf("favouriteSpaces: %s %v", predicate, args)
	}

	// Ordering names a column and always settles ties, so paging cannot show
	// one row twice and miss another.
	_, _, order = compile(t, `type = page order by title`, ctx)
	if order != "title ASC, entity_type, entity_id" {
		t.Fatalf("order: %s", order)
	}
}

// TestCompileRefusals checks a query asking for something CQL does not mean
// here is refused rather than quietly dropped, because a dropped condition
// returns more than was asked for.
func TestCompileRefusals(t *testing.T) {
	ctx := Context{Actor: "usr_1", Now: time.Now()}
	for _, source := range []string{
		`nosuchfield = 1`,
		`type > page`,
		`created ~ "yesterday"`,
		`text = page`,
		`title = currentUser()`,
		`created >= now("-7q")`,
		`created >= nosuchfunction()`,
		`space in favouriteSpaces() order by relevance`,
		`label in favouriteSpaces()`,
		`created >= "not a date"`,
		`space = currentSpace()`,
	} {
		query, err := Parse(source)
		if err != nil {
			continue
		}
		if _, _, _, err = Compile(query, ctx, 1); err == nil {
			t.Fatalf("%s should not compile", source)
		}
	}
	// The same query compiles once the search says which space it is in.
	query, err := Parse(`space = currentSpace()`)
	if err != nil {
		t.Fatal(err)
	}
	_, args, _, err := Compile(query, Context{Actor: "usr_1", CurrentSpace: "DEV"}, 1)
	if err != nil || args[0] != "DEV" {
		t.Fatalf("currentSpace: %v %v", args, err)
	}
}

// TestCompileArgumentNumbering checks the compiler starts where the caller says
// it does, so the caller's own arguments keep their positions.
func TestCompileArgumentNumbering(t *testing.T) {
	query, err := Parse(`type = page and title ~ "x"`)
	if err != nil {
		t.Fatal(err)
	}
	predicate, args, _, err := Compile(query, Context{Actor: "usr_1"}, 4)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(predicate, "$4") || !strings.Contains(predicate, "$5") || len(args) != 2 {
		t.Fatalf("numbering: %s %v", predicate, args)
	}
	if strings.Contains(predicate, "$1") {
		t.Fatalf("numbering overlapped the caller: %s", predicate)
	}
}
