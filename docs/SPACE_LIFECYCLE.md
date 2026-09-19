# Confluence space lifecycle (v1)

The v1 space API creates, updates and deletes spaces, and also serves each
space's settings and theme. The browser has the same lifecycle: archive,
restore, delete to the trash, restore from the trash and delete permanently.
Part of [Confluence](CONFLUENCE_SITE_SURFACES.md). v2 listing and creation are
in [CONFLUENCE_SPACES.md](CONFLUENCE_SPACES.md). Code:
`internal/confluence/space_lifecycle.go`,
`internal/store/wiki_space_lifecycle.go`, `internal/web/wiki_space_tools.go`,
migration 151.

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
  - **Trash.** Nothing goes to the trash. Confluence documents this endpoint
    as the one that "permanently deletes a space without sending it to the
    trash"; the browser's delete is the one that uses the trash.
- **Settings and theme.** Reading needs only view permission. Changing them
  needs space administration. A space with no theme set reports none (it
  inherits the [site look and feel](SITE_SETTINGS.md)); it does not report a
  default theme.

## The space trash

The browser's delete is not the API's. Confluence sends a deleted space to the
trash — "when you delete a space, it goes to trash rather than being
immediately removed" — and keeps everything in it until someone decides.

| Form | Who | What happens |
| --- | --- | --- |
| `POST /wiki/spaces/{space}/status` | Space administrators | Sets the status to `archived`, or back to `current`. Refused for a space in the trash. |
| `POST /wiki/spaces/{space}/trash` | Space administrators | Sets the status to `trashed`. Nothing in the space is removed. |
| `POST /wiki/spaces/{space}/restore` | Site administrators | Sets the status back to `current`. |
| `POST /wiki/spaces/{space}/purge` | Site administrators | Queues the same `wiki-delete-space` task the API's delete queues. |

- **Who acts on the trash.** Sending a space there needs space
  administration, because Confluence asks for "space admin permissions to
  send a space to the trash". Taking it out again, or removing it for good,
  needs a site administrator, because "once trashed, a space can only be
  restored or permanently deleted by a Confluence admin". A space
  administrator who is not a site administrator is refused both.
- **What a trashed space loses.** It leaves the space directory and every
  search, including a search that asks for archived spaces. Its pages, blog
  posts and everything else stay exactly where they were, so restoring
  "will immediately return the space and all of its content".
- **Reading the trash.** `GET /wiki?status=trashed` is a site
  administrator's list of every trashed space in the site, including spaces
  they are not a member of. Anyone else is refused. Restoring and permanently
  deleting reach those spaces too, so a site administrator is never locked out
  of the trash by a space's own permissions.
- **Permanent delete.** Runs as the long task, so the space disappears from
  the trash once the task finishes. It cannot be undone.
- **Not a timer.** Confluence empties its trash 60 days after a space lands
  there. Nothing here does; a trashed space waits until someone acts.

## The space directory

`GET /wiki` lists one status at a time, because an archived space
"won't appear in … the general list of spaces in the Space Directory. Instead,
they'll appear in the Archived Spaces list."

| Query | List |
| --- | --- |
| none, or `?status=current` | The spaces the reader can see. |
| `?status=archived` | The archived spaces the reader can see. |
| `?status=trashed` | Every trashed space, for site administrators only. |

An unknown status is 400. The trash tab is offered only to site
administrators.

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

## Browser

A space's own page carries **Space details** for its administrators: the name,
the description and which current page the space opens on. The form posts to
`POST /wiki/spaces/{space}/details`, which resolves the space the way the
other administration forms do -- as an administrator, not through the
visibility gate -- so a space whose grants name somebody else stays
administrable by the people who own it.

## Permissions

Update, delete, settings changes and theme changes need space
administration. Workspace administrators administer every space.

## Gaps

Tracked in [PLAN.md](../PLAN.md).

- **Trash expiry.** A space waits in the trash until someone acts; nothing
  empties the trash after 60 days.
- **Space icons.** Only the default icon is available; custom icons cannot be
  uploaded.
- **Stored-only settings.** `routeOverrideEnabled` (alias URLs),
  `contentMode` and the selected theme are stored and reported, but the
  browser UI does not use them.

## Tests

`internal/confluence/space_lifecycle_test.go`,
`internal/store/wiki_space_trash_test.go`,
`internal/web/wiki_space_trash_test.go`

## See also

[CLOUD_PARITY.md](CLOUD_PARITY.md)
