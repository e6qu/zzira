# Content tree

Pages, folders, whiteboards, databases and Smart Links share one tree per space. Any of them can hold any other, siblings of every kind share one order, and moving, archiving and restoring work the same for every kind. Part of [Confluence](CONFLUENCE_SITE_SURFACES.md); status in [CLOUD_PARITY.md](CLOUD_PARITY.md).

## Model

- `wiki_pages.parent_id` (a page) or `wiki_pages.parent_content_id` (other content) holds a page's parent; at most one is set.
- `wiki_content.parent_page_id` / `parent_content_id` do the same for folders, whiteboards, databases and Smart Links (`embed`).
- Both tables carry `position`. `wiki_tree_nodes` is a view over both. Content ids are unique across pages and content, so a parent id alone names its node.
- Custom content is not in the tree; it keeps its own children read (see [custom content](CUSTOM_CONTENT.md)).

## Reads

Every kind answers `direct-children`, `descendants` and `ancestors` from the same tree (`/wiki/api/v2/{pages|folders|whiteboards|databases|embeds}/{id}/...`).

- Children and descendants come back in `childPosition` order and include archived nodes as well as current ones.
- A node the reader cannot see hides everything beneath it.
- `ancestors` is ordered from the top of the space down. With `limit`, the **nearest** ancestors are returned; continue from the highest one received.
- `GET /pages/{id}/children` returns child pages only.
- `GET /pages/{id}?include-direct-children=true` includes the direct children.
- The v1 descendant reads (`/wiki/rest/api/content/{id}/descendant[/{type}]`) include pages beneath folders and other content; see [page writing](PAGE_WRITING.md#the-v1-content-bean).

## Placing a page beneath content

`POST /wiki/api/v2/pages` and `PUT /pages/{id}` accept any current tree node in the space as `parentId`; the page reports `parentType`. Private content cannot be a parent (400). A page that changes parent goes to the end of its new siblings.

## Moving

A node moves before or after any node, beneath one (last with `append`, first with `above`), or to the top level of a space. The v1 page move takes any node as its target (see [page moves](PAGE_MOVES.md)).

Refused (400): an unknown position, moving relative to itself, moving beneath its own descendant, and placing non-private content beneath private content.

Moving to another space takes the whole subtree (pages, content and the custom content attached to them) and needs permission to delete that kind of content in the source space and add it in the destination (403 otherwise). A space homepage cannot leave its space, and a page title already used in the destination is refused.

Content's visibility follows the nearest page above it. A move recomputes that page for every moved node, so content moved out from under a restricted page stops being hidden by it, and content moved beneath one becomes hidden.

## Archiving, restoring, renaming, deleting

- Archiving a node alone lifts its children one level; archiving with its contents archives the subtree.
- Restoring puts a node back under its parent if that parent is current, otherwise at the top of the space. A restore that would duplicate a current page title is refused.
- Archived folders, whiteboards, databases and Smart Links still read back; archived whiteboards and databases open read-only in the product.
- Folders, whiteboards, databases and Smart Links are renamed as a new version (1 to 255 characters). Pages are renamed by editing their title.
- Content that still holds a page or other content cannot be deleted.

Confluence has no REST operation to move, archive or rename a folder, whiteboard, database or Smart Link; these are UI actions.

## UI

The space page shows the whole tree, nested in shared order. Each node's **Manage** menu moves it (beside or beneath another node, or to the top of the space), archives it with or without its contents, restores it from the **Archived** list, and renames non-page nodes. Routes: `POST /wiki/spaces/{space}/content/{node}/move|archive|restore|rename`. The page editor offers content as a parent, and a page's **Move** action lists content and each space's top level.

## Replication

Every node a move, archive or restore touches is recorded with its current space, so a replica filtered by space receives a moved folder and the pages inside it together.

## Tests

- `internal/confluence/content_tree_test.go`
- `internal/confluence/page_moves_test.go`, `page_lifecycle_test.go`
- `e2e/wiki_content_tree.spec.ts`

## See also

[Moving, copying and archiving pages](PAGE_MOVES.md), [page writing](PAGE_WRITING.md), [spaces](CONFLUENCE_SPACES.md), [space permissions](SPACE_PERMISSIONS.md).
