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
		// Jira Service Management's own fields, named as its documentation
		// names them.
		{query: `"Request participants" = usr_ana`, contains: []string{"FROM service_request_participants participant_row", "participant_row.request_issue_id=i.id"}, args: []any{"usr_ana"}},
		{query: `"request-channel-type" = portal`, contains: []string{"FROM service_requests service_request"}, args: []any{"portal"}},
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
