package jql

import "testing"

func TestParseBasic(t *testing.T) {
	q, err := Parse(`status = "In Progress" AND assignee IS EMPTY ORDER BY updated DESC`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	and, ok := q.Root.(And)
	if !ok || len(and.Terms) != 2 {
		t.Fatalf("root = %#v", q.Root)
	}
	if q.OrderBy == nil || q.OrderBy.Field != "updated" || !q.OrderBy.Desc {
		t.Fatalf("order = %+v", q.OrderBy)
	}
}

func TestParseOrNotParens(t *testing.T) {
	_, err := Parse(`summary ~ "walking" OR (project = ZZ AND NOT status = Done)`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
}

func TestParseIn(t *testing.T) {
	q, err := Parse(`status in ("To Do", "In Progress")`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	cl, ok := q.Root.(Clause)
	if !ok || cl.Op != "in" || len(cl.Values) != 2 {
		t.Fatalf("root = %#v", q.Root)
	}
}

func TestParseErrors(t *testing.T) {
	for _, bad := range []string{
		`status =`,                // missing value
		`summary ~ "unterminated`, // unterminated string
		`order updated`,           // ORDER without BY
		`assignee = currentUser(`, // malformed function call
	} {
		if _, err := Parse(bad); err == nil {
			t.Fatalf("expected error for %q", bad)
		}
	}
}

func TestCompile(t *testing.T) {
	q, err := Parse(`status = "In Progress" AND assignee = currentUser() AND summary ~ walk`)
	if err != nil {
		t.Fatal(err)
	}
	c := Compile(q, "usr_me", DefaultResolver())
	if c.Err != nil {
		t.Fatalf("compile: %v", c.Err)
	}
	if len(c.Args) != 3 {
		t.Fatalf("args = %#v", c.Args)
	}
	if c.Args[0] != "In Progress" || c.Args[1] != "usr_me" || c.Args[2] != "%walk%" {
		t.Fatalf("args = %#v", c.Args)
	}
}

func TestCompileUnknownFieldAndOrder(t *testing.T) {
	q, err := Parse("bogus = 1")
	if err != nil {
		t.Fatal(err)
	}
	if c := Compile(q, "u", DefaultResolver()); c.Err == nil {
		t.Fatal("unknown field must fail")
	}
	q, err = Parse("ORDER BY status")
	if err != nil {
		t.Fatal(err)
	}
	if c := Compile(q, "u", DefaultResolver()); c.Err == nil {
		t.Fatal("unknown order field must fail")
	}
	q, err = Parse("ORDER BY bogus")
	if err != nil {
		t.Fatal(err)
	}
	if c := Compile(q, "u", DefaultResolver()); c.Err == nil {
		t.Fatal("unknown order field must fail at compile")
	}
}

func TestCompileProjectUpper(t *testing.T) {
	q, _ := Parse("project = zz")
	c := Compile(q, "u", DefaultResolver())
	if c.Err != nil || c.Args[0] != "ZZ" {
		t.Fatalf("project value = %#v err=%v", c.Args, c.Err)
	}
}
