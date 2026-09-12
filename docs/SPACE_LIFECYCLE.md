# The Confluence space lifecycle

Updated: 2026-09-12

A space is created, renamed, given a homepage, archived and deleted. It carries
per-space settings, may select a theme, and is either global or personal.

## Jira Cloud REST surface

All eleven pinned operations are implemented. An audit against a running server
found none of them working.

| Method and path | Behavior |
|---|---|
| `POST /wiki/rest/api/space` | Creates a space. |
| `POST /wiki/rest/api/space/_private` | Creates one visible to its creator alone. |
| `PUT /wiki/rest/api/space/{spaceKey}` | Renames it, or changes its description, homepage, type or status. |
| `DELETE /wiki/rest/api/space/{spaceKey}` | Deletes it permanently, reporting through a long task. |
| `GET/PUT /wiki/rest/api/space/{spaceKey}/settings` | The space's settings. |
| `GET/PUT/DELETE /wiki/rest/api/space/{spaceKey}/theme` | The theme it selected. |
| `GET /wiki/api/v2/data-policies/spaces` | Whether a data policy blocks content in each space. |

## Personal spaces

A personal space belongs to one person and is keyed `~accountId`, which is how
Confluence keys them. Creating a space with such a key makes a personal one; the
space bean reports its real type rather than always saying `global`.

This also completes the permission transition delivered previously: its
`PERSONAL` and `ALL_EXCEPT_PERSONAL` space selections now select the spaces they
name. They were refused before, because personal spaces did not exist to select.

## A private space is not a personal one

Confluence describes the private create as the ordinary create with permissions
set to the current user only. So it produces a **global** space whose every
permission is granted to its creator and to nobody else — not a personal space.
The test checks that another member cannot see it.

## The space role mode is read from the site

`GET /space-role-mode` reports what the site actually holds: direct grants and
no role assignments is a site that has not started (`PRE_ROLES`); both is a site
part-way through (`ROLES_TRANSITION`); only roles is a site that has finished
(`ROLES`). Creating a private space writes direct grants, so a site that has
done nothing else reports `PRE_ROLES`.

## Deleting takes everything in the space

Confluence deletes a space in a long running task, so this answers `202` with
the task and the client follows `/longtask/{id}`. The first probe found that
read returning 404: the long task listing knew only about the page operations,
so the delete pointed at a task nothing would report.

The delete itself then failed on a foreign key. Most of a space's belongings
cascade, but its pages do not, and content and page versions hold the pages in
place — so the task removes those in order before the space. "Permanently
deletes a space" means the content goes with it.

## What the update accepts

Name, description, homepage, type and status. A homepage must be a current page
**in that space**, or the space would point somewhere its readers cannot follow.
Permissions are not updatable here, which is Confluence's own rule.

## Evidence and current boundary

- `internal/confluence/space_lifecycle_test.go` covers all eleven operations,
  the duplicate and malformed key, the private space another member cannot see,
  the personal space's key and type, the refused homepage outside the space and
  the refused status and type, that reading settings needs only view while
  changing them needs administration, that a space with no theme reports none
  rather than a default, the role mode read from the site, and the delete task
  being reported as finished by the long task read.
- `internal/confluence/permission_transition_test.go` now covers the personal
  space selection reaching only personal spaces.
- `migrations/151_space_lifecycle.sql` is exercised from a clean PostgreSQL
  schema.

Space icons beyond the default, the `alias` route override, restoring a deleted
space from the trash, and the global look-and-feel settings the theme reads
inherit from remain.
