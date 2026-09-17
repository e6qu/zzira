# Confluence space lifecycle (v1)

The v1 space API creates, updates and deletes spaces, and also serves each
space's settings and theme. Part of [Confluence](CONFLUENCE_SITE_SURFACES.md).
v2 listing and creation are in [CONFLUENCE_SPACES.md](CONFLUENCE_SPACES.md).
Code: `internal/confluence/space_lifecycle.go`,
`internal/store/wiki_space_lifecycle.go`, migration 151.

## API

| Method and path | Behavior |
| --- | --- |
| `POST /wiki/rest/api/space` | Creates a space. |
| `POST /wiki/rest/api/space/_private` | Creates a space visible only to its creator. |
| `PUT /wiki/rest/api/space/{spaceKey}` | Updates name, description, homepage, type or status. |
| `DELETE /wiki/rest/api/space/{spaceKey}` | Permanently deletes the space as a long task; returns `202`. |
| `GET\|PUT /wiki/rest/api/space/{spaceKey}/settings` | `routeOverrideEnabled` and `contentMode` (`standard` or `compact`). |
| `GET\|PUT\|DELETE /wiki/rest/api/space/{spaceKey}/theme` | The space's selected theme. |
| `GET /wiki/rest/api/settings/theme…` | The available themes and the selected theme. |
| `GET /wiki/api/v2/data-policies/spaces` | Whether a data policy blocks content in each space. Always `false`; apps only. |

## Behavior

- **Personal spaces.** A key of the form `~accountId` creates a `personal`
  space owned by that account. The key must name a real account.
- **Private create.** `_private` creates a `global` space. Every permission
  goes to the creator as a [direct grant](SPACE_PERMISSIONS.md) and nobody
  else gets any. It does not create a personal space.
- **Update.**
  - **Allowed fields.** Only name, description, homepage, type and status can
    change. Permissions cannot be changed here.
  - **Homepage.** Must be a current page in the same space.
  - **Type and status.** Unknown values are 400.
- **Delete.** Queues a task, which `/wiki/rest/api/longtask/{id}` reports.
  - **What the task removes.** Everything in the space: content, page
    versions and pages first, then the space itself, which cascades to the
    rest.
  - **Trash.** Nothing goes to the trash.
- **Settings and theme.** Reading needs only view permission. Changing them
  needs space administration. A space with no theme set reports none (it
  inherits the [site look and feel](SITE_SETTINGS.md)); it does not report a
  default theme.

## Space role mode

`GET /wiki/api/v2/space-role-mode` reports how the site governs spaces, based
on what exists:

| Mode | The site has |
| --- | --- |
| `PRE_ROLES` | Direct grants and no role assignments. |
| `ROLES_TRANSITION` | Both direct grants and role assignments. |
| `ROLES` | Role assignments only. |

A site whose only change is a private space created with `_private` reports
`PRE_ROLES`. See [roles](CONFLUENCE_SPACE_ROLES.md) and
[transition](SPACE_PERMISSION_TRANSITION.md).

## Permissions

Update, delete, settings changes and theme changes need space
administration. Workspace administrators administer every space.

## Gaps

Tracked in [PLAN.md](../PLAN.md).

- **Space trash.** A deleted space is not moved to a space trash, so it
  cannot be restored.
- **Space icons.** Only the default icon is available; custom icons cannot be
  uploaded.
- **Stored-only settings.** `routeOverrideEnabled` (alias URLs),
  `contentMode` and the selected theme are stored and reported, but the
  browser UI does not use them.
- **Space management UI.** Spaces cannot be renamed, deleted or given a new
  description in the browser.

## Tests

`internal/confluence/space_lifecycle_test.go`

## See also

[CLOUD_PARITY.md](CLOUD_PARITY.md)
