# Confluence space roles, content state settings and permission transitions

## Space roles
- **Permission dependencies.** Creating or updating a role checks that its
  permissions include what they depend on. Every permission needs
  `read/space`, and creating, editing or deleting a kind of content needs
  reading that content. A role missing a dependency is refused with 400,
  naming what is missing.
- **Filtering by principal.** `GET /wiki/api/v2/space-roles` with a
  `principal-type` and `principal-id` lists the roles that principal holds.
  With `space-id` only that space counts; without it, every space the caller
  can see does. Spaces with no assignments of their own contribute their
  default roles.
- **Updating a role.** `PUT /wiki/api/v2/space-roles/{id}` changes a custom
  role's name, description and permissions.
  - `anonymousReassignmentRoleId` moves the role's anonymous-access
    assignments to another existing role, in every space.
  - `guestReassignmentRoleId` does the same for guests: people, and groups,
    holding the guest role on the site's Confluence.
  - A principal that already held the target role keeps a single assignment.
- **Deleting a role.** `DELETE /wiki/api/v2/space-roles/{id}` removes a
  custom role and its assignments in every space.
- **Long tasks.** Both answer `202` with a `taskId`. The work is done before
  responding and the task is recorded as finished, so
  `/wiki/rest/api/longtask/{id}` reports the outcome. System roles cannot be
  changed or deleted.

## Content state settings
Space administrators choose, on the space page, whether pages show content
states at all, whether writers may use the states the space suggests, and
whether they may make their own.
- `GET /wiki/rest/api/space/{spaceKey}/state/settings` reports the three
  settings to space administrators. It lists the suggested states only when
  they are allowed.
- Setting a state on a page is refused with 400 when states are turned off,
  or when the kind of state is.
- `GET /wiki/rest/api/space/{spaceKey}/state/content` takes `expand` like
  other v1 content reads. Asking for `body.export_view` or `body.styled_view`
  holds a page of results to 25.

## Permission transition combinations
`GET /wiki/api/v2/space-permissions/transition/combinations`:
- lists combinations by principal count, highest first;
- pages with `limit` (1–250, default 25) and an opaque `cursor`, returned in
  the body while more results remain;
- refuses a limit outside that range, or a cursor it did not issue, with 400.
