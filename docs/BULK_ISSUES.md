# Bulk work-item operations

Jira Cloud's bulk routes delete, move, transition, edit, watch and unwatch up to
1,000 work items as one durable task. Each task rechecks access per work item,
runs through the same command as the single-item operation, and reports
per-item results. Part of the [Jira platform](JIRA_PLATFORM.md); see
[CLOUD_PARITY.md](CLOUD_PARITY.md) for status.

## API

| Route | Behavior |
|---|---|
| `POST /rest/api/3/bulk/issues/delete` | Queue deletion |
| `POST /rest/api/3/bulk/issues/move` | Queue project, work type and parent moves (`targetToSourcesMapping`) |
| `GET /rest/api/3/bulk/issues/transition` | Transitions common to the selection, grouped by workflow, cursor-paged |
| `POST /rest/api/3/bulk/issues/transition` | Queue one or more transition groups |
| `GET /rest/api/3/bulk/issues/fields` | Fields shared by the selection, with field search and 50-item cursor pages |
| `POST /rest/api/3/bulk/issues/fields` | Queue field edits |
| `POST /rest/api/3/bulk/issues/watch` | Queue self-watch |
| `POST /rest/api/3/bulk/issues/unwatch` | Queue self-unwatch |
| `GET /rest/api/3/bulk/queue/{taskId}` | Submitter, timestamps, state, progress and terminal counts |

## Behavior

- **Selection.** 1 to 1,000 unique, visible work item IDs or keys.
- **Admission.** At most five bulk tasks queued or running per workspace
  (`ErrBulkTaskLimit`). Admission is serialized; execution is durable and
  replay-safe through per-item markers, so a recovered worker neither repeats
  changes nor duplicates history.
- **Retention.** Task progress is readable for 14 days after submission.
- **Watch / unwatch.** Watcher state, sync actions and the task result commit
  in one transaction; repeated requests are idempotent.
- **Delete.** Uses the permission-checked delete command. Metadata, the
  action and attachment cleanup intents commit together; blob cleanup is
  retried by a leased worker (see [ATTACHMENTS.md](ATTACHMENTS.md)).
- **Move.** Resolves destination projects, work types, sub-task parents and
  status maps before queueing. Execution picks a destination-workflow status,
  rekeys atomically and keeps every former key as an alias. Project-bound
  versions and components, and incompatible security levels, are cleared
  across projects. Every moved work item fires the Issue moved event.
  - `inferClassificationDefaults`: an unclassified item takes the destination
    project's default level; a classified one keeps its level. Otherwise
    `targetClassification` must map every source level to a published level,
    or the item fails. See [CLASSIFICATION_LEVELS.md](CLASSIFICATION_LEVELS.md).
  - `inferFieldDefaults`: items keep values for fields the destination
    requires; an item without one fails. Otherwise `targetMandatoryFields`
    supplies values (raw lists or ADF); existing values are kept unless
    `retain` is false.
  - Sub-tasks move with their parent. A sub-task keeps its type if the
    destination offers it; otherwise `inferSubtaskTypeDefault` picks one, and
    without it the parent fails.
- **Transition.** Discovery evaluates each item's workflow, status-history and
  hierarchy conditions, intersects transitions per workflow, and omits
  transitions whose screen needs input. Submission revalidates every pair; the
  worker uses the REST transition command, so conditions, validators and
  post-functions apply as for one item.
- **Edit.** Discovery intersects create/edit metadata across the selected
  projects and uses the same option IDs as the metadata APIs.
  `selectedActions` must match the edited field IDs exactly (1 to 200).
  Supported `editedFieldsInput` families: `singleLineTextFields`,
  `clearableNumberFields`, `dateTimePickerFields`, `datePickerFields`,
  `richTextFields`, `singleSelectClearableUserPickerFields`,
  `multipleSelectClearableUserPickerFields`, `singleSelectFields`,
  `multipleSelectFields`, `cascadingSelectFields`, `singleGroupPickerFields`,
  `multipleGroupPickerFields`, `singleVersionPickerFields`,
  `multipleVersionPickerFields`, `multiselectComponents`, `labelsFields`,
  `colorFields`, `urlFields`, `priority`, `originalEstimateField`,
  `timeTrackingField`, `issueType` and `status`. Each item goes through the
  ordinary update command (validation, history, notifications, security, SLA
  reconciliation).
  - `issueType` and `status` are not values on a screen, so they are not on
    the editable-field list; the ids a client names them by are resolved at
    submission, and a status must name the same status in every project the
    selection reaches.
  - An item's type changes the way a move within its own project does: the
    same status mapping, required fields and Move issues permission, and it
    fires the Issue moved event.
  - An item's status is reached by running the transition that leads there,
    so conditions, validators and post-functions apply. A work item whose
    workflow offers no such transition from where it stands fails and says
    so, as does one that could only get there through a transition screen.
  - Within one item the type is applied first, then the fields, then the
    status, so the item ends where the edit asked rather than where its new
    type's workflow put it.
- **Notifications.** Delete, move, transition and edit accept
  `sendBulkNotification` (default true). In-app notifications still fire per
  event; email is collapsed into one bulk change email per recipient when the
  task finishes, sent at most once even on retry. With `false`, no email.

## Permissions

Every bulk operation, in REST and in the browser, needs the global **Bulk
change** permission (`BULK_CHANGE`), granted on the global permissions page
([PERMISSION_SCHEMES.md](PERMISSION_SCHEMES.md)). Without it the REST routes
and the navigator's bulk forms answer 403.

Bulk change admits the bulk routes; it is not a way around project
permissions. Each item goes through the same command as the single-item
operation, so the submitter also needs, per item and per project:

| Operation | Needs |
|---|---|
| Delete | Delete issues |
| Move | Move issues on the source item, and Create issues in the destination project |
| Transition | Transition issues, plus whatever the transition itself needs |
| Edit | Edit issues, and the permission each edited field needs (Assign issues, Schedule issues, Resolve issues, Set issue security) |

Jira Service Management's queue actions are a different surface: they act on
one desk's requests, need agent access rather than Bulk change, and are
described in [SERVICE_MANAGEMENT.md](SERVICE_MANAGEMENT.md#queues).

## UI

The issue navigator (`/issues/{projectKey}`) shows its selection checkboxes and
bulk forms only to people holding Bulk change. They select rows on the current
page and bulk delete, move (project, type, parent) or transition them, choosing
whether to notify watchers. "Edit selected" ticks the fields to change and
changes every ticked one in a single task: the assignee, the priority, the due
date, labels, components and fix versions. A list field replaces what is
there, adds to it or takes values out of it; an empty box clears the field,
except for a priority, which Jira has no unset value for. Watching and unwatching the selection ask only to
be able to see the work items, as watching one does, so they are offered
without Bulk change. Progress is shown at
`/issues/{projectKey}/bulk/{taskId}` to the submitter and administrators.

## Gaps

See [PLAN.md](../PLAN.md).

- The navigator's bulk edit offers the six built-in fields above and the
  custom fields every work type in the project shares: text, a number, a date,
  a date and time, a URL, a single or multiple select, a cascading select, a
  person, a group, a team, a project, one or several versions, and a list of
  values. Assets fields are REST-only, as are work type and status, which the
  navigator changes through bulk move and bulk transition instead.

## In the navigator

**Edit selected** offers a field per fieldset: tick a field and fill its box.
The custom fields are the ones shared by every work type in the project,
because the selection is whatever is ticked -- a field only some types carry
would fail for the rest. The custom fields are fetched when the
editor is opened, not with the page around it: reading them means reading the
whole site's create metadata, and a page of work items should not pay for an
editor nobody opened. What a field is, and whether it may be set at all, is
read from the project when the edit is submitted rather than taken from the
form, so a form naming another field changes nothing. An emptied box clears
the field, as the single-item editor does; a date and time is read as UTC.

## See also

[ISSUE_SURFACE.md](ISSUE_SURFACE.md) · [JQL.md](JQL.md) ·
[NOTIFICATION_SCHEMES.md](NOTIFICATION_SCHEMES.md) ·
[PERMISSION_SCHEMES.md](PERMISSION_SCHEMES.md)
