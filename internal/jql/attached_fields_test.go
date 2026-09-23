package jql

import (
	"strings"
	"testing"
)

func compileJQL(t *testing.T, query string) Compiled {
	t.Helper()
	parsed, err := Parse(query)
	if err != nil {
		t.Fatalf("parse %q: %v", query, err)
	}
	compiled := Compile(parsed, "usr_me", DefaultResolver())
	if compiled.Err != nil {
		t.Fatalf("compile %q: %v", query, compiled.Err)
	}
	return compiled
}

// The fields that are not columns on the work item: its text and comments,
// the people watching or voting, its files, the links it takes part in.
func TestAttachedFieldsCompile(t *testing.T) {
	for _, tc := range []struct {
		query    string
		contains []string
		args     []any
	}{
		{query: `text ~ "release"`, contains: []string{"i.summary ILIKE", "FROM comments comment_row"}, args: []any{"%release%"}},
		{query: `comment ~ "release"`, contains: []string{"FROM comments comment_row"}, args: []any{"%release%"}},
		{query: `text !~ "release"`, contains: []string{"(NOT ("}, args: []any{"%release%"}},
		{query: `watcher = currentUser()`, contains: []string{"FROM watchers watcher_row", "watcher_row.user_id=$1"}, args: []any{"usr_me"}},
		{query: `watchers IN (usr_ana, usr_bo)`, contains: []string{"$1", "$2"}, args: []any{"usr_ana", "usr_bo"}},
		{query: `watcher IS EMPTY`, contains: []string{"(NOT EXISTS (SELECT 1 FROM watchers"}},
		{query: `voter = usr_ana`, contains: []string{"FROM issue_votes voter_row"}, args: []any{"usr_ana"}},
		{query: `votes > 2`, contains: []string{"FROM issue_votes vote_count"}, args: []any{int64(2)}},
		{query: `attachments IS NOT EMPTY`, contains: []string{"FROM attachments attachment_row"}},
		{query: `issueLinkType = blocks`, contains: []string{"JOIN issue_link_types link_type", "lower(link_type.inward)"}, args: []any{"blocks"}},
		{query: `hierarchyLevel >= 1`, contains: []string{"hierarchy_level"}, args: []any{int64(1)}},
		{query: `level = Managers`, contains: []string{"security_schemes level_scheme"}, args: []any{"Managers"}},
		{query: `category = Delivery`, contains: []string{"project_categories project_category"}, args: []any{"Delivery"}},
		{query: `statusCategoryChangedDate >= -7d`, contains: []string{"i.status_category_changed_at"}},
		{query: `statusCategoryChangeDate >= -7d`, contains: []string{"i.status_category_changed_at"}},
		// Jira Service Management's own fields, named as its documentation
		// names them.
		{query: `"Request participants" = usr_ana`, contains: []string{"FROM service_request_participants participant_row", "participant_row.request_issue_id=i.id"}, args: []any{"usr_ana"}},
		{query: `"request-channel-type" = portal`, contains: []string{"FROM service_requests service_request"}, args: []any{"portal"}},
		{query: `"Request Type" = "Report an incident"`, contains: []string{"JOIN service_request_types request_type"}, args: []any{"Report an incident"}},
		{query: `"request-type" IS NOT EMPTY`, contains: []string{"service_request_types request_type"}},
		{query: `Organizations = "Riverbank"`, contains: []string{"service_desk_organizations desk_organization", "lower(organization.name)="}, args: []any{"Riverbank"}},
		{query: `organization IN ("Riverbank", "Kestrel")`, contains: []string{"service_organization_users organization_member"}, args: []any{"Riverbank", "Kestrel"}},
		{query: `Organizations IS EMPTY`, contains: []string{"(NOT EXISTS (SELECT 1 FROM service_requests organization_request"}},
		// The aliases Jira's own documentation uses.
		{query: `issuekey = ZZ-1`, contains: []string{"i.key ="}, args: []any{"ZZ-1"}},
		{query: `type = Bug`, contains: []string{"ito.name, it.name"}, args: []any{"Bug"}},
		{query: `timeoriginalestimate > 2h`, contains: []string{"i.original_estimate_seconds"}, args: []any{int64(7200)}},
		{query: `timeestimate < 1h`, contains: []string{"i.remaining_estimate_seconds"}, args: []any{int64(3600)}},
	} {
		t.Run(tc.query, func(t *testing.T) {
			compiled := compileJQL(t, tc.query)
			for _, want := range tc.contains {
				if !strings.Contains(compiled.Where, want) {
					t.Fatalf("where = %q, want it to contain %q", compiled.Where, want)
				}
			}
			if len(tc.args) > 0 {
				if len(compiled.Args) != len(tc.args) {
					t.Fatalf("args = %#v, want %#v", compiled.Args, tc.args)
				}
				for index := range tc.args {
					if compiled.Args[index] != tc.args[index] {
						t.Fatalf("args = %#v, want %#v", compiled.Args, tc.args)
					}
				}
			}
		})
	}
}

// Each of these fields answers only what Jira lets it answer.
func TestAttachedFieldsRefuseWrongOperators(t *testing.T) {
	for query, want := range map[string]string{
		`text = release`:          "text supports only",
		`comment IS EMPTY`:        "comment supports only",
		`attachments = thing.txt`: "attachments supports only",
		`watcher ~ ana`:           "watcher supports",
		`issueLinkType ~ blocks`:  "issueLinkType supports",
	} {
		parsed, err := Parse(query)
		if err != nil {
			t.Fatalf("parse %q: %v", query, err)
		}
		compiled := Compile(parsed, "usr_me", DefaultResolver())
		if compiled.Err == nil || !strings.Contains(compiled.Err.Error(), want) {
			t.Fatalf("%q: err = %v, want %q", query, compiled.Err, want)
		}
	}
}

// Jira orders by how many people voted and by where the work sits in the
// hierarchy, so those two are orderable as well as searchable.
func TestVotesAndHierarchyLevelOrder(t *testing.T) {
	for query, want := range map[string]string{
		"ORDER BY votes DESC":     "FROM issue_votes vote_count",
		"ORDER BY hierarchyLevel": "hierarchy_level",
	} {
		compiled := compileJQL(t, query)
		if !strings.Contains(compiled.OrderSQL, want) {
			t.Fatalf("%q order = %q, want it to contain %q", query, compiled.OrderSQL, want)
		}
	}
}

// A saved filter is a query a query may name: it is compiled where it is
// named, it may not name itself, and one that cannot be read does not exist.
func TestSavedFilterClause(t *testing.T) {
	saved := map[string]string{
		"open work":  `status != Done`,
		"mine":       `assignee = currentUser() AND filter = "open work"`,
		"circular":   `filter = "circular"`,
		"indirect a": `filter = "indirect b"`,
		"indirect b": `filter = "indirect a"`,
		"broken":     `status IN (`,
	}
	resolver := WithFilterJQL(DefaultResolver(), func(userID, nameOrID string) (string, bool) {
		text, ok := saved[strings.ToLower(nameOrID)]
		return text, ok
	})
	compile := func(query string) Compiled {
		t.Helper()
		parsed, err := Parse(query)
		if err != nil {
			t.Fatalf("parse %q: %v", query, err)
		}
		return Compile(parsed, "usr_me", resolver)
	}

	compiled := compile(`filter = "open work"`)
	if compiled.Err != nil || !strings.Contains(compiled.Where, "st.name") {
		t.Fatalf("where = %q, err = %v", compiled.Where, compiled.Err)
	}
	if len(compiled.Args) != 1 || compiled.Args[0] != "Done" {
		t.Fatalf("args = %#v", compiled.Args)
	}

	// A filter that names another is compiled through it, with the searching
	// person's own currentUser().
	nested := compile(`filter = mine`)
	if nested.Err != nil || !strings.Contains(nested.Where, "i.assignee_id") || !strings.Contains(nested.Where, "st.name") {
		t.Fatalf("nested where = %q, err = %v", nested.Where, nested.Err)
	}
	if len(nested.Args) != 2 || nested.Args[0] != "usr_me" || nested.Args[1] != "Done" {
		t.Fatalf("nested args = %#v", nested.Args)
	}

	// Aliases, negation and lists.
	if c := compile(`savedFilter IN ("open work", mine)`); c.Err != nil || !strings.Contains(c.Where, " OR ") {
		t.Fatalf("list where = %q, err = %v", c.Where, c.Err)
	}
	if c := compile(`filter != "open work"`); c.Err == nil && !strings.HasPrefix(c.Where, "(NOT ") {
		t.Fatalf("negated where = %q", c.Where)
	}

	for query, want := range map[string]string{
		`filter = circular`:       "leads back to itself",
		`filter = "indirect a"`:   "leads back to itself",
		`filter = "not a filter"`: "does not exist or you do not have permission",
		`filter = broken`:         "no longer parses",
		`filter ~ mine`:           "filter supports",
	} {
		if c := compile(query); c.Err == nil || !strings.Contains(c.Err.Error(), want) {
			t.Fatalf("%q: err = %v, want %q", query, c.Err, want)
		}
	}

	// Without a lookup -- a compile that has no site behind it -- the field
	// says so rather than matching everything.
	parsed, err := Parse(`filter = mine`)
	if err != nil {
		t.Fatal(err)
	}
	if c := Compile(parsed, "usr_me", DefaultResolver()); c.Err == nil {
		t.Fatal("a filter compiled with no filters behind it")
	}
}

// What the person searching has opened: the date they last opened a work
// item, and the list of what they have opened at all.
func TestViewedFields(t *testing.T) {
	compiled := compileJQL(t, `lastViewed >= -7d ORDER BY lastViewed DESC`)
	if !strings.Contains(compiled.Where, "FROM issue_views issue_view") || !strings.Contains(compiled.Where, "issue_view.user_id=$1") {
		t.Fatalf("where = %q", compiled.Where)
	}
	if !strings.Contains(compiled.OrderSQL, "issue_view.user_id=$3") {
		t.Fatalf("order = %q, args = %#v", compiled.OrderSQL, compiled.Args)
	}
	// The reader is an ordinary parameter, numbered where it is used: once in
	// the clause, once in the ordering.
	if len(compiled.Args) != 3 || compiled.Args[0] != "usr_me" || compiled.Args[2] != "usr_me" {
		t.Fatalf("args = %#v", compiled.Args)
	}
	history := compileJQL(t, `key IN issueHistory()`)
	if !strings.Contains(history.Where, "FROM issue_views viewed") {
		t.Fatalf("issueHistory where = %q", history.Where)
	}
}

// Work another system names: the global id of a remote link finds it.
func TestRemoteLinkFunction(t *testing.T) {
	compiled := compileJQL(t, `key IN issuesWithRemoteLinksByGlobalId("system-1", "system-2")`)
	if !strings.Contains(compiled.Where, "FROM remote_issue_links remote_link") {
		t.Fatalf("where = %q", compiled.Where)
	}
	if len(compiled.Args) != 2 || compiled.Args[0] != "system-1" || compiled.Args[1] != "system-2" {
		t.Fatalf("args = %#v", compiled.Args)
	}
	parsed, err := Parse(`key IN issuesWithRemoteLinksByGlobalId()`)
	if err != nil {
		t.Fatal(err)
	}
	if c := Compile(parsed, "usr_me", DefaultResolver()); c.Err == nil || !strings.Contains(c.Err.Error(), "between 1 and 100") {
		t.Fatalf("err = %v", c.Err)
	}
}
