# Jira permission schemes

Updated: 2026-09-10

ZZIRA stores Jira project permission schemes, their grants, and the scheme
assigned to each project. Site administrators manage the shared catalog at
`/settings/permission-schemes`. Project administrators can inspect the effective
scheme and its grants at `/projects/{key}/settings/permissions`.

## Jira Cloud REST surface

This checkpoint implements the 11 pinned permission-scheme and project
assignment operations:

| Method and path | Behavior |
|---|---|
| `GET/POST /rest/api/3/permissionscheme` | Lists schemes or creates a scheme, with optional expanded grants. |
| `GET/PUT/DELETE /rest/api/3/permissionscheme/{schemeId}` | Reads, replaces, or deletes an unused non-default scheme. |
| `GET/POST /rest/api/3/permissionscheme/{schemeId}/permission` | Lists or adds grants. |
| `GET/DELETE /rest/api/3/permissionscheme/{schemeId}/permission/{permissionId}` | Reads or removes one grant. |
| `GET/PUT /rest/api/3/project/{projectKeyOrId}/permissionscheme` | Reads or assigns the project's scheme. |

It also implements the five pinned permission discovery and evaluation
operations:

| Method and path | Behavior |
|---|---|
| `GET /rest/api/3/permissions` | Returns the built-in global and project permission catalog. |
| `GET /rest/api/3/mypermissions` | Evaluates selected permissions in global, project, issue, or comment context. |
| `POST /rest/api/3/permissions/check` | Evaluates bounded global and project permission batches, including another user for administrators. |
| `POST /rest/api/3/permissions/project` | Returns active projects where the caller has every requested permission. |
| `GET /rest/api/3/user/permission/search` | Pages active users who satisfy the requested permissions and context. |

Malformed IDs, unknown expansion names, invalid principals, duplicate grants,
duplicate scheme names, deletion of the default or an assigned scheme, and
unauthorized writes return explicit Jira-shaped errors. Every scheme, grant,
and assignment mutation writes an immutable action in the same transaction.

## Catalog, holders, and defaults

The catalog contains Jira's 36 built-in project permissions and nine global
permission keys. Project grants accept `anyone`, application-role, assignee,
group, group-custom-field, project-lead, project-role, reporter, service-portal
customer, user, and user-custom-field holders. User, group, and role holders are
validated against the current workspace. Group names remain synchronized after
a rename, deleted principals are removed, and role deletion swaps update grants
atomically.

Every workspace receives numeric scheme `10000`. Its Members role receives the
33 ordinary work permissions, while its Administrators role receives
`ADMINISTER_PROJECTS`, `EDIT_WORKFLOW`, and `EDIT_ISSUE_LAYOUT`. Existing and
new projects receive that default assignment, preserving the access behavior
that preceded configurable schemes.

Global Jira administration follows organization and site administrator role
bindings. Active workspace members receive the existing shared-object,
user-picker, browse-user, bulk-change, and team-managed project capabilities;
the remaining global permissions stay administrator-only until global
permission administration is delivered.

## Runtime authorization

The PostgreSQL evaluator is the common permission decision for REST, browser,
search, enhanced-search snapshots, synchronization, board/release issue lists,
and project navigation. `BROWSE_PROJECTS` now hides denied projects and work
items before serialization. `ADMINISTER_PROJECTS` controls project role and
effective-permission administration, including direct users and group-backed
project-role grants. Site and organization administrators retain access.

Issue-sensitive holders evaluate the selected issue for direct reads. For a
project-only permission query, assignee, reporter, and custom-field holders
match when at least one active issue in that project supplies the relationship,
which matches Jira's context-dependent permission-query behavior.

## Evidence and current boundary

- `internal/api3/permission_schemes_test.go` covers the 16 operations,
  validation, assignment conflicts, direct/group grants, delegated project
  administration, issue/project/search visibility, permission discovery,
  pagination inputs, and action evidence.
- `e2e/permission_schemes.spec.ts` covers site scheme creation, grant editing,
  project assignment, delegated project inspection, effective API permissions,
  320 px reflow, reassignment, and cleanup.
- `migrations/130_permission_schemes.sql` is exercised from a clean PostgreSQL
  schema as part of the migration and integration gates.

The compatibility assessment remains partial because Jira permits anonymous
access to some discovery operations while ZZIRA currently requires a workspace
identity at the HTTP boundary. App-defined permission registration, global
permission administration, and action-specific enforcement for every remaining
issue mutation are later PR 1 work. Holder expansion beans and every Jira
pagination and error edge also remain under contract review.
