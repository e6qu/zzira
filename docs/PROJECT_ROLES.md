# Project roles

A site-wide catalog of Jira project roles, their default actors, and each project's user and group assignments. Site administrators manage roles and default actors at `/settings/project-roles`. Project administrators manage a project's assignments at `/projects/{key}/settings/roles`. Roles are holders in [permission schemes](PERMISSION_SCHEMES.md), [notification schemes](NOTIFICATION_SCHEMES.md), [issue security schemes](ISSUE_SECURITY_SCHEMES.md) and [filter sharing](FILTERS.md). Part of the [Jira platform](JIRA_PLATFORM.md). For status, see [CLOUD_PARITY.md](CLOUD_PARITY.md).

## API

All 15 operations of Jira's Project roles and Project role actors groups:

| Method and path | Behavior |
| --- | --- |
| `GET /rest/api/3/role` | The catalog with default actors. |
| `POST /rest/api/3/role` | Creates a role (unique name, no default actors). |
| `GET /rest/api/3/role/{id}` | A role and its default actors. |
| `POST /rest/api/3/role/{id}` | Partial update of name or description; name wins when both are sent, as in Jira. |
| `PUT /rest/api/3/role/{id}` | Replaces name and description. |
| `DELETE /rest/api/3/role/{id}` | Deletes an unused role; `swap` moves every use to another role first. |
| `GET/POST/DELETE /rest/api/3/role/{id}/actors` | Default users and groups. |
| `GET /rest/api/3/project/{projectIdOrKey}/role` | Every role name mapped to its project URL. |
| `GET /rest/api/3/project/{projectIdOrKey}/roledetails` | Role details; `currentMember` filters to the caller's roles. |
| `GET/POST/PUT/DELETE /rest/api/3/project/{projectIdOrKey}/role/{id}` | Reads, adds, replaces or removes a project's users and groups. |

- Actors are users (`atlassian-user-role-actor`, account ids) and groups (`atlassian-group-role-actor` by name, `atlassian-group-role-actor-id` by id). Comma-separated values are accepted; group names and group ids cannot be mixed.
- Reads sort actors by type and display name and can exclude inactive users.
- Group membership counts toward the caller's roles.
- Malformed ids, inactive or missing principals, duplicate names and unsafe deletes return Jira-shaped errors.

## Behavior

- **Storage:** `project_roles` is per workspace. `project_role_default_actors` holds the actors copied into new projects. Project assignments live in the shared `role_bindings` table, so pages, REST, delegated project administration and filter sharing see the same data.
- **Defaults:** every workspace starts with Administrators (10000) and Members (10001). A new project gets the default memberships, site administrators, its lead and the default actors.
- **Automatic updates:** membership role changes and project lead changes update the system-assigned actors. Removing a workspace member clears their project assignments.
- **Deleting a role in use** requires `swap`. One transaction moves project actors, default actors, filter share permissions and the Administrators/Members role traits, then deletes the role.
- **Audit:** every change writes an action in the same transaction.

## Permissions

- Role definitions and default actors: site administrators.
- Project actors: site or organization administrators, and anyone holding Administer projects in the project's permission scheme (directly, through a group or through a role).
- REST hides inaccessible projects with 404; the browser answers 403.

## Gaps

Tracked in [PLAN.md](../PLAN.md).

- Jira's `atlassian-addons-project-access` role, which gives installed apps project access, is not modelled.

## Tests

- `internal/api3/project_roles_test.go`: all 15 operations, validation, authorization, direct/group/default actors, inactive filtering, membership and lead changes, deletion swaps, filter shares, actions.
- `e2e/project_roles.spec.ts`: role creation and defaults, inherited assignments, delegated administration, the role picker in filter sharing, deletion swap, 320px reflow.
