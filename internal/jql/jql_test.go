package jql

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

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
	if c := Compile(q, "u", DefaultResolver()); c.Err != nil || c.OrderSQL != "st.name ASC, i.id ASC" {
		t.Fatalf("status sort = %q, %v", c.OrderSQL, c.Err)
	}
	q, err = Parse("ORDER BY bogus")
	if err != nil {
		t.Fatal(err)
	}
	if c := Compile(q, "u", DefaultResolver()); c.Err == nil {
		t.Fatal("unknown order field must fail at compile")
	}
}

func TestParseAndCompileNotIn(t *testing.T) {
	query, err := Parse(`status NOT IN (Done, Closed) AND labels NOT IN (archived, stale)`)
	if err != nil {
		t.Fatal(err)
	}
	compiled := Compile(query, "usr_me", DefaultResolver())
	if compiled.Err != nil {
		t.Fatal(compiled.Err)
	}
	if !strings.Contains(compiled.Where, "st.name IS NOT NULL AND NOT (st.name IN") ||
		!strings.Contains(compiled.Where, "cardinality(i.labels) > 0 AND NOT") {
		t.Fatalf("NOT IN SQL = %s", compiled.Where)
	}
}

func TestRelativeDateFunctionsAndCurrentUser(t *testing.T) {
	query, err := Parse(`updated >= startOfMonth(-1M) AND created < endOfDay() AND due >= -5d AND assignee = currentUser()`)
	if err != nil {
		t.Fatal(err)
	}
	compiled := Compile(query, "usr_me", DefaultResolver())
	if compiled.Err != nil {
		t.Fatal(compiled.Err)
	}
	if len(compiled.Args) != 4 || compiled.Args[3] != "usr_me" {
		t.Fatalf("args = %#v", compiled.Args)
	}
	for _, value := range compiled.Args[:3] {
		if _, ok := value.(time.Time); !ok {
			t.Fatalf("date function resolved to %T, want time.Time", value)
		}
	}
}

func TestHistoryClausesCompileAgainstImmutableActions(t *testing.T) {
	query, err := Parse(`status WAS IN ("In Progress", Done) BY currentUser() BEFORE startOfDay() OR assignee CHANGED FROM currentUser() DURING (startOfMonth(-1M), endOfMonth())`)
	if err != nil {
		t.Fatal(err)
	}
	compiled := Compile(query, "usr_me", DefaultResolver())
	if compiled.Err != nil {
		t.Fatal(compiled.Err)
	}
	if strings.Count(compiled.Where, "FROM actions ah") != 2 ||
		!strings.Contains(compiled.Where, "ah.payload->'diff' ? 'status'") ||
		!strings.Contains(compiled.Where, "ah.payload->'diff' ? 'assignee'") ||
		!strings.Contains(compiled.Where, "ah.created_at BETWEEN") {
		t.Fatalf("history SQL = %s", compiled.Where)
	}
}

func TestMultipleOrderFieldsAreBoundedAndDeterministic(t *testing.T) {
	query, err := Parse(`project = ZZ ORDER BY priority DESC, updated ASC, key DESC`)
	if err != nil {
		t.Fatal(err)
	}
	compiled := Compile(query, "usr_me", DefaultResolver())
	if compiled.Err != nil {
		t.Fatal(compiled.Err)
	}
	if compiled.OrderSQL != "pr2.name DESC, i.updated_at ASC, i.key DESC, i.id ASC" {
		t.Fatalf("order = %q", compiled.OrderSQL)
	}
	if _, err := Parse(`project = ZZ ORDER BY key,key,key,key,key,key,key,key`); err == nil {
		t.Fatal("accepted more than seven ORDER BY fields")
	}
}

func TestRelationBackedListFunctionsCompile(t *testing.T) {
	query, err := Parse(`assignee IN (membersOf(engineering), usr_direct) AND sprint IN (openSprints(), "Sprint 2") AND issuetype IN (standardIssueTypes()) AND issue IN linkedIssues(OPS-1, blocks)`)
	if err != nil {
		t.Fatal(err)
	}
	compiled := Compile(query, "usr_me", DefaultResolver())
	if compiled.Err != nil {
		t.Fatal(compiled.Err)
	}
	for _, fragment := range []string{"FROM sites member_site", "FROM sprint_issues sprint_match", "NOT it.subtask", "FROM issue_links linked"} {
		if !strings.Contains(compiled.Where, fragment) {
			t.Fatalf("list-function SQL missing %q: %s", fragment, compiled.Where)
		}
	}
	if !reflect.DeepEqual(compiled.Args, []any{"engineering", "usr_direct", "active", "Sprint 2", "OPS-1", "blocks"}) {
		t.Fatalf("list-function args = %#v", compiled.Args)
	}
	for _, raw := range []string{`sprint IN openSprints(today)`, `assignee IN membersOf()`, `issue IN linkedIssues()`, `issuetype IN standardIssueTypes(extra)`} {
		parsed, parseErr := Parse(raw)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		if invalid := Compile(parsed, "usr_me", DefaultResolver()); invalid.Err == nil {
			t.Fatalf("invalid function compiled: %s", raw)
		}
	}
}

func TestJiraIssueIDCompilesAsNumeric(t *testing.T) {
	query, err := Parse(`id IN (10001, 10002)`)
	if err != nil {
		t.Fatal(err)
	}
	compiled := Compile(query, "usr_me", DefaultResolver())
	if compiled.Err != nil || !strings.Contains(compiled.Where, "i.jira_id IN") || !reflect.DeepEqual(compiled.Args, []any{int64(10001), int64(10002)}) {
		t.Fatalf("numeric issue ID SQL=%s args=%#v err=%v", compiled.Where, compiled.Args, compiled.Err)
	}
	bad, _ := Parse(`id = iss_internal`)
	if compiled = Compile(bad, "usr_me", DefaultResolver()); compiled.Err == nil {
		t.Fatal("internal issue ID was accepted as a Jira ID")
	}
}

func TestCompileLabelsAsMultiValueField(t *testing.T) {
	query, err := Parse(`labels in (incident, urgent) AND labels != archived`)
	if err != nil {
		t.Fatal(err)
	}
	compiled := Compile(query, "u", DefaultResolver())
	if compiled.Err != nil {
		t.Fatal(compiled.Err)
	}
	if !strings.Contains(compiled.Where, "$1 = ANY(i.labels)") || !strings.Contains(compiled.Where, "$3 = ANY(i.labels)") {
		t.Fatalf("labels SQL = %s", compiled.Where)
	}
	if !reflect.DeepEqual(compiled.Args, []any{"incident", "urgent", "archived"}) {
		t.Fatalf("labels args = %#v", compiled.Args)
	}
}

func TestCompileProjectUpper(t *testing.T) {
	q, _ := Parse("project = zz")
	c := Compile(q, "u", DefaultResolver())
	if c.Err != nil || c.Args[0] != "ZZ" {
		t.Fatalf("project value = %#v err=%v", c.Args, c.Err)
	}
}

func TestSetOrderPreservesQuotedAndNestedText(t *testing.T) {
	got, err := SetOrder(`summary ~ "order by design" AND (status = Done) ORDER BY updated DESC`, "priority", false)
	if err != nil {
		t.Fatal(err)
	}
	if got != `summary ~ "order by design" AND (status = Done) ORDER BY priority ASC` {
		t.Fatalf("SetOrder = %q", got)
	}
	got, err = SetOrder(`status = "To Do"`, "key", true)
	if err != nil || got != `status = "To Do" ORDER BY key DESC` {
		t.Fatalf("SetOrder append = %q, %v", got, err)
	}
}

func TestTransformClauseFunctionsReplacesOnlyWholeFunctionClauses(t *testing.T) {
	query, err := Parse(`project = ZZ AND issue in riskIssues("high", platform) AND labels in (urgent, teamLabels())`)
	if err != nil {
		t.Fatal(err)
	}
	called := 0
	err = TransformClauseFunctions(query, func(invocation FunctionInvocation) (Node, bool, error) {
		if invocation.Name != "riskIssues" {
			return nil, false, nil
		}
		called++
		if invocation.Field != "issue" || invocation.Operator != "in" || !reflect.DeepEqual(invocation.Arguments, []string{"high", "platform"}) {
			t.Fatalf("invocation = %+v", invocation)
		}
		replacement, parseErr := Parse(`priority = Highest OR status = Blocked`)
		return replacement.Root, true, parseErr
	})
	if err != nil || called != 1 {
		t.Fatalf("transform calls=%d err=%v", called, err)
	}
	root, ok := query.Root.(And)
	if !ok || len(root.Terms) != 3 {
		t.Fatalf("transformed root = %#v", query.Root)
	}
	labels, ok := root.Terms[2].(Clause)
	if !ok || !reflect.DeepEqual(labels.Values, []string{"urgent", "teamLabels()"}) {
		t.Fatalf("mixed-list clause changed = %#v", root.Terms[2])
	}
}

func TestParseCustomFunctionOnlyOperators(t *testing.T) {
	for input, operator := range map[string]string{
		`summary ~= containsText(test)`: "~=",
		`resolution IS resolvedBy(app)`: "is",
		`resolution IS NOT ownedBy(me)`: "isnot",
	} {
		query, err := Parse(input)
		if err != nil {
			t.Fatalf("parse %q: %v", input, err)
		}
		clause, ok := query.Root.(Clause)
		if !ok || clause.Op != operator || len(clause.Values) != 1 {
			t.Fatalf("parse %q = %#v", input, query.Root)
		}
	}
}
