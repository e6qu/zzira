# Releases and project versions

The release hub at `/projects/{key}/releases` connects version planning to issue
scope and release notes. Project navigation exposes it to every workspace member.
Administrators create versions, change their dates/details, release/unrelease,
archive/unarchive and delete them. Members can assign visible work items to a
release. Deletion asks for confirmation and clears version references while
preserving the work items.

Build and deployment events submitted through the Jira Software APIs are
rolled up from the release's visible work items. The release view shows the
pipeline, environment and current outcome beside scope and notes, so teams can
verify delivery evidence without leaving the version journey.

## Delivered Jira Cloud operations

| Operation | Delivered behavior |
|---|---|
| POST /rest/api/3/version | Create using numeric projectId or deprecated project key; name, description, start/release dates, archived flag |
| GET/PUT/DELETE /rest/api/3/version/{id} | Read, partial update, release/archive lifecycle, delete with optional fix/affected replacements |
| GET /rest/api/3/project/{projectIdOrKey}/versions | Full project version list |
| GET /rest/api/3/project/{projectIdOrKey}/version | Pagination, nextPage, name/description query, status filtering and sequence/name/description/date ordering |
| GET /rest/api/3/version/{id}/relatedIssueCounts | Permission-filtered fix/affected membership counts |
| GET /rest/api/3/version/{id}/unresolvedIssueCount | Visible fixed-issue total and unresolved count |
| PUT /rest/api/3/version/{id}/mergeto/{moveIssuesTo} | Atomic merge of fix and affected references into another version in the same project, without duplicates |
| POST /rest/api/3/version/{id}/removeAndSwap | Atomic delete and optional fix/affected replacements; rejects unsupported custom-field replacements |

GET version and project lists support `expand=issuesstatus`. Version metadata is
also available through field/create/edit metadata. Issue create and update accept
`fields.fixVersions` and `fields.versions` arrays by ID or name; issue update
supports atomic `update` set/add/remove operations for these fields. The create
dialog includes both pickers and issue pages link to assigned versions.

JQL supports `fixVersion` and `affectedVersion` by ID or name with `=`, `!=`, `IN`,
`IS EMPTY` and `IS NOT EMPTY`. Existing logical AND/OR/NOT composition applies.

## Consistency and permissions

The project row serializes issue-version writes with version lifecycle changes.
All reference replacement/clearing, version state, issue snapshots and action-log
entries commit together. Version rename/release/archive refreshes issue snapshots,
including readable field history. Existing replicas receive those issue updates.
Concurrent additions to separate version memberships do not overwrite each other.

Counts, release work-item lists and notes use issue visibility checks. A version
mutation can update restricted issues administratively, but their actions stay
filtered from members who cannot read them. Replacements must be different
versions in the same project, and invalid requests roll back the entire operation.

## Remaining compatibility boundaries

This is a partial version/release implementation, not full Jira Cloud fidelity:

- Managing versions asks the project's permission scheme for Administer
  projects, as Jira does, so project roles and groups granted it manage
  versions. Reading follows the work items a person can see.
- Versions carry an explicit order. The move endpoint takes `after`, or a
  position of First, Earlier, Later or Last, and the release list offers a
  project administrator buttons to move a version up or down. Those buttons
  appear on the unfiltered list, because a version moves within the project's
  whole order rather than within the rows a filter leaves on screen. Dragging
  is not implemented; the buttons reach the keyboard and need no script.
- Releasing a version can move the work it did not finish to another version in
  the same project: `moveUnfixedIssuesTo` on a version update, and a choice in
  the release form. Work already done stays with the version that shipped it,
  and the move happens when a version is released rather than every time a
  released version is saved. The field is a version self link, as Jira sends
  it, and is not applicable when creating a version.
- Release notes group a version's work by work type, as Jira's do. A person
  chooses which work types to include and a format: styled on the page, or plain
  text or Markdown to copy. Plain text follows Jira's release notes layout, a
  `** Type` heading over `* [KEY] - Summary` lines. Notes list only the work the
  person can see. Cross-project releases remain a gap.
  Unsupported request options are explicit errors.
- Unresolved work is work whose resolution is empty, as Jira has it: that is
  what `unresolvedIssueCount` counts and what releasing a version moves. Reaching
  a done status sets the site's default resolution and leaving one clears it, so
  the two agree on the default path; where they differ, resolution decides. The
  release page's to do, in progress and done breakdown follows status
  categories, which is how Jira draws it.
- Dates use ISO dates and UTC for overdue calculation, with fixed English display
  dates. User-locale/site-timezone formatting is not yet configurable. A date can
  be cleared with an empty string; null is treated as omitted.
- Legacy nonnumeric project IDs stay valid. Version responses omit `projectId`
  for those projects instead of sending a string in an integer schema field;
  clients can create their versions using `project` with the project key. New
  numeric project IDs are emitted as integers.
- Issue version references are synchronized, but the release hub and its
  administration require an online connection. The version catalog itself has
  no local materialization yet. Pagination uses offsets under concurrent writes.
- Delivery evidence follows issue associations. Releases name a driver and
  collect approvals from the people asked to approve them; cross-project
  deployment grouping, configurable gates and environment promotion policies
  remain unfinished.

The lifecycle and schema reference is the vendored Jira platform specification
and [Atlassian's project versions API](https://developer.atlassian.com/cloud/jira/platform/rest/v3/api-group-project-versions/).
