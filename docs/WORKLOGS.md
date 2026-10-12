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
| `GET /rest/api/3/issue/{issueIdOrKey}/worklog` | Paged worklogs; optional start-time filters and property expansion |
| `POST /rest/api/3/issue/{issueIdOrKey}/worklog` | Log work (`timeSpent` or positive `timeSpentSeconds`, optional timezone-bearing `started` and ADF `comment`) |
| `GET /rest/api/3/issue/{issueIdOrKey}/worklog/{id}` | One worklog; one on another work item is 404 |
| `PUT /rest/api/3/issue/{issueIdOrKey}/worklog/{id}` | Change time, start time and comment; omitted values are kept |
| `DELETE /rest/api/3/issue/{issueIdOrKey}/worklog/{id}` | Delete one |
| `DELETE /rest/api/3/issue/{issueIdOrKey}/worklog` | Delete every worklog on the work item |
| `POST /rest/api/3/issue/{issueIdOrKey}/worklog/move` | Move 1 to 1,000 worklogs to another work item |
| `GET /rest/api/3/worklog/updated` | Worklogs changed after `since` (ms), up to 1,000 per page; property expansion |
| `GET /rest/api/3/worklog/deleted` | Worklogs deleted after `since` (ms), up to 1,000 per page; property expansion |
| `POST /rest/api/3/worklog/list` | Up to 1,000 worklogs by ID, across work items; property expansion |
| `GET /rest/api/3/issue/{k}/worklog/{id}/properties` | Property keys |
| `GET/PUT/DELETE /rest/api/3/issue/{k}/worklog/{id}/properties/{propertyKey}` | One property; `PUT` answers 201 for a new key, 200 for a replacement |
| `POST /rest/internal/api/latest/worklog/bulk` | Which of 1 to 1,000 `{issueId, worklogId}` pairs exist (see [JIRA_PLATFORM.md](JIRA_PLATFORM.md)) |

Log, change and delete accept `adjustEstimate`, `newEstimate`, `reduceBy`,
`increaseBy` and `notifyUsers`, as described in
[TIME_TRACKING.md](TIME_TRACKING.md).

## Behavior

- **Dates and authors.** `started` records when work happened independently of
  `created`. Omitting it on create uses the recording time; omitting it on update
  preserves the existing start time. Supply an ISO timestamp with a timezone,
  such as `2026-10-01T09:00:00.000+0000`; invalid or null values are refused.
  `updated` and `updateAuthor` record the last change, while the original author
  and creation time remain intact. Responses include the numeric `issueId` and
  a working `self` link scoped to that work item. Existing logs inherit their
  creation time as their start time; their original author is the migration's
  fallback editor because earlier editor identities were not stored.
- **Collection reads.** `startAt` defaults to 0; `maxResults` defaults to 5,000
  and is capped at 5,000. Non-negative values are required. `startedAfter` and
  `startedBefore` are exclusive epoch-millisecond bounds applied before paging;
  `total` counts the filtered collection. `expand=properties` adds each log's
  property array, including an empty array where appropriate. Single reads, the
  bulk read by id and the change feeds accept the same expansion, and none of
  them carries the properties unasked. Property expansion is fetched in one
  batch.

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
(`POST /issues/{key}/worklogs/{id}/delete`). The optional “Work started (UTC)”
input records earlier work; leaving it blank uses the submission time. Refused
submissions retain duration, date, description and remaining-estimate choices.
The estimate options occupy their own fieldset so the description does not
cover the radio controls. Activity displays the recorded start time separately from the logging time,
including when the offline replica renders it.

## Gaps

See [PLAN.md](../PLAN.md).

- Worklog `visibility` restrictions to a group or role remain unimplemented.
  Create and update refuse a supplied non-null restriction with a field error;
  they never silently save a restricted note as unrestricted.

## Tests

`internal/api3/worklogs_test.go`, `internal/api3/time_tracking_test.go`,
`internal/web/worklog_started_test.go`, `e2e/worklog_started.spec.ts`.

## See also

[TIME_TRACKING.md](TIME_TRACKING.md) · [WORKFLOW_RULES.md](WORKFLOW_RULES.md) ·
[PERMISSION_SCHEMES.md](PERMISSION_SCHEMES.md) ·
[NOTIFICATION_SCHEMES.md](NOTIFICATION_SCHEMES.md)
