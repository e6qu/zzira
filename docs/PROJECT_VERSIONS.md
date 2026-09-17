# Project versions API

A project's versions (releases): release state and dates, the work items that
fix or affect them, display order, driver, approvers and related work links.
This page covers the REST API; the release hub UI is in
[RELEASES.md](RELEASES.md). Part of the [Jira platform](JIRA_PLATFORM.md) and
used by [Jira Software](JIRA_SOFTWARE.md); see
[CLOUD_PARITY.md](CLOUD_PARITY.md) for status.

## API

All 15 Jira Cloud project-version operations:

| Route | Behavior |
|---|---|
| `GET /rest/api/3/project/{projectIdOrKey}/versions` | Every version in display order |
| `GET /rest/api/3/project/{projectIdOrKey}/version` | Paged, with ordering, `query` and `status` (`released`, `unreleased`, `archived`) filters |
| `POST /rest/api/3/version` | Create; `released: true` is refused, as is `moveUnfixedIssuesTo` |
| `GET/PUT/DELETE /rest/api/3/version/{id}` | Read (with expansions), update name, description, dates, driver, release or archive state and `moveUnfixedIssuesTo`, or delete (`moveFixIssuesTo`, `moveAffectedIssuesTo`) |
| `POST /rest/api/3/version/{id}/move` | Reorder with `after`, or `position` `First`, `Earlier`, `Later`, `Last` |
| `PUT /rest/api/3/version/{id}/mergeto/{moveIssuesTo}` | Move work items to another version and delete this one |
| `POST /rest/api/3/version/{id}/removeAndSwap` | Delete, optionally moving fix and affects references |
| `GET /rest/api/3/version/{id}/relatedIssueCounts` | Work items that fix and that affect the version |
| `GET /rest/api/3/version/{id}/unresolvedIssueCount` | Work items and how many are unresolved |
| `GET/POST/PUT /rest/api/3/version/{id}/relatedwork` | List, add or update related work links |
| `DELETE /rest/api/3/version/{versionId}/relatedwork/{relatedWorkId}` | Remove one link |

Expansions: `issuesstatus`, `operations` (release actions a project
administrator may take), `driver` and `approvers`; unknown expansions are
refused.

## Behavior

- Names are unique per project.
- Counts include only work items the caller can see, so totals never reveal
  restricted work.
- Versions have an explicit position; `move` rewrites the order densely.
  Moving after a version from another project is refused.
- Related work is a required category, an optional title and an optional URL,
  which must be absolute `http` or `https`. Every change writes an action in
  the same transaction.
- A version names a **driver**. Project administrators request **approvals**
  from site members with a note; each approver approves or declines on the
  release page, optionally with a reason.

## Permissions

Reads need Browse projects on the version's project; writes need Administer
projects.

## UI

`/projects/{key}/releases` and `/projects/{key}/releases/{version}`; see
[RELEASES.md](RELEASES.md).

## Tests

`internal/api3/versions_test.go`, `internal/api3/expansions_filters_test.go`,
`e2e/releases.spec.ts`.

## See also

[RELEASES.md](RELEASES.md) · [REPORTS.md](REPORTS.md) · [JQL.md](JQL.md)
(version functions) · [PROJECT_GOVERNANCE.md](PROJECT_GOVERNANCE.md)
