# Jira notification schemes

Updated: 2026-09-10

ZZIRA stores reusable Jira notification schemes, event recipients, and the
scheme assigned to every project. Site administrators manage the catalog at
`/settings/notification-schemes`. Project administrators inspect the effective
scheme at `/projects/{key}/settings/notifications`.

## Jira Cloud REST surface

This checkpoint implements all nine pinned Jira Cloud notification-scheme
operations:

| Method and path | Behavior |
|---|---|
| `GET/POST /rest/api/3/notificationscheme` | Pages and filters schemes, or creates a scheme with optional event recipients. |
| `GET /rest/api/3/notificationscheme/project` | Pages project-to-scheme mappings with project and scheme filters. |
| `GET/PUT /rest/api/3/notificationscheme/{id}` | Reads an optionally expanded scheme or updates its name and description. |
| `PUT /rest/api/3/notificationscheme/{id}/notification` | Atomically adds event-recipient rules. |
| `DELETE /rest/api/3/notificationscheme/{notificationSchemeId}/notification/{notificationId}` | Removes one event-recipient rule. |
| `DELETE /rest/api/3/notificationscheme/{notificationSchemeId}` | Deletes an unused non-default scheme. |
| `GET /rest/api/3/project/{projectKeyOrId}/notificationscheme` | Returns the project's effective scheme to a project or site administrator. |

Names are unique without regard to case. IDs are Jira-style numeric values.
The API validates built-in event IDs, recipient parameters, active users,
workspace groups, project roles, custom fields, email addresses, pagination,
expansions, assignment conflicts, and deletion safety. Scheme, rule, and
assignment mutations write immutable actions in the same transaction.

## Events, recipients, and delivery

The event registry contains Jira's 17 built-in issue events. Rules support the
twelve Jira recipient types: current assignee, reporter, current user, project
lead, component lead, a named user, group, project role, email address, all
watchers, user custom field, and group custom field.

Issue creation, ordinary updates, assignment changes, comments, resolutions,
reopens, and other workflow transitions now fire their corresponding scheme
event from the shared command layer. A delivery transaction resolves the final
issue state, collapses duplicate recipients, and verifies each user against
`BROWSE_PROJECTS` and the issue security level before writing anything. It then
creates a private synchronized inbox item, its immutable action, and a durable
email-outbox row. The source action sequence and event ID make replay
idempotent. Email uses the existing leased, retryable SMTP runner; rows remain
durable when SMTP is intentionally disabled.

Every workspace receives a default scheme and every existing or future project
receives an assignment. The default scheme covers create, update, assignment,
resolve, close, comment, reopen, and generic events for assignee, reporter, and
watchers. Implicit roles suppress the actor's own changes; an explicit
`CurrentUser` recipient enables those notifications.

## Evidence and current boundary

- `internal/api3/notification_schemes_test.go` covers all nine operations,
  validation, filtering, assignment conflicts, event delivery, email outbox,
  immutable actions, deduplication state, and issue-security suppression.
- `e2e/notification_schemes.spec.ts` covers site configuration, a named user
  recipient, project assignment and inspection, modal issue creation, recipient
  inbox delivery, 320 px reflow, reassignment, and cleanup.
- `migrations/131_notification_schemes.sql` is exercised from a clean
  PostgreSQL schema. Both settings pages are included in the light and dark axe
  sweep.

The assessment remains partial while custom event administration, per-user
email preferences, mentions, notification diagnostics, event firing for issue
delete/move and worklog/comment edit/delete mutations, rich email rendering,
and every nested user/group/role/field expansion bean remain. Jira's deprecated
direct email-address recipient is stored and delivered, but it does not create
an in-app identity. Exact self links on every paged response and all Jira error
wording also remain under contract review.
