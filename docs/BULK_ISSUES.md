# Jira bulk work-item operations

ZZIRA exposes the Jira Cloud bulk work-item routes under the ordinary site base
URL. The current delivered slice includes:

| Route | Behavior |
|---|---|
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
issue-type bulk field families remain alongside delete, move, transition and
transition discovery. Queue retention also needs Jira's 14-day expiry behavior.
