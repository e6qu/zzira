# Moving, copying and archiving pages

Updated: 2026-09-14

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

**Moving a space's homepage to another space**, and **a title the destination
already shows** — a space holds one current page per title, so the move is
refused rather than leaving two.

## Moving to another space

A target in another space is allowed, as it is in Confluence. The page takes its
whole subtree along, and with it the folders, whiteboards, databases and Smart
Links that hang off those pages; leaving them behind would strand content in a
space whose page tree no longer holds its parent. It needs permission to delete
pages in the space the tree leaves and to add pages in the space it joins.

Every moved page, and every moved piece of content, is recorded with the space
it is now in. Replicas receive wiki content filtered by space, so an action
that did not say which space it belonged to reached nobody. Page moves, copies,
archiving and trashing all used to record such actions; they now record the
page itself.

## What travels with a copy

The caller chooses: attachments, properties and labels. A copy made to start a
new document wants the text and not last quarter's attachments, so none of them
are implied. An attachment's bytes are content-addressed, so a copied
attachment points at the same stored blob rather than duplicating it.

A space shows one current page per title, so a copy landing beside its source is
renamed — "Alpha (2)" — rather than failing on the collision. `titleOptions` on
a hierarchy copy applies a prefix or a search and replace to every page in the
tree, which is what makes a copied hierarchy distinguishable from the original.

## Archiving and restoring

Archiving is a status of its own: an archived page keeps its history, leaves
the page tree and is read-only until it is restored, though it can still go to
the trash. Trashing a tree takes the descendants with it; archiving takes them
only when asked. A page archived on its own lifts its children a level, which
is what Confluence does, so they stay in the tree rather than hanging off a
page nobody can see there.

Restoring puts a page back under its parent when that parent is still current,
and at the top of the space when it is not. Archived children come back with it
when asked. A restore is refused while the space shows another current page
with the same title.

The page view offers Archive, Restore and Move, and the space lists its archived
pages under their own tab. The REST archive is queued and the page's own
action is immediate; they share the same store operation.

## Status filters

Confluence's v2 page and attachment collections list `current` and `archived`
unless asked for others, and so do these. `deleted` is accepted and matches
nothing, since a purged page is gone. A single page read accepts the whole
status vocabulary; an earlier version reads back as `historical`. An attachment
reports `archived` while the page it belongs to is archived.

## Evidence and current boundary

- `internal/confluence/page_moves_test.go` covers all seven operations, all four
  move positions **by the order a reader then sees**, every refusal above, the
  renamed copy, the hierarchy copy landing under its destination with its title
  prefix, a hierarchy copy into itself failing with the task saying so,
  archiving taking a page out of the space, trashing a tree taking its
  descendants, and the long task list and its 404.
- `migrations/147_page_moves.sql` is exercised from a clean PostgreSQL schema.

- `internal/confluence/page_lifecycle_test.go` covers the default and filtered
  listings, the `deleted` and `historical` statuses, attachment statuses, an
  archived page refusing edits, restore refusing a current title, children
  lifting a level, archiving and restoring a subtree, a restore under an
  archived parent landing at the top, a cross-space move carrying its subtree
  and folder, the replica action carrying the new space, and the homepage and
  title refusals.
- `e2e/wiki_page_lifecycle.spec.ts` archives a page in the browser, finds it in
  the Archived tab, restores it and moves a page to another space.

Copying permissions and custom contents with a page, `expand` on the copy
response, and moving a page to the top level of a space (the REST move always
names a target page) remain.
