# Jira project roles and people

Updated: 2026-09-09

ZZIRA stores a workspace-wide Jira project-role catalog and project-specific
user and group assignments. Site administrators maintain reusable roles and
default actors at `/settings/project-roles`. A site administrator or a member
of the project's administration role manages that project's assignments at
`/projects/{key}/settings/roles`.

## Jira Cloud REST surface

The checkpoint implements all 15 pinned Jira Cloud Platform operations in the
Project roles and Project role actors groups:

| Method and path | Behavior |
|---|---|
| `GET /rest/api/3/role` | Lists the shared role catalog with default actors. |
| `POST /rest/api/3/role` | Creates a unique role without default actors. |
| `GET /rest/api/3/role/{id}` | Reads a role and its default actors. |
| `POST /rest/api/3/role/{id}` | Partially updates name or description; Jira name precedence applies when both are present. |
| `PUT /rest/api/3/role/{id}` | Replaces both name and description. |
| `DELETE /rest/api/3/role/{id}` | Deletes an unused role or atomically swaps every use to a replacement. |
| `GET/POST/DELETE /rest/api/3/role/{id}/actors` | Reads, adds, or removes default users or groups. |
| `GET /rest/api/3/project/{projectIdOrKey}/role` | Maps every shared role name to its project URL. |
| `GET /rest/api/3/project/{projectIdOrKey}/roledetails` | Returns role detail beans with `currentMember` filtering. |
| `GET/POST/PUT/DELETE /rest/api/3/project/{projectIdOrKey}/role/{id}` | Reads, adds, replaces, or removes project users and groups. |

Actor input accepts Jira account IDs, group IDs, group names, and
comma-separated values. Reads sort actors by type and display name and can omit
inactive users. Group membership participates in current-user role checks.
Malformed IDs, mixed group-name/group-ID input, inactive additions, missing
principals, duplicate names, and unsafe deletion return explicit Jira-shaped
errors.

## Persistence and authorization

`project_roles` is workspace scoped. `project_role_default_actors` records the
users and groups copied when a later project is created. Project-specific
actors use the common `role_bindings` authorization table, so browser pages,
REST reads, delegated project administration, and filter sharing evaluate the
same assignments.

New workspaces receive Administrators and Members roles. New projects receive
the current default membership, site administrators, their lead, and configured
default actors. Membership-role and project-lead changes update system-derived
assignments. Removing a workspace member clears their project assignments.
Deleting an in-use role requires `swap`; the transaction moves project actors,
default actors, filter share permissions, and the default/admin role traits
before deleting the old role.

Global role definition/default mutations require Jira site administration.
Project actor operations accept Jira site or organization administrators and
users or active groups assigned to the project's administration role. Project
REST endpoints conceal inaccessible projects with `404`; the browser returns a
direct `403`. Every mutation writes an immutable `actions` record in the same
transaction as its state change.

## Evidence and current boundary

- `internal/api3/project_roles_test.go` covers all 15 operations, validation,
  authorization, direct/group/default actors, inactive filtering, membership
  and lead changes, deletion swaps, filter shares, and action evidence.
- `e2e/project_roles.spec.ts` covers site-role creation/editing/defaults,
  inherited project assignments, delegated project administration, the custom
  role selector for filter sharing, 320 px reflow, deletion swap, and cleanup.
- `migrations/129_project_roles.sql` is exercised from a clean PostgreSQL
  schema as part of the migration and integration gates.

The compatibility assessment remains partial until Jira permission schemes
decide the Administer projects and Browse projects permissions, anonymous
project-role access is modeled, and app, guest, AI-agent, and other service-role
actor types are implemented. Default Members assignment is ZZIRA's current
workspace-access policy rather than a configurable Jira permission-scheme rule.
