# Moving, copying and archiving pages

Reordering and moving pages, copying a page or a hierarchy, archiving, trashing a tree, and the long tasks that report background work. Part of [Confluence](CONFLUENCE_SITE_SURFACES.md); status in [CLOUD_PARITY.md](CLOUD_PARITY.md). Moving non-page nodes is in [content tree](CONTENT_TREE.md).

## API

| Method and path | Behavior |
| --- | --- |
| `PUT /wiki/rest/api/content/{pageId}/move/{position}/{targetId}` | Moves a page `before` or `after` the target, or beneath it (`append`: last, `above`: first). The target may be any tree node. |
| `POST /wiki/rest/api/content/{id}/copy` | Copies one page; answers the copy as v1 content with `expand` (at most 8). |
| `POST /wiki/rest/api/content/{id}/pagehierarchy/copy` | Queues a copy of a page and its descendants (202). |
| `POST /wiki/rest/api/content/archive` | Queues archiving a list of pages (202). |
| `DELETE /wiki/rest/api/content/{id}/pageTree` | Queues trashing a page and its descendants (202). |
| `GET /wiki/rest/api/longtask`, `/longtask/{id}` | Background task status. |

Queued operations answer `202` with a task `id` and a `links.status` URL. They run on the durable task queue shared with Jira bulk operations, so they survive a restart. A failed task reports why (for example, a hierarchy copied into itself).

Pages carry a stored `position`; every page listing follows it.

## Move rules

Refused (400):

- an unknown position;
- moving a page relative to itself, or beneath its own descendant;
- moving a space homepage to another space;
- a title the destination space already has as a current page.

A move to another space takes the whole subtree, including folders, whiteboards, databases, Smart Links and custom content under those pages. It needs permission to delete pages in the source space and add pages in the destination (403 otherwise). Every moved page and node is recorded with its new space, so space-filtered replicas receive it.

## Copying

- **Destination**: `destination` of type `space_key`, `parent_page` or `existing_page`.
- **Options**, all off by default: `copyAttachments`, `copyProperties`, `copyLabels`, `copyPermissions` (restrictions, copied as they are), `copyCustomContents` (custom content directly under the page, restarted at version 1).
- **Body**: `body.storage` replaces the copied body; `pageTitle` sets the title.
- **Title clash**: a copy landing where its title is taken is renamed "Title (2)", "Title (3)", and so on.
- **Hierarchy copy**: `titleOptions` applies a `prefix`, or a `search` and `replace`, to every copied title.
- **Attachments**: copied attachments reference the same stored blob (content-addressed).

## Archiving and restoring

- An archived page keeps its history, leaves the page tree, and is read-only until restored. It can still be trashed.
- Archiving a page alone lifts its children one level; archiving with descendants archives the subtree. Trashing a tree always takes the descendants.
- Restoring puts a page under its parent if the parent is current, otherwise at the top of the space. Archived children come back when asked. A restore is refused while another current page has the same title.
- The REST archive is queued; the page view's action is immediate. Both use the same store operation.

## Status filters

- `GET /wiki/api/v2/pages` lists `current` and `archived` by default and accepts `current`, `archived`, `trashed`, `deleted`, and `draft` (only without a space). `deleted` pages are returned only to that space's administrators (see [drafts and deletion](CONTENT_DRAFTS.md)).
- Attachment lists default to `current` and `archived` and accept `trashed`. An attachment reports `archived` while its page is archived.
- A single page read accepts every status. An earlier version reads back as `historical`.

## UI

The page view offers **Archive**, **Restore** and **Move** (`POST /wiki/spaces/{space}/pages/{page}/archive|restore|move`). The space page has an **Archived** tab.

## Tests

- `internal/confluence/page_moves_test.go`
- `internal/confluence/page_lifecycle_test.go`
- `e2e/wiki_page_lifecycle.spec.ts`

## See also

[Content tree](CONTENT_TREE.md), [page writing](PAGE_WRITING.md), [space lifecycle](SPACE_LIFECYCLE.md), [Confluence operations](CONFLUENCE_OPERATIONS.md).
