# Notification schemes

Jira notification schemes, their event recipients, the scheme assigned to each project, issue events, mentions, and the delivery of in-app and email notifications. Site administrators manage schemes at `/settings/notification-schemes` and custom events under `/admin` › Events. Project administrators view a project's scheme at `/projects/{key}/settings/notifications`. Part of the [Jira platform](JIRA_PLATFORM.md). For status, see [CLOUD_PARITY.md](CLOUD_PARITY.md).

## API

All nine operations of Jira's notification scheme group, plus the event list:

| Method and path | Behavior |
| --- | --- |
| `GET/POST /rest/api/3/notificationscheme` | Pages and filters schemes, or creates one with optional recipients. |
| `GET /rest/api/3/notificationscheme/project` | Pages project-to-scheme mappings, filtered by project and scheme. |
| `GET/PUT /rest/api/3/notificationscheme/{id}` | Reads a scheme (expandable) or updates name and description. |
| `PUT /rest/api/3/notificationscheme/{id}/notification` | Adds event recipients atomically. |
| `DELETE /rest/api/3/notificationscheme/{notificationSchemeId}/notification/{notificationId}` | Removes one recipient. |
| `DELETE /rest/api/3/notificationscheme/{notificationSchemeId}` | Deletes a scheme that is neither the default nor assigned. |
| `GET /rest/api/3/project/{projectKeyOrId}/notificationscheme` | The project's scheme, for project or site administrators. |
| `GET /rest/api/3/events` | Built-in and custom issue events. |

- Names are unique ignoring case; ids are numeric.
- Event ids, recipient parameters, users, groups, project roles, custom fields, email addresses, paging, expansions, assignment conflicts and deletes are validated.
- The `user`, `group`, `projectRole`, `field` and `all` expansions add recipient details.
- Every change writes an action in the same transaction.

## Events

- **Built-in events:** Jira's 17, with Jira's ids: 1 created, 2 updated, 3 assigned, 4 resolved, 5 closed, 6 commented, 7 reopened, 8 deleted, 9 moved, 10 work logged, 11 work started, 12 work stopped, 13 generic, 14 comment edited, 15 worklog updated, 16 worklog deleted, 17 comment deleted.
- **Custom events:** ids from 10000. Administrators add, rename and delete them; an event used by a scheme or a workflow transition cannot be deleted.
- **What fires an event:** every mutation Jira fires an event for fires it here. A transition fires the event its status change implies, or the event its `customIssueEventId` names (set through the workflow APIs or the transition editor's "Fire event"). That is how 11, 12 and 13 fire.
- **Deletion and moves:** deleting a work item fires 8 with recipients resolved inside the deleting transaction, while the item, its watchers and its security level still exist. A move fires 9 for the item and each moved sub-task.

## Recipients

The twelve Jira recipient types: current assignee, reporter, current user, project lead, component lead, user, group, project role, email address, all watchers, user custom field, group custom field.

- **Default scheme:** every workspace has one, and every project is assigned one. It notifies assignee, reporter and watchers of create, update, assign, resolve, close, comment, reopen and generic events.
- **The person who made the change** is left out of implicit recipients unless the scheme has a Current user recipient or their *My changes* preference says otherwise.
- **Email address recipients** (deprecated in Jira) are stored and emailed but create no in-app identity.

**Personal preferences** (profile › Email notifications, also `/rest/api/3/mypreferences`; see [PEOPLE.md](PEOPLE.md)):
- *My changes* (`user.notify.own.changes`, default "Do not notify me"): whether implicit recipients include the person who made the change.
- *Autowatch* (`user.autowatch.disabled`, default on): makes a member a watcher of work items they create or comment on, unless the site turns watching off ([JIRA_SITE_CONFIGURATION.md](JIRA_SITE_CONFIGURATION.md)).

## Mentions

A `mention` node in a description or comment notifies that person (in-app and email) the first time it appears, whatever the scheme says.
- Keeping a mention while editing notifies nobody again. Mentioning yourself notifies nobody.
- The person must be an active member who can browse the work item at its security level and, for a restricted comment, belong to its group or role.
- Typing `@` in the comment editor offers the project's people.

## Delivery

- **Per event,** one transaction resolves the final work item state, removes duplicate recipients, and checks each user against Browse projects and the issue security level ([ISSUE_SECURITY_SCHEMES.md](ISSUE_SECURITY_SCHEMES.md)). It then writes a private synced inbox item, its action, and an email outbox row.
- **Replay is idempotent:** the source action sequence and event id identify each delivery.
- **Email** goes through the leased, retrying SMTP outbox ([ADMIN.md](ADMIN.md)). Rows stay queued when SMTP is off.
- **Message format:** `multipart/alternative` HTML plus plain text, quoted-printable, RFC 2047 subjects. The HTML says who did what, the project, the linked key and summary, the status, a *View work item* button and a link to notification preferences.
- **Links:** the outbox stores site-relative links; the mailer makes them absolute with `BASE_URL`.

**Notification helper** (`/admin/notification-helper`). Given a person, a work item and an event, it names the project's scheme and the rules that name the person. It says whether a Current user rule also notifies the actor, and whether the person is an active member, can browse the project, and can see the work item's security level.

## Gaps

Tracked in [PLAN.md](../PLAN.md).

- No copy action for notification schemes in the UI.

## Tests

- `internal/api3/notification_schemes_test.go`: all nine operations, validation, filtering, assignment conflicts, delivery, email outbox, actions, deduplication, issue security suppression.
- `e2e/notification_schemes.spec.ts`: site configuration, a named user recipient, project assignment, creating a work item, inbox delivery, 320px reflow. Both settings pages are in the light and dark axe sweep.
