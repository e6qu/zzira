# Jira bulk work-item operations

ZZIRA exposes the Jira Cloud bulk work-item routes under the ordinary site base
URL. The current delivered slice includes:

| Route | Behavior |
|---|---|
| `POST /rest/api/3/bulk/issues/delete` | Queue deletion for up to 1,000 selected work items with execution-time access checks and per-item results |
| `POST /rest/api/3/bulk/issues/move` | Queue project, issue-type and parent moves with status, classification and required-field mappings, sub-tasks moving with their parent, key aliases and per-item results |
| `GET /rest/api/3/bulk/issues/transition` | Group common, screenless transitions by workflow for up to 1,000 selected work items, with cursor paging |
| `POST /rest/api/3/bulk/issues/transition` | Queue one or more validated transition groups and report per-item execution outcomes |
| `GET /rest/api/3/bulk/issues/fields` | Discover the fields shared by the selected work items, with field search and 50-item cursor pages |
| `POST /rest/api/3/bulk/issues/fields` | Queue validated edits for the selected work items and report per-item success, access loss or field failure |
| `POST /rest/api/3/bulk/issues/watch` | Queue self-subscription for the selected work items |
| `POST /rest/api/3/bulk/issues/unwatch` | Queue self-unsubscription for the selected work items |
| `GET /rest/api/3/bulk/queue/{taskId}` | Read submission identity, timestamps, state, progress and terminal counts |

Submissions contain between one and 1,000 unique visible issue IDs or keys.
ZZIRA admits at most five queued or running bulk work-item operations per
workspace. Admission is serialized, execution is durable, and a worker rechecks
visibility before changing each work item. A watch or unwatch batch commits its
watcher state, ordinary synchronization actions and terminal task result in one
transaction, so cancellation or failure cannot expose a partially completed
batch. Repeated requests remain idempotent.

Bulk deletion uses the ordinary permission-checked issue command. Each issue's
metadata deletion, immutable action and attachment cleanup intents commit in one
transaction. Blob cleanup then runs immediately and through a leased retry
worker, so an object-store outage cannot orphan an attachment permanently or
roll back an already committed issue deletion. A recovered bulk task rebuilds
its prior successes from the atomic delete actions instead of emitting duplicate
history. Administrators can select the current navigator page, choose watcher
notification intent, submit the operation, and follow its durable progress in
the browser.

Bulk move accepts Jira's `targetToSourcesMapping` shape and resolves destination
projects, issue types, explicit sub-task parents and status maps before queueing.
Execution rechecks source visibility, selects a destination-workflow status,
changes project keys atomically, and keeps every former key as an issue alias.
Project-bound version/component values and incompatible security levels are
cleared when crossing projects. A transactional task-item marker makes worker
replay idempotent. The navigator exposes project, type and parent controls and
uses the common progress page.

Each target mapping follows Jira's inference switches:

- **Classification.** With `inferClassificationDefaults`, a work item without
  a classification takes the destination project's default level, and one
  with a level keeps it. Otherwise `targetClassification` must map every
  source level to a published level, or that item fails.
- **Required fields.** With `inferFieldDefaults`, work items keep their
  values for fields the destination's field configuration requires, and one
  without a value fails. Otherwise `targetMandatoryFields` supplies raw value
  lists or ADF documents for required custom fields. Existing values are kept
  unless `retain` is false.
- **Sub-tasks.** When a parent moves to another project, its sub-tasks move
  with it and stay under it. A sub-task keeps its type when the destination
  offers it. With `inferSubtaskTypeDefault` it otherwise takes a sub-task type
  the destination offers; without it, the parent fails.

Every moved work item, sub-tasks included, fires the notification scheme's
Issue moved event.

Transition discovery evaluates each selected issue's current workflow,
status-history and hierarchy conditions as the requesting administrator. It
intersects transitions within each workflow group and omits transitions whose
screen requires additional fields, matching the bulk endpoint's executable
subset. Submission validates every issue/transition pair again; the worker then
uses the ordinary REST transition command so conditions, validators,
post-functions, permission loss and action history keep the single-issue
semantics. Transactional item markers make a recovered worker replay-safe. The
navigator offers transitions common to all selected visible rows.

Field discovery intersects the canonical create/edit metadata for every selected
project. It returns only field types the current command path can persist,
combines context-specific options, and provides the same option IDs used by the
ordinary Jira-compatible metadata APIs.

Bulk edit accepts the Jira field families backed by ZZIRA's current work-item
model: single-line text, clearable number, date-time, rich text, assignee,
single-select security level, labels, multiple versions, components and
priority. `selectedActions` must match the edited field IDs exactly and may
contain at most 200 fields. The task rechecks access and applies every visible
work item through the ordinary update command, preserving validation, immutable
history, notifications, security tombstones and SLA reconciliation. Replayed
set/add/remove operations do not append duplicate issue actions.

Bulk operations need Jira's global **Bulk change** permission, which
administrators grant on the global permissions page.

Delete, move, transition and edit accept `sendBulkNotification`, which
defaults to true. While a task runs, the notification scheme events it raises
(Issue deleted, Issue moved, the transition's event or Issue updated) still
create in-app notifications. Instead of one email per work item, each recipient
gets one bulk change email listing the work items, once the task finishes. With
`sendBulkNotification` false, the task sends no email. A retried task sends at
most one bulk email per recipient.

Cascading/color/date/select/group/multi-user/URL/time-tracking and issue-type
bulk field families remain. Bulk task progress is kept for 14 days.
