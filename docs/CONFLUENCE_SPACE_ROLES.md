# Confluence space roles

A space role is a named set of space permissions. Roles are assigned in each
space to users, groups or access classes. The permission check accepts either
a role assignment or a [direct grant](SPACE_PERMISSIONS.md). Part of
[Confluence](CONFLUENCE_SITE_SURFACES.md). Code:
`internal/confluence/space_roles.go`, `internal/store/wiki_space_roles.go`,
`internal/store/wiki_space_role_tasks.go`.

## API

| Method and path | Behavior |
| --- | --- |
| `GET /wiki/api/v2/space-roles` | Lists roles. Filters: `space-id`, `role-type` (`SYSTEM` or `CUSTOM`), and `principal-type` with `principal-id`. |
| `POST /wiki/api/v2/space-roles` | Creates a custom role. |
| `GET /wiki/api/v2/space-roles/{id}` | Reads one role. |
| `PUT /wiki/api/v2/space-roles/{id}` | Updates a custom role. Returns `202` with a `taskId`. |
| `DELETE /wiki/api/v2/space-roles/{id}` | Deletes a custom role and all of its assignments. Returns `202` with a `taskId`. |
| `GET /wiki/api/v2/spaces/{id}/role-assignments` | Lists a space's assignments. |
| `POST /wiki/api/v2/spaces/{id}/role-assignments` | Replaces a space's assignments. |
| `GET /wiki/api/v2/space-role-mode` | `PRE_ROLES`, `ROLES_TRANSITION` or `ROLES`, based on the grants and assignments that exist (see [lifecycle](SPACE_LIFECYCLE.md#space-role-mode)). |

UI: the space page has two forms. Site administrators use one to create roles
(`POST /wiki/spaces/{space}/roles`). Space administrators use the other to set
assignments (`POST /wiki/spaces/{space}/role-assignments`).

## Behavior

- **System roles.**
  - `system-admin` (Space administrators) has everything, including
    `administer/space`.
  - `system-member` (Space members) can create, read, update and delete every
    content type.
  - `system-viewer` (Space viewers) can read only.
  - System roles cannot be changed or deleted.
- **Principals.**
  - `USER` must be an active site member.
  - `GROUP` must belong to the workspace directory.
  - `ACCESS_CLASS` is one of `anonymous-users`, `authenticated-users`,
    `all-licensed-users`, `all-product-admins` or `jsm-project-admins`.
- **Default assignments.** A new space gives `authenticated-users` the member
  role and `all-product-admins` the admin role (see
  [spaces](CONFLUENCE_SPACES.md#creating-a-space)).
- **Dependencies.** Every permission requires `read/space`. Creating, editing
  or deleting a content type requires reading it. A role that is missing a
  dependency is 400, and the error names what is missing.
- **Filtering by principal.** With `space-id`, only that space is searched.
  Without it, every space the caller can see is searched. A space with no
  assignments of its own counts as holding its default roles.
  `principal-type` and `principal-id` must be given together.
- **Updating.** `PUT` changes a custom role's name, description and
  permissions.
  - **`anonymousReassignmentRoleId`** moves the role's anonymous-access
    assignments to another existing role, in every space.
  - **`guestReassignmentRoleId`** does the same for guests: people and groups
    that hold the guest role on the site's Confluence.
  - **Existing holders.** A principal that already holds the target role still
    ends up with a single assignment.
- **Long tasks.** Updates and deletes finish before the response is sent, and
  the task is recorded as complete. `/wiki/rest/api/longtask/{id}` reports it.

## Permissions

- **Custom roles.** Creating, updating and deleting them needs site or
  organization administration.
- **Assignments.** Setting a space's assignments needs administration of that
  space.

## Content state settings

The space page also holds the [content state](CONTENT_STATES.md) settings:
whether pages show states at all, whether the space's suggested states may be
used, and whether writers may create their own.

- **Settings read.** `GET /wiki/rest/api/space/{spaceKey}/state/settings`
  returns the three settings to space administrators. It lists the suggested
  states only when they are allowed.
- **Setting a state.** Setting a state on a page is 400 when states are off,
  or when that kind of state is off.
- **Content by state.** `GET /wiki/rest/api/space/{spaceKey}/state/content`
  takes `expand`. Requesting `body.export_view` or `body.styled_view` limits a
  page of results to 25.

## Browser

Custom roles are created, edited and deleted from the space page's **Space
roles** card, at `POST /wiki/spaces/{space}/roles` with `action=create`,
`update` or `delete`. The permission checkboxes are the enforced catalogue, so
a role built in the browser can hold everything a role built through the API
can, and the edit form offers the two reassignment roles the API takes for
anonymous and guest holders. System roles are Confluence's own: they are shown
but not editable.

## Tests

`internal/confluence/space_roles_states_test.go`

## Gaps

Tracked in [PLAN.md](../PLAN.md).

- **Role catalogue.** No `export/space`, `archive/page` or
  `restrict_content/space` permissions.

## See also

[SPACE_PERMISSION_TRANSITION.md](SPACE_PERMISSION_TRANSITION.md) ·
[CONFLUENCE_OPERATIONS.md](CONFLUENCE_OPERATIONS.md) ·
[CLOUD_PARITY.md](CLOUD_PARITY.md)
