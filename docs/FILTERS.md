# Saved filters

Saved filters store a JQL query with an owner, view and edit shares, favorites,
navigator columns and email subscriptions. The REST API and the browser use the
same workspace-scoped records. Part of the [Jira platform](JIRA_PLATFORM.md);
see [CLOUD_PARITY.md](CLOUD_PARITY.md) for status.

## API

All 19 Jira Cloud filter and filter-sharing operations under
`/rest/api/3/filter`:

- Create, read, update, delete; `filter/my`, `filter/favourite` and paginated
  `filter/search` (exact or substring name, owner, group, project, IDs,
  ordering, `expand`).
- `PUT`/`DELETE /filter/{id}/favourite` (`POST` is kept as an alias).
- `GET/PUT/DELETE /filter/{id}/columns`: columns are HTML form data naming
  navigable fields (400 otherwise, 403 for non-owners, 404 while unset);
  responses are `ColumnItem` lists.
- `PUT /filter/{id}/owner`: by the owner or a Jira administrator; the new
  owner must be an active member without a same-named filter.
- `GET/POST /filter/{id}/permission`, `GET/DELETE /filter/{id}/permission/{permissionId}`.
- `GET/PUT /filter/defaultShareScope`: per user; `GLOBAL` is normalized to
  `AUTHENTICATED`.
- `expand=subscriptions` and `expand=sharedUsers` fill those lists, with
  Jira's `[start:end]` ranges; unexpanded they are empty.

## Behavior

- Names are unique per owner, case-insensitively.
- Only queries the [JQL](JQL.md) compiler accepts can be saved.
- **Sharing.** View shares: authenticated users, a user, a group, a project or
  a project role. Edit shares grant updates without ownership. Group shares
  follow current membership; project shares reach people who can browse the
  project; role shares evaluate the role in that project. Private filters are
  visible only to their owner. Public (anonymous) sharing does not exist, as in
  Jira Cloud, so anonymous callers see no filters.
- Viewing a filter is required to favorite it or read its columns and
  permissions.
- **Audit.** Create, update, delete, share changes and owner transfer write
  organization audit events without the JQL.
- **Subscriptions.** Owners schedule email results daily, weekly (Monday) or
  on a cron expression, in an IANA time zone, to up to 50 active workspace
  members. A runner claims due runs (recovering stale claims), evaluates the
  JQL with each recipient's permissions, deduplicates per recipient through
  the outbox, retries delivery, and records result counts and errors. Emails
  link directly to work items.

## UI

- `/filters` lists visible filters with JQL, owner, favorite count and
  separate view and edit access. Owners edit details, JQL and columns, manage
  shares (everyone signed in, person, group, project, project role), transfer
  ownership, delete, and add or remove an email schedule showing next and last
  run. Each user sets whether new filters start private or shared with
  everyone signed in.
- Site administrators see private filters and can reassign ownership when an
  owner leaves. The administration page lists every filter subscription on
  the site and can delete any of them.
- The issue navigator saves the current query as a filter
  (`POST /issues/{projectKey}/filters`).

## Gaps

See [PLAN.md](../PLAN.md).

- Only the owner can subscribe; Jira lets anyone who can view a filter
  subscribe.
- Subscriptions cannot target a group, and the **Manage group filter
  subscriptions** global permission is defined but not enforced.
- Empty results are always emailed; Jira's "email this filter even if there
  are no work items" option (off by default) is missing.
- The **Create shared objects** global permission is not checked when
  sharing.

## See also

[JQL.md](JQL.md) · [DASHBOARDS.md](DASHBOARDS.md) ·
[AGILE_BOARDS.md](AGILE_BOARDS.md) · [PERMISSION_SCHEMES.md](PERMISSION_SCHEMES.md)
