# The Confluence content tree

Updated: 2026-09-14

Confluence keeps pages, folders, whiteboards, databases and Smart Links in one
content tree. Any of them can hold any other, siblings of every kind share one
order, and moving, archiving and restoring a node work the same whatever it is.

## Two trees that should have been one

Content could sit beneath a page, but a page could not sit beneath a folder,
and content had no stored order: its position was counted from its id among
siblings of its own table. A folder holding a page and a whiteboard had nothing
to say which came first, and a page's children and a folder's children were
answered by different code that each saw half the tree.

Pages may now have a folder, whiteboard, database or Smart Link as their
parent, content has a stored position, and the migration seeds one order per
parent across both tables — pages first, in the order readers were already
seeing, then content in id order. `wiki_tree_nodes` is a view over both
tables; content ids are unique across pages and content, so a parent id alone
names its node.

## Reads

Every kind answers `direct-children`, `descendants` and `ancestors` from the
same tree, so a folder lists the pages inside it and a page lists the folders
beneath it.

- Children and descendants come back in their stored `childPosition` order and
  include archived nodes as well as current ones, which is what the response
  schema's status enum allows.
- A node the reader cannot see hides everything beneath it, as a restricted
  page does.
- `ancestors` answers from the top of the space down. With a `limit`, the
  nearest ancestors are returned, so a caller continues from the highest one it
  received, as Confluence documents; before, the highest were returned and the
  nearest could not be reached.
- A page's older `children` read keeps to child pages. The v1 descendant reads
  report pages, now including pages beneath folders and other content.

## Placing a page beneath content

`POST /pages` and `PUT /pages/{id}` take any node of the tree as `parentId`,
and a page reports the kind of its parent as `parentType`. Private content is
refused as a parent: a page filed beneath a database only its creator can see
would vanish for everyone else. A page that changes parent takes its place at
the end of its new siblings.

## Moving

Any node can move before or after another node of any kind, beneath one (last
with `append`, first with `above`), or to the top level of a space. The v1 page
move takes any node as its target. A node moved to another space takes
everything beneath it along — pages, content, and the custom content hanging
off them — under the same rules pages already followed: permission to delete
that kind of content where it is and to add it where it goes, no space
homepage leaving its space, and no page title the destination already shows.

Content answers to the nearest page above it, whose restrictions decide who can
see it. A move changes which page that is, so it is worked out again for every
node that moved; a database moved out from under a restricted page is no longer
hidden by that page, and one moved beneath it is.

## Archiving, restoring and renaming

Archiving works on any node. On its own, a node's children move up a level so
they stay in the tree; with its contents, the whole subtree is archived. A
restore returns a node beneath its parent when that parent is still current
and to the top of the space otherwise, and refuses when a restored page would
repeat a current title. Archived folders, whiteboards, databases and Smart
Links still read back.

Folders, whiteboards, databases and Smart Links are renamed as a new version.
Content that still holds a page cannot be deleted, as content that holds other
content already could not.

Confluence publishes no REST operation to move, archive or rename a folder,
whiteboard, database or Smart Link, so those are offered where Confluence
offers them: in the space's content tree.

## The space view

The space lists its content tree in one place, with pages, folders,
whiteboards, databases and Smart Links nested in their shared order. Each node
has a Manage menu to move it (beside or beneath another node, or to the top of
the space), archive it with or without its contents, restore it from the
Archived list, and rename it when it is not a page. The per-kind sections name
a node's parent by title rather than by id, the page editor offers content as
a parent, and a page's own Move action lists content and each space's top
level.

## Replicas

Every node a move, archive or restore touches is recorded as it now is, with
the space it is in, so a replica filtered by space sees a folder and the pages
inside it arrive in their new space together.

## Evidence and current boundary

- `internal/confluence/content_tree_test.go` covers a page beneath a folder and
  its `parentType`, mixed children in stored order, the child-page read keeping
  to pages, mixed descendants, ancestors of a page beneath a folder and of a
  database, the limited ancestors read returning the nearest, the v1 page
  descendants beneath a folder, a page moved after a database, the refusals of
  private content as a parent and of deleting a folder that holds a page, a
  folder moved to another space's top level taking its page and database, the
  database no longer answering to its old page, the replica action carrying
  the new space, archiving a folder on its own and with its contents, archived
  folders reading back, restoring, and renaming.
- `e2e/wiki_content_tree.spec.ts` files a page beneath a folder in the browser,
  checks the tree's nesting, renames the folder, moves it to the top of the
  space, archives it with its contents, finds both in the Archived list and
  restores them.
- `internal/confluence/page_moves_test.go` and `page_lifecycle_test.go` keep
  covering the page moves, copies, archive and restore that now run on the
  tree.

Custom content stays outside the tree, as it does in Confluence, and keeps its
own children read. The v1 descendant reads, the Smart Link card and the
archived whiteboard and database views are described in PAGE_WRITING.md.
