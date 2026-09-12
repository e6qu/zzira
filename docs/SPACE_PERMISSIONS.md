# Space permissions

Updated: 2026-09-12

A space says who may do what in it. Confluence has two ways to say it, and this
product had only one.

## Two ways to say it

A **role** gathers permissions and is assigned to people; a **direct grant**
gives one subject one permission. This product started with roles only —
[space roles](../api/conformance/MATRIX.md) were delivered earlier — so the
older permission API had nothing to write to.

Grants exist now, alongside role assignments, and the permission check accepts
either. That is what lets the two APIs describe one space rather than two
models that could disagree about who may open a page.

## Jira Cloud REST surface

All six pinned operations in this family are implemented. An audit against a
running server found none of them working.

| Method and path | Behavior |
|---|---|
| `GET /wiki/api/v2/space-permissions` | The permissions a caller may grant. |
| `GET /wiki/api/v2/space-role-mode` | How this site governs spaces. |
| `POST /wiki/rest/api/space/{spaceKey}/permission` | Grants one permission to one subject. |
| `POST /wiki/rest/api/space/{spaceKey}/permission/custom-content` | Grants several custom content operations at once. |
| `DELETE /wiki/rest/api/space/{spaceKey}/permission/{id}` | Removes a grant. |
| `POST /wiki/rest/api/content/{id}/permission/check` | Whether a subject may act on a piece of content. |

## Confluence has one View permission, not one per content type

Confluence's space permission set has a single **View**, and being able to see
the space is what lets you read what is in it. This product names each read
separately — `read/page`, `read/blogpost`, and so on — because its roles were
built that way.

So a granted `read/space` satisfies every `read/*` permission. Without that, a
client granting Confluence's View would find the person still could not open a
page, and the API would be faithful in shape and wrong in effect.

The first probe caught exactly this: the check said no to someone who had just
been granted View.

## A space that says nothing is open

That was already true of roles: a space with no role assignments is open to its
readers. Grants join the same rule — **no assignments and no grants** means
open. Had grants been left out of it, a space governed only by grants would
have read as open to everyone, which is the opposite of what granting means.

The consequence is worth stating plainly: the first grant closes the space to
everyone it does not name.

## An administrator cannot be locked out

A workspace administrator administers every space, which is the product's
existing rule. The grant operations therefore resolve the space without the
ordinary visibility gate for an administrator — otherwise the first grant would
hide the space from the person configuring it, and nothing could undo it.

## Removing View removes the rest

Confluence documents that removing a subject's View removes all their space
permissions. It does here too: a permission that cannot be reached is not a
permission, and leaving the others behind would show grants that do nothing.

## `access: false` is not a grant

The custom content endpoint takes operations each carrying `access`. An
operation marked `false` describes a permission the app does **not** want, so
granting it would be the opposite of what was asked. The test counts the rows to
prove only the wanted one was written.

## Evidence and current boundary

- `internal/confluence/space_permission_grants_test.go` covers all six
  operations, that the catalogue names the view permission as a prerequisite,
  that an open space allows anyone while a granted one does not, that View
  allows reading but not updating, that a group grant reaches its people, every
  refusal, that `access: false` grants nothing, and that removing View takes the
  subject's other grants with it.
- `migrations/149_space_permission_grants.sql` is exercised from a clean
  PostgreSQL schema.

The five `space-permissions/transition/*` operations remain. They migrate a site
from grants to roles, and now that both models exist they have something real to
move between, which is why they are a checkpoint of their own rather than part
of this one. Confluence's `read/space` on a space the caller cannot see, export
and restriction permissions, and space permissions in the browser journeys also
remain.
