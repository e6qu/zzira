# Anonymous access to Jira

Jira's platform specification marks, operation by operation, which REST
operations "can be accessed anonymously". ZZIRA serves those reads to callers
who present no credentials, as Jira's anonymous user.

## Which operations

`api/conformance/anonymous_operations.py` reads the pinned
`api/specs/jira-v3.json` and writes
`internal/api3/anonymous_operations_generated.go`: every GET the specification
opens to anonymous callers, plus the POST operations that only read (searches,
JQL parsing, expression evaluation, bulk issue and comment reads, changelog
lists and permission checks). CI runs the script with `--check` so the table
always matches the specification.

A request is anonymous only when it carries no credential at all: no
Authorization header, no session cookie and no signed app principal. A
credential that fails to verify is answered 401, never downgraded to anonymous.
Every operation outside the table, and every write, still requires an account.

## What the anonymous user sees

The anonymous user holds no global permissions, belongs to no group or role,
and is never a project lead, assignee, reporter or administrator. Access comes
only from permission scheme grants to `anyone` (Jira's "Anyone on the web").
The same decisions apply to signed-in callers, so these reads now enforce the
permissions Jira documents for each of them:

- Projects, components, versions, project properties, statuses and JQL project
  suggestions need Browse Projects; hidden projects answer 404 and drop out of
  collections.
- Issues, comments, worklogs, properties, remote links, changelogs, votes and
  watchers need Browse Projects and issue security; search results are
  filtered the same way.
- Transitions are listed only with Transition issues, editable fields only with
  Edit issues, voters and watchers only with View voters and watchers, and
  create metadata only for projects where the caller holds Create issues.
- User search and browse-user search return nothing without Browse users and
  groups; the user picker then matches an exact display name only, and the
  group-and-user picker is refused. Assignable-user search needs Browse Projects
  for every requested project.
- Email addresses in user beans follow Jira's default profile visibility: they
  are shown to the person themselves and to administrators.
- Filters and dashboards are shared with signed-in users at most, as in Jira
  Cloud, so anonymous callers see none. Recent projects and favourite filters
  are empty, and viewing a project records no history.

## Evidence

- `internal/api3/anonymous_access_test.go` calls every operation in the table
  without credentials against a project granting Browse Projects to anyone and
  a private project, and fails on a 401, a server error, or any response that
  exposes the private project, its work, or an email address. It also checks
  public issue, search, project, component and permission reads, and that
  writes, other operations and failed credentials stay unauthorized.
