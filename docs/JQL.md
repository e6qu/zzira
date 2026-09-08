# JQL and issue search

Updated: 2026-09-09

ZZIRA uses one JQL parser and PostgreSQL compiler for the Jira REST search
resources, the browser issue navigator, saved filters, Agile board and quick
filters, service queues and SLA goals, dashboard gadgets, webhooks, and
scheduled automation. Callers add workspace and issue-security predicates
outside the compiled user expression, so aggregates and pages cannot include a
work item the viewer cannot browse.

## Implemented query behavior

- `AND`, `OR`, unary `NOT`, nested parentheses, and bare-text search;
- `=`, `!=`, `~`, `!~`, `>`, `>=`, `<`, `<=`, `IN`, `NOT IN`, `IS EMPTY`,
  and `IS NOT EMPTY`;
- core work-item, project, type, status/category, priority, user, label,
  version, parent, environment, component, sprint, resolution, and date fields,
  plus typed custom fields and installed-app scalar field aliases;
- `currentUser()`, `now()`, start/end of day, week, month, and year functions,
  Jira date literals, relative values such as `-5d`, and function increments
  such as `startOfMonth(-1M)`;
- immutable `WAS`, `WAS IN`, `WAS NOT`, `WAS NOT IN`, and `CHANGED` evaluation
  for retained field diffs, including `FROM`, `TO`, `BY`, `BEFORE`, `AFTER`,
  and `DURING` predicates;
- as many as seven `ORDER BY` fields with an issue-ID tie-breaker for
  deterministic paging; and
- legacy offset search plus enhanced search with seven-day opaque cursors bound
  to the JQL, workspace, and user.

The history compiler evaluates `actions`, ZZIRA's immutable ordered change log.
It compares both stored IDs and display values, so a status query can use either
the status ID or its name. Relative dates are resolved once per compilation in
UTC so all clauses in one execution share the same clock value.

## Search resources

Both methods of `/rest/api/3/search` and `/rest/api/3/search/jql`, plus
`POST /rest/api/3/search/approximate-count`, use the same compiler and
permission-filtered store query. Enhanced search defaults to issue IDs, honors
selected fields, rejects unbounded queries and offset pagination, and rejects a
cursor reused with another query, workspace, or user.

## Reference and query services

ZZIRA exposes the pinned GET/POST reference-data resources and returns only
fields, operators, and functions supported by its compiler, including typed
custom fields. Project, status/category, priority, issue-type, user, label,
component, sprint, resolution, and version suggestions are generated from the
current workspace. Suggestions derived from work items apply issue visibility
before collecting distinct values.

The parse resource returns one Jira-shaped structure or error list per input
query and supports strict, warning, and syntax-only validation. Bulk matching
compiles each query independently and evaluates it only against the requested,
visible issue IDs. Sanitization returns per-query errors without failing the
batch. Personal-data migration converts known workspace member email/display
operands on assignee, reporter, and creator equality clauses to account IDs.

## App function precomputations

Installed apps can list, page, filter, retrieve, and update only the durable
precomputations owned by their installation through the three Jira app JQL
function resources. Signed ZZIRA and Connect requests use the app's stable
non-human principal; ordinary users receive a forbidden response. Connect
keeps Atlassian's `READ` scope behavior for reads and recalculation updates.

Each invocation identity is stable across repeated use and tracks created,
updated, and last-used timestamps. Bulk recalculation replaces either the JQL
fragment or its user-facing error. Updates are atomic when missing IDs are not
skipped; the opt-in skip mode applies found updates and returns missing IDs.
ID search treats foreign-tenant and foreign-app records as missing.

## Current limits

The search and JQL service resources remain assessed as partial. The remaining
PR 1 work adds app-function invocation in the compiler, more built-in functions
and multi-value fields, complete personal-data migration for list/history
operands and unknown-user reporting; project-aware validation warnings; exact
expansion/property selection; strong-consistency reconciliation; and
snapshot/keyset semantics for pages whose matching work items change between
requests.

Some Jira history fields cannot be queried until their mutations persist a
structured diff. Unsupported functions fail during compilation instead of
being compared as literal text.
