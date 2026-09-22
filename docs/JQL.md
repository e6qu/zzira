# JQL and work-item search

One JQL parser and PostgreSQL compiler (`internal/jql`) serves the REST search
resources, the issue navigator, saved filters, board and quick filters, service
queues and SLA goals, dashboard gadgets, webhooks and automation. Callers add
workspace and issue-security predicates outside the compiled expression, so no
page or aggregate includes a work item the viewer cannot browse. Part of the
[Jira platform](JIRA_PLATFORM.md); see [CLOUD_PARITY.md](CLOUD_PARITY.md) for
status.

## API

| Route | Behavior |
|---|---|
| `GET/POST /rest/api/3/search` | Legacy offset search |
| `GET/POST /rest/api/3/search/jql` | Enhanced (cursor) search |
| `POST /rest/api/3/search/approximate-count` | Count for a bounded query |
| `GET/POST /rest/api/3/jql/autocompletedata` | Fields, operators and functions the compiler supports |
| `GET /rest/api/3/jql/autocompletedata/suggestions` | Value suggestions |
| `POST /rest/api/3/jql/parse` | Parse and validate queries |
| `POST /rest/api/3/jql/match` | Match queries against given work item IDs |
| `POST /rest/api/3/jql/sanitize` | Rewrite queries for a viewer (administrators only) |
| `POST /rest/api/3/jql/pdcleaner` | Convert people references to account IDs |
| `GET/POST /rest/api/3/jql/function/computation`, `POST …/computation/search` | App function precomputations (apps only) |

## Language

- `AND`, `OR`, `NOT`, parentheses and bare-text search. A bare term is the
  `text` field written without naming it, so it searches the work item's own
  text and the text of its comments.
- Operators `=`, `!=`, `~`, `!~`, `>`, `>=`, `<`, `<=`, `IN`, `NOT IN`,
  `IS EMPTY`, `IS NOT EMPTY`.
- Fields: `key`/`issue`/`workItem`, `id`, `summary`, `description`,
  `environment`, `project`/`space`, `issuetype`/`workType`, `status`,
  `statusCategory`, `priority`, `assignee`, `reporter`, `creator`, `labels`,
  `fixVersion`, `affectedVersion`, `component(s)`, `sprint`, `parent`,
  `resolution`, `resolved`/`resolutionDate`, `created`, `updated`,
  `due`/`dueDate`, `originalEstimate`/`timeOriginalEstimate`,
  `remainingEstimate`/`timeEstimate`, `timeSpent`, `workRatio`, `approvals`,
  `text`, `comment`, `watcher(s)`, `voter(s)`, `votes`, `attachments`,
  `issueLinkType`, `level`, `category`, `hierarchyLevel`,
  `statusCategoryChangedDate`, `lastViewed`, `filter` (and its aliases
  `request`, `savedFilter` and `searchRequest`),
  `"Request participants"`, `request-channel-type`, `"Request Type"`, service
  SLA fields,
  typed custom fields (`cf[N]`, id or name), app field aliases and indexed
  entity properties. `issueKey` and `type` are Jira's aliases for `key` and
  `issuetype`.
- A saved filter is a query a query may name: `filter = "Open work"` matches
  what that filter matches, by name or by either of its ids, and the filter's
  own query is compiled where it is named. A filter the searcher may not see
  is refused the way one that does not exist is, a filter that leads back to
  itself is refused rather than followed, and a chain may lead through ten.
- Fields that are things attached to the work rather than values on it ask
  what Jira lets them ask: `text` and `comment` take `~` and `!~` alone --
  `text` searches the work item's own text and its comments -- `attachments`
  takes only `IS EMPTY` and `IS NOT EMPTY`, and `watcher`, `voter` and
  `issueLinkType` take the equality and list operators. `issueLinkType`
  matches a link type by its name or by either direction's wording.
- Project clauses match key (any case), numeric ID or name.
- Multi-value fields (labels, components, versions, current and past sprints)
  follow Jira's empty-field behavior for negated comparisons.
- History: `WAS`, `WAS IN`, `WAS NOT`, `WAS NOT IN` and `CHANGED` with `FROM`,
  `TO`, `BY`, `BEFORE`, `AFTER` and `DURING`, for `status`, `assignee`,
  `reporter`, `priority`, `parent`, `labels`, `summary`, `description`,
  `fixVersion`, `affectedVersion` and `resolution`, which is every field
  Jira's own history search covers. History reads the `actions` log and
  compares both stored IDs and display names; `resolution WAS Unresolved`
  matches having had no resolution, which the log records as an empty value.
- Dates: Jira date literals, relative values (`-5d`) and date functions with
  increments (`startOfMonth(-1M)`, `startOfMonth(-1)`). Relative dates resolve
  once per compilation in UTC.
- `ORDER BY` up to seven fields, with an issue-ID tie-breaker.
- An unsupported function fails compilation; it is never compared as text.

### Built-in functions

The catalog is `jqlFunctions` in `internal/api3/api3_jql.go`.

| Kind | Functions |
|---|---|
| Date | `now()`, `startOfDay/Week/Month/Year()`, `endOfDay/Week/Month/Year()`, `currentLogin()`, `lastLogin()` |
| User | `currentUser()`, `membersOf()` |
| Work item | `linkedIssues()`/`linkedWorkItems()` (optional link types), `watchedIssues()`/`watchedWorkItems()`, `votedIssues()`/`votedWorkItems()`, `issueHistory()`/`workItemHistory()` (what you have opened), `issuesWithRemoteLinksByGlobalId()`/`workItemsWithRemoteLinksByGlobalId()` (1 to 100 global ids), `updatedBy()` (optional date range) |
| Sprint | `openSprints()`, `closedSprints()`, `futureSprints()` |
| Work type | `standardIssueTypes()`/`standardWorkTypes()`, `subtaskIssueTypes()`/`subtaskWorkTypes()` |
| Version | `releasedVersions()`, `unreleasedVersions()`, `latestReleasedVersion()`, `earliestUnreleasedVersion()` |
| Project | `projectsLeadByUser()`/`spacesLeadByUser()`, `projectsWhereUserHasRole()`/`spacesWhereUserHasRole()`, `projectsWhereUserHasPermission()`/`spacesWhereUserHasPermission()` (evaluated through the permission schemes) |
| Component | `componentsLeadByUser()` |
| Custom field | `cascadeOption()` (cascading selects) |
| Approvals (JSM) | `approved()`, `approver()`, `myApproval()`, `myPendingApproval()`, `myPending()`, `pending()`, `pendingApprovalBy()`, `pendingBy()` |
| SLA (JSM) | `breached()`, `completed()`, `everBreached()`, `paused()`, `remaining()`, `running()`, `withinCalendarHours()` |

`currentLogin()` and `lastLogin()` use a current/previous login pair advanced
atomically on every successful password, OIDC, Google, Microsoft or Atlassian
sign-in. Logout does not erase it. An account without the requested boundary
matches nothing.

## Search

- Legacy and enhanced search share the compiler and the permission-filtered
  store query.
- Enhanced search returns IDs by default, rejects unbounded queries and offset
  paging, and keeps a seven-day result-order snapshot bound to the JQL,
  `reconcileIssues`, workspace and user. A cursor reused with any of those
  changed is refused. Continuations read stored positions (no duplicates or
  skips after edits) and recheck visibility on every page.
- `reconcileIssues` (at most 50 IDs) is honored trivially: search reads the
  transactional store, not a replica.
- Field selection: repeated or comma-separated `fields`, `*all`,
  `*navigable`, exclusions, custom field IDs and app keys; `fieldsByKeys`;
  up to five `properties`.
- Expansions: `names`, `schema`, `renderedFields`, `transitions`,
  `editmeta`, `changelog`, `operations` and `versionedRepresentations`
  (current values under version `1`).
- Bodies reject unknown fields and trailing JSON; unknown expansions and
  invalid booleans are 400.
- Numeric Jira issue IDs work wherever an ID or key is accepted; internal
  `iss_*` IDs stay stable for sync.
- **Values:** a clause naming a status, priority, resolution or work type that
  the site does not have answers Jira's `The value 'X' does not exist for the
  field 'Y'.` -- as an error under `strict`, as a warning under `warn`, where
  the clause then matches nothing. `Unresolved` stays the absence of a
  resolution, and `EMPTY` is not a value.
- `validateQuery`: `strict` (or `true`) answers 400 with every error; `warn`
  (or `false`) runs the query with failing clauses matching nothing, skips
  unsortable ordering and returns `warningMessages`; `none` does the same
  silently. Malformed JQL is 400 in every mode.

## Reference data and query services

- **Autocomplete data** lists only what the compiler supports. Custom fields
  carry `cfid`, their value type and their searcher's operators; `value` is
  the field name while unique, else `cf[N]`. `POST` keeps custom fields whose
  contexts apply to `projectIds` (invalid IDs ignored, at most 1,000).
  `includeCollapsedFields` adds entries such as `"Component[Dropdown]"`; a
  collapsed name matches if any member field does, and a negative condition
  must hold for all.
- **Suggestions** come from the current workspace (projects, statuses,
  priorities, types, users, labels, components, sprints, resolutions,
  versions and custom field values by `cf[N]`, id, name or collapsed name).
  `predicateName` `by` suggests people; `from`/`to` suggest field values.
  Values derived from work items respect issue visibility.
- **Parse** returns a structure or errors per query: `strict` lists errors
  without the structure, `warn` lists them as warnings beside it, `none` only
  parses.
- **Match** compiles each query and evaluates it only against the requested,
  visible IDs.
- **Sanitize** rewrites each query for its viewer (anonymous when `accountId`
  is null): projects, components and versions the viewer cannot browse become
  IDs; custom fields in none of the viewer's projects become `cf[N]`; a name
  standing for several IDs becomes a list (`=` becomes `in`). Unparsable
  queries and unknown accounts report per-query errors.
- **PD cleaner** converts emails and unique display names to account IDs in
  user fields, user custom fields, `IN` lists and history operands. Unknown
  people become `unknown` and are listed in `queriesWithUnknownUsers`; an
  unparsable query fails the request.

## App functions

Connect and native app descriptors can declare JQL functions, shown in
autocomplete. A clause such as `issue in riskIssues("high")` creates or
refreshes an installation-owned precomputation and, on a cache miss, calls the
app's signed endpoint with the field, field type, operator, function name,
arguments and precomputation ID.

- Returned fragments replace the whole clause and go through the ordinary
  compiler; workspace, user and security predicates still apply. Fragments
  cannot add `ORDER BY`; nesting stops at four levels.
- Invalid fragments, unsupported types or operators, wrong argument counts,
  duplicate or built-in names and unavailable endpoints are query errors.
- Fragments and opted-in app errors are cached (not per user) and expire after
  seven days unused.
- Apps list, page, filter, read and update only their own precomputations
  through the computation resources. Updates are atomic unless the opt-in
  skip mode is used, which applies found updates and returns missing IDs.
  Connect keeps Atlassian's `READ` scope; ordinary users get 403.

The same expansion runs for REST search and match, the navigator, boards and
quick filters, gadgets, service queues and SLA goals, automation and webhooks.

## Entity property search

Apps index issue properties through a Connect `jiraEntityProperties` module
(descriptor or dynamic). Each extraction names a property key, a dotted
`objectName` path and a type:

| Type | Operators |
| --- | --- |
| `number` | `=`, `!=`, `>`, `>=`, `<`, `<=`, `IN`, `NOT IN`, `IS EMPTY`, `IS NOT EMPTY` |
| `date` | as `number`, with relative dates and date functions |
| `string` | `=`, `!=`, `IN`, `NOT IN`, `IS EMPTY`, `IS NOT EMPTY` |
| `user` | as `string`, and `currentUser()` |
| `text` | `~`, `!~`, `IS EMPTY`, `IS NOT EMPTY` |

- Query as `issue.property[key].path` or by the extraction's `alias`; an alias
  that collides with a system or custom field name is ignored.
- An array value matches if any element does. Non-numeric or non-date values
  never match number or date clauses and never error.
- `!=`, `NOT IN` and `!~` skip work items without the value. Indexed values
  can order results.
- A path no active app indexes is an unknown field; a suspended app's indexes
  stop answering. Autocomplete data lists every indexed path and alias.

## UI

The issue navigator at `/issues/{projectKey}` has basic (`?mode=basic`) and
advanced (`?mode=advanced`) modes and saves queries as
[filters](FILTERS.md).

## Gaps

See [PLAN.md](../PLAN.md).

- JSM fields not searchable: `Organizations`, because a request is not shared
  with an organization here; only a desk is.
- `versionedRepresentations` carry only the current value, not field history.
- Value validation covers status, priority, resolution and work type (by name
  or id). A project, component, version, user or custom field option that does
  not exist still matches nothing rather than reporting it.

## See also

[FILTERS.md](FILTERS.md) · [BULK_ISSUES.md](BULK_ISSUES.md) ·
[AGILE_BOARDS.md](AGILE_BOARDS.md) · [SERVICE_MANAGEMENT.md](SERVICE_MANAGEMENT.md) ·
[APPS.md](APPS.md) · [TIME_TRACKING.md](TIME_TRACKING.md) ·
[ISSUE_SECURITY_SCHEMES.md](ISSUE_SECURITY_SCHEMES.md)
