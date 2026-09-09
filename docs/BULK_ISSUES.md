# Jira bulk work-item operations

ZZIRA exposes the Jira Cloud bulk work-item routes under the ordinary site base
URL. Bulk watch and unwatch are the first complete durable execution slice:

| Route | Behavior |
|---|---|
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

Workspace administrators currently stand in for Jira's global **Bulk change**
permission. Configurable global permission grants and Jira's notification
controls remain part of the administration completion work. The remaining Jira
bulk operations are delete, edit, move, transition, field discovery and
transition discovery. Queue retention also needs Jira's 14-day expiry behavior.

