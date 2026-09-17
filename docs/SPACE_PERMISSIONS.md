# Confluence space permissions (direct grants)

A direct grant gives one user or group one permission in a space. Grants
exist alongside [space role](CONFLUENCE_SPACE_ROLES.md) assignments, and the
permission check accepts either. Part of
[Confluence](CONFLUENCE_SITE_SURFACES.md). Code:
`internal/confluence/space_permission_grants.go`,
`internal/store/wiki_space_permissions.go`, table
`wiki_space_permission_grants` (migration 149).

## API

| Method and path | Behavior |
| --- | --- |
| `GET /wiki/api/v2/space-permissions` | The permissions that can be granted. Every entry except `read/space` lists `read/space` in `requiredPermissionIds`. |
| `GET /wiki/api/v2/space-role-mode` | See [lifecycle](SPACE_LIFECYCLE.md#space-role-mode). |
| `POST /wiki/rest/api/space/{spaceKey}/permission` | Grants one `operation` (`key` + `target`) to one `subject`. |
| `POST /wiki/rest/api/space/{spaceKey}/permission/custom-content` | Grants several custom content operations at once. An operation with `access: false` is skipped. |
| `DELETE /wiki/rest/api/space/{spaceKey}/permission/{id}` | Removes a grant. |
| `POST /wiki/rest/api/content/{id}/permission/check` | Reports whether a subject may perform an operation on a page. |

## Behavior

- **Subjects.**
  - A `user` is named by account id.
  - A `group` is named by id or by name.
  - Any other subject type is 400.
- **Operations.** A `key/target` pair that is not in the catalogue is 400.
  Granting the same permission twice changes nothing.
- **View covers all reads.** A granted `read/space` satisfies every `read/*`
  permission, which matches Confluence's single **View** permission.
- **Open spaces.** A space with no role assignments and no grants is open to
  its readers. The first grant or assignment closes the space to everyone it
  does not name.
- **Removing View.** Removing a subject's `read/space` also removes all of
  that subject's other grants in the space.
- **Administrators.** Workspace administrators administer every space. The
  grant endpoints look the space up without the usual visibility check for
  them, so a grant can never lock an administrator out.
- **Permission check.**
  - **How it decides.** It uses the same rules as reads. A group is allowed if
    any of its members is allowed.
  - **Missing page.** A page that does not exist is 404, not `false`.

## Permissions

Granting and removing need space administration or workspace administration.
The permission check needs site membership.

## Gaps

Tracked in [PLAN.md](../PLAN.md).

- **Missing permissions.** `export/space`, `restrict_content/space`,
  `archive/page` and Confluence's other non-CRUD permissions cannot be granted
  or enforced.
- **Subject types.** No anonymous or guest grants. Grant subjects are only
  `user` or `group`.
- **Permission check.** It covers pages only, not blog posts, attachments,
  comments or tree content.
- **Browser.** Direct grants cannot be viewed or edited in the browser; only
  role assignments can.

## Tests

`internal/confluence/space_permission_grants_test.go`

## See also

[SPACE_PERMISSION_TRANSITION.md](SPACE_PERMISSION_TRANSITION.md) ·
[CONFLUENCE_OPERATIONS.md](CONFLUENCE_OPERATIONS.md) ·
[CLOUD_PARITY.md](CLOUD_PARITY.md)
