# Worklogs

A worklog records time spent on a work item. zzira serves Jira Cloud's worklog
operations: per-item reads and writes, moving worklogs between work items, the
workspace-wide updated and deleted feeds, bulk fetch by ID, and worklog
properties. Estimates are covered in [TIME_TRACKING.md](TIME_TRACKING.md).
Part of the [Jira platform](JIRA_PLATFORM.md); see
[CLOUD_PARITY.md](CLOUD_PARITY.md) for status.

## API

| Route | Behavior |
|---|---|
| `GET /rest/api/3/issue/{issueIdOrKey}/worklog` | All of the work item's worklogs |
| `POST /rest/api/3/issue/{issueIdOrKey}/worklog` | Log work (`timeSpent` or positive `timeSpentSeconds`, optional ADF `comment`) |
| `GET /rest/api/3/issue/{issueIdOrKey}/worklog/{id}` | One worklog; one on another work item is 404 |
| `PUT /rest/api/3/issue/{issueIdOrKey}/worklog/{id}` | Change time and comment; an omitted comment is kept |
| `DELETE /rest/api/3/issue/{issueIdOrKey}/worklog/{id}` | Delete one |
| `DELETE /rest/api/3/issue/{issueIdOrKey}/worklog` | Delete every worklog on the work item |
| `POST /rest/api/3/issue/{issueIdOrKey}/worklog/move` | Move 1 to 1,000 worklogs to another work item |
| `GET /rest/api/3/worklog/updated` | Worklogs changed after `since` (ms), up to 1,000 per page |
| `GET /rest/api/3/worklog/deleted` | Worklogs deleted after `since` (ms), up to 1,000 per page |
| `POST /rest/api/3/worklog/list` | Up to 1,000 worklogs by ID, across work items |
| `GET /rest/api/3/issue/{k}/worklog/{id}/properties` | Property keys |
| `GET/PUT/DELETE /rest/api/3/issue/{k}/worklog/{id}/properties/{propertyKey}` | One property; `PUT` answers 201 for a new key, 200 for a replacement |
| `POST /rest/internal/api/latest/worklog/bulk` | Which of 1 to 1,000 `{issueId, worklogId}` pairs exist (see [JIRA_PLATFORM.md](JIRA_PLATFORM.md)) |

Log, change and delete accept `adjustEstimate`, `newEstimate`, `reduceBy`,
`increaseBy` and `notifyUsers`, as described in
[TIME_TRACKING.md](TIME_TRACKING.md).

## Behavior

- **Feeds.** Worklogs carry `updated_at`, stamped on change and on move, so
  the updated feed reports moved worklogs. A delete leaves a tombstone
  (`deleted_worklogs`: ID, work item, author) that the deleted feed reports.
  Both feeds report and compare times in milliseconds, so passing the previous
  `until` as the next `since` always makes progress.
- **Visibility.** `worklog/list` and `worklog/updated` return only worklogs on
  active projects the caller can browse and work items issue security allows;
  others are silently omitted, as in Jira. `worklog/deleted` is unfiltered: it
  returns only IDs and timestamps, and clients need every ID to invalidate
  their caches.
- **Properties** are deleted with their worklog.
- **Non-editable work items.** A status whose properties set
  `jira.issue.editable` (or the deprecated `issueEditable`) to `false` locks
  its work items: logging, changing, deleting and moving work are 400. A
  Connect or Forge app with Administer Jira can pass
  `overrideEditableFlag=true`; anyone else passing it gets 403.

## Permissions

| Action | Needs |
|---|---|
| Log work | Work on issues |
| Change | Edit all worklogs, or Edit own worklogs for the caller's own |
| Delete | Delete all worklogs, or Delete own worklogs for the caller's own |
| Move | Delete all worklogs and Work on issues on both work items, both editable |

## UI

The work item page logs work (`POST /issues/{key}/worklogs`) and deletes it
(`POST /issues/{key}/worklogs/{id}/delete`).

## Gaps

See [PLAN.md](../PLAN.md).

- No `started` time: create and update ignore it and the bean reports a
  non-Jira `startsAt` equal to `created`.
- The bean's `updated` and `updateAuthor` repeat `created` and `author`; it
  lacks `issueId`, and `self` omits the work item.
- The per-item list ignores `startAt`, `maxResults`, `startedAfter`,
  `startedBefore` and `expand=properties`.
- No worklog `visibility` restriction to a group or role.

## Tests

`internal/api3/worklogs_test.go`, `internal/api3/time_tracking_test.go`.

## See also

[TIME_TRACKING.md](TIME_TRACKING.md) · [WORKFLOW_RULES.md](WORKFLOW_RULES.md) ·
[PERMISSION_SCHEMES.md](PERMISSION_SCHEMES.md) ·
[NOTIFICATION_SCHEMES.md](NOTIFICATION_SCHEMES.md)
