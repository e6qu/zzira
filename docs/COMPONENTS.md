# Project components

Components group a project's work items. Each has a stable numeric ID, a name
unique within its project (case-insensitive), a description, an optional lead
and a default-assignee mode (project default, project lead, component lead or
unassigned). Part of the [Jira platform](JIRA_PLATFORM.md); see
[CLOUD_PARITY.md](CLOUD_PARITY.md) for status.

## API

| Route | Behavior |
|---|---|
| `GET /rest/api/3/component` | Components across browsable projects; `projectIds`, `query`, `orderBy`, offset paging |
| `POST /rest/api/3/component` | Create |
| `GET/PUT/DELETE /rest/api/3/component/{id}` | Read, partial update, delete (`moveIssuesTo` reassigns work items) |
| `GET /rest/api/3/component/{id}/relatedIssueCounts` | Work items using the component |
| `GET /rest/api/3/project/{projectIdOrKey}/components` | All of a project's components |
| `GET /rest/api/3/project/{projectIdOrKey}/component` | The same, paged, with search and ordering |

Responses include the effective assignee and whether that assignment is valid.
`componentSource` accepts only `jira`.

## Behavior

- The `components` field takes arrays of component IDs or names on create
  and update. The command checks the project, drops duplicates and stores
  snapshots.
- A rename refreshes every assigned work item in the same transaction.
  Deleting either removes the component from its work items or replaces it
  with another component of the same project; both emit updated work item
  snapshots for sync clients.
- When the assignee is left at its default, the first selected component's
  default assignee applies before the project default.
- Every write records a product action and, when the workspace belongs to a
  site, an organization audit event.
- JQL: `component`/`components`, multi-value empty and negation semantics, and
  `component in componentsLeadByUser([user])`. See [JQL.md](JQL.md).

## Permissions

- Reads need Browse projects on the component's project; the read routes are
  open to anonymous callers when anonymous access allows it (see
  [ANONYMOUS_ACCESS.md](ANONYMOUS_ACCESS.md)).
- Writes need Administer projects on the project (Administer Jira implies it).

## UI

Project settings (`/projects/{key}/settings`) list components; project
administrators create, edit (lead, default assignee) and delete them. Create
and edit metadata offer the same choices to REST and the create dialog.

## Gaps

See [PLAN.md](../PLAN.md).

- Compass components (`componentSource=compass`, archived and deleted Compass
  representations) are not supported.

## Tests

`internal/api3/components_test.go`, `e2e/projects.spec.ts`.

## See also

[PROJECT_GOVERNANCE.md](PROJECT_GOVERNANCE.md) ·
[PERMISSION_SCHEMES.md](PERMISSION_SCHEMES.md) · [ISSUE_METADATA.md](ISSUE_METADATA.md)
