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
  `colorFields`, `urlFields`, `priority`, `originalEstimateField` and
  `timeTrackingField`. Each item goes through the ordinary update command
  (validation, history, notifications, security, SLA reconciliation).
- **Notifications.** Delete, move, transition and edit accept
  `sendBulkNotification` (default true). In-app notifications still fire per
  event; email is collapsed into one bulk change email per recipient when the
  task finishes, sent at most once even on retry. With `false`, no email.

## Permissions

REST bulk operations need the global **Bulk change** permission
(`BULK_CHANGE`), granted on the global permissions page.

## UI

The issue navigator (`/issues/{projectKey}`) lets site administrators select
rows on the current page and bulk delete, move (project, type, parent) or
transition them, choosing whether to notify watchers. Progress is shown at
`/issues/{projectKey}/bulk/{taskId}`.

## Gaps

See [PLAN.md](../PLAN.md).

- `editedFieldsInput.issueType` and `status` are refused (pointing to bulk
  move and bulk transition) instead of being edited in place.
- The navigator offers no bulk edit, watch or unwatch, and gates bulk actions
  on site administration rather than the Bulk change permission.

## See also

[ISSUE_SURFACE.md](ISSUE_SURFACE.md) · [JQL.md](JQL.md) ·
[NOTIFICATION_SCHEMES.md](NOTIFICATION_SCHEMES.md) ·
[PERMISSION_SCHEMES.md](PERMISSION_SCHEMES.md)
