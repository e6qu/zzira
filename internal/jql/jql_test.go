package jql

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/models"
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

func TestLoginDateFunctionsUseDurableUserBoundaries(t *testing.T) {
	query, err := Parse(`created > currentLogin() AND resolved <= lastLogin() AND status CHANGED AFTER currentLogin()`)
	if err != nil {
		t.Fatal(err)
	}
	compiled := Compile(query, "usr_me", DefaultResolver())
	if compiled.Err != nil {
		t.Fatal(compiled.Err)
	}
	if strings.Count(compiled.Where, "FROM user_login_state login_state") != 3 ||
		!strings.Contains(compiled.Where, "current_started_at") ||
		!strings.Contains(compiled.Where, "previous_started_at") {
		t.Fatalf("login date SQL = %s", compiled.Where)
	}
	if !reflect.DeepEqual(compiled.Args, []any{"usr_me", "usr_me", "usr_me"}) {
		t.Fatalf("login date args = %#v", compiled.Args)
	}

	resolver := WithCustomFields(DefaultResolver(), []*models.CustomField{{
		ID: "customfield_22000", Name: "Review date", Type: models.CustomFieldDatetime,
		AppKey: "calendar.app", AppModuleKey: "review-date",
	}})
	custom, err := Parse(`customfield_22000 >= currentLogin() AND calendar.app__review-date < lastLogin()`)
	if err != nil {
		t.Fatal(err)
	}
	if result := Compile(custom, "usr_me", resolver); result.Err != nil || !strings.Contains(result.Where, "::timestamptz") {
		t.Fatalf("custom date compile = %s, %v", result.Where, result.Err)
	}

	for _, raw := range []string{
		`summary = currentLogin()`,
		`created IN (currentLogin())`,
		`created > currentLogin(-1d)`,
		`summary = now()`,
		`updated IN (startOfDay())`,
	} {
		invalid, err := Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if result := Compile(invalid, "usr_me", DefaultResolver()); result.Err == nil {
			t.Fatalf("accepted invalid login function query %q: %s", raw, result.Where)
		}
	}
}

func TestApprovalFunctionsCompileAgainstServiceApprovalState(t *testing.T) {
	query, err := Parse(`approvals = myPendingApproval() OR approval = approver(usr_a, "Ada Example") AND approvals != pendingBy(currentUser())`)
	if err != nil {
		t.Fatal(err)
	}
	compiled := Compile(query, "usr_me", DefaultResolver())
	if compiled.Err != nil {
		t.Fatal(compiled.Err)
	}
	if strings.Count(compiled.Where, "FROM service_request_approvals approval JOIN") != 3 ||
		!strings.Contains(compiled.Where, "approval.final_decision='pending'") ||
		!strings.Contains(compiled.Where, "approval_actor.decision='pending'") ||
		!strings.Contains(compiled.Where, "approval_user.username") {
		t.Fatalf("approval SQL = %s", compiled.Where)
	}
	if !reflect.DeepEqual(compiled.Args, []any{"usr_me", "usr_a", "Ada Example", "usr_me"}) {
		t.Fatalf("approval args = %#v", compiled.Args)
	}

	for _, valid := range []string{
		`approvals = approved()`,
		`approvals = myApproval()`,
		`approvals = myPending()`,
		`approvals = pending()`,
		`approvals != pendingApprovalBy(usr_a)`,
	} {
		parsed, err := Parse(valid)
		if err != nil {
			t.Fatal(err)
		}
		if result := Compile(parsed, "usr_me", DefaultResolver()); result.Err != nil {
			t.Fatalf("%s: %v", valid, result.Err)
		}
	}
	for _, invalid := range []string{
		`summary = pending()`,
		`approvals != approved()`,
		`approvals = approver()`,
		`approvals IN (pending())`,
		`approvals = myApproval(usr_a)`,
	} {
		parsed, err := Parse(invalid)
		if err != nil {
			t.Fatal(err)
		}
		if result := Compile(parsed, "usr_me", DefaultResolver()); result.Err == nil {
			t.Fatalf("accepted invalid approval query %q: %s", invalid, result.Where)
		}
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

func TestCloudFunctionAliasesAndRelationFunctionsCompile(t *testing.T) {
	query, err := Parse(`workItem IN linkedWorkItems(OPS-1, blocks, clones) AND workType IN standardWorkTypes() AND space IN projectsLeadByUser(currentUser())`)
	if err != nil {
		t.Fatal(err)
	}
	compiled := Compile(query, "usr_me", DefaultResolver())
	if compiled.Err != nil {
		t.Fatal(compiled.Err)
	}
	for _, fragment := range []string{"FROM issue_links linked", "NOT it.subtask", "pr.lead_account_id"} {
		if !strings.Contains(compiled.Where, fragment) {
			t.Fatalf("Cloud alias SQL missing %q: %s", fragment, compiled.Where)
		}
	}
	if !reflect.DeepEqual(compiled.Args, []any{"OPS-1", "blocks", "clones", "usr_me"}) {
		t.Fatalf("Cloud alias args = %#v", compiled.Args)
	}
}

func TestVersionWatchVoteAndHistoryFunctionsCompile(t *testing.T) {
	query, err := Parse(`fixVersion IN releasedVersions(OPS) AND affectedVersion = earliestUnreleasedVersion(OPS) AND issue IN watchedIssues() AND key IN votedWorkItems() AND issue IN updatedBy(currentUser(), startOfMonth(-1), endOfMonth())`)
	if err != nil {
		t.Fatal(err)
	}
	compiled := Compile(query, "usr_me", DefaultResolver())
	if compiled.Err != nil {
		t.Fatal(compiled.Err)
	}
	for _, fragment := range []string{"FROM project_versions version_value", "version_value.released=true", "version_value.released=false", "FROM watchers watched", "FROM issue_votes voted", "FROM actions updated_action"} {
		if !strings.Contains(compiled.Where, fragment) {
			t.Fatalf("function SQL missing %q: %s", fragment, compiled.Where)
		}
	}
	if len(compiled.Args) != 7 || compiled.Args[2] != "usr_me" || compiled.Args[3] != "usr_me" || compiled.Args[4] != "usr_me" {
		t.Fatalf("function args = %#v", compiled.Args)
	}
	for _, value := range compiled.Args[5:] {
		if _, ok := value.(time.Time); !ok {
			t.Fatalf("updatedBy date resolved to %T", value)
		}
	}
}

func TestDateFunctionNaturalIncrementsAndCloudWeek(t *testing.T) {
	now := time.Date(2026, time.September, 9, 12, 0, 0, 0, time.UTC)
	start, err := resolveDateFunction("startOfWeek", []string{"1"}, now)
	if err != nil || !start.Equal(time.Date(2026, time.September, 13, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("startOfWeek(1) = %s, %v", start, err)
	}
	end, err := resolveDateFunction("endOfWeek", nil, now)
	wantEnd := time.Date(2026, time.September, 13, 0, 0, 0, 0, time.UTC).Add(-time.Nanosecond)
	if err != nil || !end.Equal(wantEnd) {
		t.Fatalf("endOfWeek() = %s, want %s (%v)", end, wantEnd, err)
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

func TestCompileComponentsAsCanonicalMultiValueField(t *testing.T) {
	query, err := Parse(`components in componentsLeadByUser(currentUser()) AND component != Legacy`)
	if err != nil {
		t.Fatal(err)
	}
	compiled := Compile(query, "usr_me", DefaultResolver())
	if compiled.Err != nil {
		t.Fatal(compiled.Err)
	}
	if !strings.Contains(compiled.Where, "project_components component_lead") || !strings.Contains(compiled.Where, "jsonb_array_elements") {
		t.Fatalf("component SQL = %s", compiled.Where)
	}
	if !reflect.DeepEqual(compiled.Args, []any{"usr_me", "Legacy"}) {
		t.Fatalf("component args = %#v", compiled.Args)
	}
	invalid, _ := Parse(`component = componentsLeadByUser()`)
	if compiled = Compile(invalid, "usr_me", DefaultResolver()); compiled.Err == nil {
		t.Fatal("componentsLeadByUser accepted a non-list operator")
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
