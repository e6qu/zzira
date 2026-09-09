# Jira bulk work-item operations

ZZIRA exposes the Jira Cloud bulk work-item routes under the ordinary site base
URL. The current delivered slice includes:

| Route | Behavior |
|---|---|
| `POST /rest/api/3/bulk/issues/delete` | Queue deletion for up to 1,000 selected work items with execution-time access checks and per-item results |
| `POST /rest/api/3/bulk/issues/move` | Queue project, issue-type and explicit parent moves with workflow status inference, key aliases and per-item results |
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
uses the common progress page. Classification mappings, mandatory-field value
mappings, and implicit parent-with-subtasks moves are rejected explicitly; the
task reports an execution-time error if a parent acquires subtasks after
submission.

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

Workspace administrators currently stand in for Jira's global **Bulk change**
permission. Configurable global permission grants and Jira's notification
controls remain part of the administration completion work. The
`sendBulkNotification` switch is accepted but bulk email delivery is not yet
available. Cascading/color/date/select/group/multi-user/URL/time-tracking and
issue-type bulk field families remain alongside transition and
transition discovery. Queue retention also needs Jira's 14-day expiry behavior.
