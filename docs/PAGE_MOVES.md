# Moving, copying and archiving pages

Updated: 2026-09-11

Confluence relocates and duplicates whole page trees: a page moves among its
siblings or under a new parent, one page or a whole hierarchy is copied,
pages are archived, and a tree goes to the trash.

## Pages had no order to move within

Children were returned in id order, so "move this page before that one" had
nothing to act on. A move would have answered 200 and changed nothing — the
failure that is hardest to notice, because the status code looks right.

Pages now carry a position, seeded from the id order readers were already
seeing, and every page listing follows it. The test asserts the order a reader
gets after each move rather than the status code.

## Jira Cloud REST surface

All seven pinned operations are implemented. An audit against a running server
found none of them working.

| Method and path | Behavior |
|---|---|
| `PUT /wiki/rest/api/content/{pageId}/move/{position}/{targetId}` | Moves a page `before`, `after`, `append` (under the target) or `above` (under it, first). |
| `POST /wiki/rest/api/content/{id}/copy` | Copies one page, answering with the copy. |
| `POST /wiki/rest/api/content/{id}/pagehierarchy/copy` | Queues a copy of a page and its descendants. |
| `POST /wiki/rest/api/content/archive` | Queues archiving a list of pages. |
| `DELETE /wiki/rest/api/content/{id}/pageTree` | Queues trashing a page and everything beneath it. |
| `GET /wiki/rest/api/longtask` and `/longtask/{id}` | Reports the background operations. |

## What runs in the background, and why

Copying one page answers immediately: it is one row and its belongings. Copying
a hierarchy, archiving a list and trashing a tree can each touch a great many
pages, so Confluence accepts them with `202` and a task, and so does this. They
run on the same durable task queue Jira's bulk operations use rather than a
mechanism of their own, which is why they survive a restart and why the long
task reads have something real to report.

The long task reads are part of this checkpoint rather than a later one: an
operation that answers `202` and a task id is not usable until the task can be
read back.

## What a move refuses

**An unknown position.** Confluence's four are the whole vocabulary.

**Moving a page relative to itself**, and **moving a page beneath its own
descendant** — that would leave the subtree with no root and the page would
vanish from the space.

**A target in another space.** Moving between spaces is a different operation
with different permissions.

## What travels with a copy

The caller chooses: attachments, properties and labels. A copy made to start a
new document wants the text and not last quarter's attachments, so none of them
are implied. An attachment's bytes are content-addressed, so a copied
attachment points at the same stored blob rather than duplicating it.

A space shows one current page per title, so a copy landing beside its source is
renamed — "Alpha (2)" — rather than failing on the collision. `titleOptions` on
a hierarchy copy applies a prefix or a search and replace to every page in the
tree, which is what makes a copied hierarchy distinguishable from the original.

## Archiving

Archiving is a status of its own: an archived page keeps its history and leaves
the space's current content. Trashing a tree takes the descendants with it;
archiving does not, because Confluence archives the pages it was given.

## Evidence and current boundary

- `internal/confluence/page_moves_test.go` covers all seven operations, all four
  move positions **by the order a reader then sees**, every refusal above, the
  renamed copy, the hierarchy copy landing under its destination with its title
  prefix, a hierarchy copy into itself failing with the task saying so,
  archiving taking a page out of the space, trashing a tree taking its
  descendants, and the long task list and its 404.
- `migrations/147_page_moves.sql` is exercised from a clean PostgreSQL schema.

Copying permissions and custom contents with a page, moving a page between
spaces, restoring an archived page, `expand` on the copy response, and the
browser journeys for these remain.
