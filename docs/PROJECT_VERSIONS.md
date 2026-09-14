# Jira project versions

Updated: 2026-09-11

ZZIRA stores a project's versions, their release state and dates, the work items
that fix or affect them, their display order, and the external links Jira calls
related work. Project administrators manage them at `/projects/{key}/releases`.

## Jira Cloud REST surface

All 15 pinned project-version operations are implemented:

| Method and path | Behavior |
|---|---|
| `GET /rest/api/3/project/{projectIdOrKey}/versions` | Returns every version in display order. |
| `GET /rest/api/3/project/{projectIdOrKey}/version` | Returns the same versions paginated, with ordering and status filters. |
| `POST /rest/api/3/version` | Creates a version in a project; a version cannot be created already released. |
| `GET/PUT/DELETE /rest/api/3/version/{id}` | Reads an optionally expanded version, updates its name, description, dates and release or archive state, or deletes it. |
| `POST /rest/api/3/version/{id}/move` | Reorders a version with `after`, or `position` First, Earlier, Later, or Last. |
| `PUT /rest/api/3/version/{id}/mergeto/{moveIssuesTo}` | Moves the work items to another version and removes this one. |
| `POST /rest/api/3/version/{id}/removeAndSwap` | Removes a version, optionally moving fix and affects references elsewhere. |
| `GET /rest/api/3/version/{id}/relatedIssueCounts` | Counts the work items that fix and that affect the version. |
| `GET /rest/api/3/version/{id}/unresolvedIssueCount` | Counts the version's work items and how many remain unresolved. |
| `GET/POST/PUT /rest/api/3/version/{id}/relatedwork` | Lists, adds, or updates the external links attached to the release. |
| `DELETE /rest/api/3/version/{versionId}/relatedwork/{relatedWorkId}` | Removes one link. |

Version names are unique per project. Issue counts are computed from the work
items the caller may see, so a version never reveals restricted work through its
totals.

## Ordering and related work

Versions carry an explicit position, and `move` rewrites the order densely, so
reads are stable. Moving after a version that is not in the project is rejected
rather than silently ignored.

Related work is a category, an optional title, and an optional URL. The category
is required, and a URL must be an absolute `http` or `https` address, so a
release page cannot link somewhere the browser will not follow. Every related
work mutation writes an immutable action in the same transaction.

## Evidence and current boundary

- `internal/api3/versions_test.go` covers all 15 operations, permission and
  visibility filtering, validation of names and dates, the release and archive
  transitions, merge and remove-and-swap, every move form, and the related work
  lifecycle including its validation and 404s.
- `e2e/releases.spec.ts` covers the browser journey from planning a release
  through assigning scope, publishing notes, archiving and deleting.
- `migrations/138_version_related_work.sql` is exercised from a clean PostgreSQL
  schema.

## Drivers, approvers and operations

A release names a driver, the person responsible for it, set through the
version API's `driver` field or the release page. Project administrators ask
site members to approve a release with a note on what they approve; each
approver approves or declines on the release page, optionally saying why.
`migrations/187_version_drivers_approvers.sql` stores both. Version reads expand
`driver`, `approvers` and `operations`, the release actions a project
administrator may take, alongside `issuesstatus`; unknown expansions are
refused. `internal/api3/expansions_filters_test.go` covers the expansions and
approval decisions.

Related work ordering and exact Jira error wording remain.
