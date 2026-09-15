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
The API validates built-in and custom event IDs, recipient parameters, active users,
workspace groups, project roles, custom fields, email addresses, pagination,
expansions, assignment conflicts, and deletion safety. Scheme, rule, and
assignment mutations write immutable actions in the same transaction.

## Events, recipients, and delivery

The event registry contains Jira's 17 built-in issue events, numbered as Jira's
EventType ids (1 created through 17 comment deleted, with 13 the generic event), and the site's
custom events. Administrators add, rename and delete custom events in the
Events section of site administration. Custom events take ids from 10000 up,
and one a notification scheme or workflow transition uses cannot be deleted.
`GET /rest/api/3/events` lists both kinds. A workflow transition's
`customIssueEventId`, set through the workflow APIs or the transition editor's
"Fire event" choice, fires that event instead of the one the status change
implies. Rules support the
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

Deleting a work item fires Issue deleted (8) while its watchers and security
level still resolve, and a move fires Issue moved (9) for the item and each
moved sub-task.

Each person chooses two notification preferences in the Email notifications
section of their profile, stored under Jira's preference keys and readable
through `/rest/api/3/mypreferences`. *My changes* (`user.notify.own.changes`,
default "Do not notify me") decides whether implicit recipients such as
reporter, assignee and watchers include the person who made the change.
*Autowatch* (`user.autowatch.disabled`, default enabled) makes a member a
watcher of work items they create or comment on, unless the site turns
watching off.

Mentioning someone tells them, whatever the notification scheme says. A
description or comment whose ADF names a person in a `mention` node notifies
that person the first time the mention appears, with an inbox item and an
email; keeping a mention while editing tells nobody again, and mentioning
yourself notifies nobody. The person must be an active member who can browse
the work item at its security level and, for a restricted comment, belong to
its group or project role. Typing @ in the issue page's comment editor offers
the project's people and inserts the mention.

Work item notification and mention emails are HTML with a plain-text
alternative (`multipart/alternative`, quoted-printable, RFC 2047 subjects).
The HTML names who did what, the project, the linked key and summary, the
status, a *View work item* button and a link to notification preferences.
The outbox stores site-relative links, and the mailer makes them absolute with
`BASE_URL` when it sends. Administrators check a decision with the notification helper
(`/admin/notification-helper`, linked from the Events section of
administration). Given a person, a work item and an event, it names the
project's scheme, the rules that name the person, whether a Current user rule
also notifies whoever makes the change, and whether the person is an active
member, can browse the project and can see the work item's security level. The `user`, `group`, `projectRole`, `field`
and `all` expansions add each recipient's details. Jira's deprecated
direct email-address recipient is stored and delivered, but it does not create
an in-app identity. Exact self links on every paged response and all Jira error
wording also remain under contract review.
