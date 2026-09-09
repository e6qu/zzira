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
  plus durable `currentLogin()` and `lastLogin()` boundaries for system and
  custom date-time fields and history predicates; Jira date literals, relative
  values such as `-5d`, explicit increments such as `startOfMonth(-1M)`, and
  natural-period increments such as `startOfMonth(-1)`;
- relation-backed `membersOf()`, `linkedIssues()`/`linkedWorkItems()` with
  multiple link types, `openSprints()`, `closedSprints()`, `futureSprints()`,
  `standardIssueTypes()`/`standardWorkTypes()`, and
  `subtaskIssueTypes()`/`subtaskWorkTypes()` list functions;
- release selectors `releasedVersions()`, `unreleasedVersions()`,
  `latestReleasedVersion()`, and `earliestUnreleasedVersion()` against the
  canonical project version registry;
- `watchedIssues()`/`watchedWorkItems()`, `votedIssues()`/`votedWorkItems()`,
  and date-bounded `updatedBy()` issue selectors;
- `projectsLeadByUser()`/`spacesLeadByUser()` and
  `projectsWhereUserHasRole()`/`spacesWhereUserHasRole()` project selectors;
- project-scoped component ID/name matching and
  `componentsLeadByUser([user])` against canonical component ownership;
- multi-value semantics for labels, components, version memberships, and current or
  historical sprint memberships, including Jira's empty-field behavior for
  negated comparisons;
- immutable `WAS`, `WAS IN`, `WAS NOT`, `WAS NOT IN`, and `CHANGED` evaluation
  for retained field diffs, including `FROM`, `TO`, `BY`, `BEFORE`, `AFTER`,
  and `DURING` predicates;
- as many as seven `ORDER BY` fields with an issue-ID tie-breaker for
  deterministic paging;
- immutable numeric Jira issue IDs at the REST boundary while retaining separate
  local-first sync identities; and
- legacy offset search plus enhanced search with durable seven-day result-order
  snapshots bound to the JQL, reconciliation set, workspace, and user.

The history compiler evaluates `actions`, ZZIRA's immutable ordered change log.
It compares both stored IDs and display values, so a status query can use either
the status ID or its name. Relative dates are resolved once per compilation in
UTC so all clauses in one execution share the same clock value.

Successful password, generic OIDC, Google, Microsoft, and Atlassian sign-ins
atomically advance a durable current/previous login pair. Logout and provider
back-channel revocation remove authentication sessions without erasing these
JQL boundaries. Existing sessions receive a deterministic current boundary
during upgrade; the next successful sign-in establishes an exact previous
boundary. Accounts that have never established the requested boundary produce
no match for comparisons against it.

## Search resources

Both methods of `/rest/api/3/search` and `/rest/api/3/search/jql`, plus
`POST /rest/api/3/search/approximate-count`, use the same compiler and
permission-filtered store query. Enhanced search defaults to issue IDs, honors
selected fields, rejects unbounded queries and offset pagination, and rejects a
cursor reused with another query, reconciliation set, workspace, or user. Each
continuation reads stored result positions, so inserts, edits, and reordering
after the first page cannot duplicate, skip, or inject matching issues. Current
visibility is checked again on every page.

Legacy and enhanced search accept repeated or comma-delimited field selectors,
`*all`/`*navigable`, exclusions, and custom-field IDs or installed-app keys.
`fieldsByKeys` controls installed-app output keys. The `names`, `schema`, and
`renderedFields` expansions follow the selected field set. Transition expansion
uses the executable workflow-rule evaluator; edit metadata, immutable changelog,
and issue-operation expansions reuse their issue-resource builders. Versioned
representations expose the selected current values under version `1` and replace
the normal `fields` object. Each issue can include as many as five requested
JSON properties. Request bodies reject
unknown fields and trailing JSON; unsupported expansion names, invalid boolean
options, archived-project requests before archive support exists, and
reconciliation lists larger than Jira's 50-ID limit return explicit errors.
Approximate count requires a bounded query like enhanced search.

Enhanced search accepts Jira numeric IDs through `reconcileIssues`. ZZIRA reads
the transactional PostgreSQL issue state instead of an eventually consistent
search replica, so requested IDs already participate in the query with strong
consistency and require no index-side repair. Numeric IDs also work anywhere an
issue ID or key is accepted and in the bulk JQL match resource; internal
`iss_*` identities remain stable for sync, actions, and database relations.

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

Installed Connect and native ZZIRA descriptors can also declare custom JQL
functions. The functions appear in workspace autocomplete. A clause such as
`issue in riskIssues("high")` creates or refreshes its installation-owned
precomputation and calls the app's signed relative endpoint on a cache miss.
The request includes the canonical field, field type, operator, function name,
arguments, and precomputation ID. Successful JQL fragments and opted-in app
errors are cached without user scoping and expire after seven days without use.

Returned fragments replace the complete function clause. They are parsed and
compiled through the ordinary JQL engine, and the final search still applies
workspace, user, and issue-security predicates. Fragments cannot add `ORDER
BY`; nested app functions are bounded to four levels. Invalid fragments,
unsupported field types/operators, invalid argument counts, duplicate function
names, and unavailable app endpoints produce query errors without treating app
text as SQL. The same expansion hook is used by REST search and match, the
browser navigator, boards and quick filters, dashboard gadgets, service queues
and SLA goals, automation execution, and webhook filtering.

## Current limits

The search and JQL service resources remain assessed as partial. The remaining
PR 1 work adds the remaining built-in functions and multi-value fields,
complete personal-data migration for list/history
operands and unknown-user reporting; project-aware validation warnings; exact
historical versioned representations and richer rendered values.

The remaining built-in catalog includes permission-scheme,
customer/organization, approval, and SLA functions. Those
functions depend on their owning PR 1/JSM state models and are implemented with
those models instead of returning approximate results.

Some Jira history fields cannot be queried until their mutations persist a
structured diff. Unsupported functions fail during compilation instead of
being compared as literal text.
