# Worklogs

Updated: 2026-09-11

ZZIRA records work logged against a work item, and serves the full Jira Cloud
worklog surface around it: the per-item reads and writes, the move between work
items, the workspace-wide updated and deleted feeds, the bulk fetch by id, and
entity properties on a worklog.

## Jira Cloud REST surface

All 14 pinned worklog operations are implemented. An audit against a running
server found four already working — the list, the create, the single read and
the single delete — and the other ten returning 404.

| Method and path | Behavior |
|---|---|
| `GET /rest/api/3/issue/{issueIdOrKey}/worklog` | Lists the work item's worklogs. |
| `POST /rest/api/3/issue/{issueIdOrKey}/worklog` | Logs work; `timeSpentSeconds` must be positive. |
| `GET /rest/api/3/issue/{issueIdOrKey}/worklog/{id}` | Reads one worklog; one on another work item is a 404. |
| `PUT /rest/api/3/issue/{issueIdOrKey}/worklog/{id}` | Changes the time and comment. An omitted comment keeps the stored one. |
| `DELETE /rest/api/3/issue/{issueIdOrKey}/worklog/{id}` | Removes one worklog. |
| `DELETE /rest/api/3/issue/{issueIdOrKey}/worklog` | Removes every worklog on the work item. |
| `POST /rest/api/3/issue/{issueIdOrKey}/worklog/move` | Moves worklogs to another work item, 1 to 1000 at a time. |
| `GET /rest/api/3/worklog/updated` | Reports worklogs changed after `since`. |
| `GET /rest/api/3/worklog/deleted` | Reports worklogs removed after `since`. |
| `POST /rest/api/3/worklog/list` | Fetches up to 1000 worklogs by id, across work items. |
| `GET /rest/api/3/issue/{k}/worklog/{id}/properties` | Lists a worklog's property keys. |
| `GET/PUT/DELETE /rest/api/3/issue/{k}/worklog/{id}/properties/{propertyKey}` | Reads, stores, or removes one property; a new key answers 201 and a replacement 200. |

## How the feeds work

Jira's updated and deleted feeds let a client resynchronize without re-reading
every work item, so they need two things this schema did not have.

**Worklogs now carry an `updated_at`.** It is stamped on the update and on the
move, so a client that follows the feed sees a worklog that changed work items.

**A delete records a tombstone.** The row is gone, so `deleted_worklogs` keeps
the id, the work item and the author it had, which is what the deleted feed
reports. The tombstone is what makes the feed answer at all; without it a
deleted worklog would simply vanish and a client's cache would keep it forever.

Both feeds report times in milliseconds, and **compare in milliseconds too**.
PostgreSQL stores microseconds, so a client that passes the previous `until`
back as its next `since` would otherwise be handed the same entries again on
the sub-millisecond remainder and never make progress.

## Visibility

`POST /worklog/list` and `GET /worklog/updated` are workspace-wide, so they
resolve through the same predicate the search uses: the project must be active,
the caller must have `BROWSE_PROJECTS`, and issue security must permit the work
item. A caller who cannot browse a project gets an empty list rather than an
error, which is what Jira does.

`GET /worklog/deleted` is deliberately not filtered. It returns an id and a
timestamp for something that no longer exists, and a client invalidating its
cache needs every id it may be holding — filtering would leave stale entries
behind for exactly the worklogs the caller can no longer check.

## Evidence and current boundary

- `internal/api3/worklogs_test.go` covers all 14 operations, including the
  404s for a worklog on another work item, the non-positive time, the empty id
  list, the unknown move destination, a worklog that is not on the source work
  item, an unparseable `since`, the caught-up feed, the cascade that takes a
  worklog's properties with it, and a caller who cannot browse the project.
- `migrations/141_worklog_updates.sql` and `migrations/142_worklog_properties.sql`
  are exercised from a clean PostgreSQL schema.

Jira's `startedAfter`/`startedBefore` filters and `expand` on the worklog reads,
the `notifyUsers` and `adjustEstimate` parameters, worklog visibility
restriction to a group or role, and `POST /rest/internal/api/latest/worklog/bulk`
remain.
