# Drafts and deletion

Drafts of pages and blog posts, the three kinds of deletion, blog post reads, and v1 labels on any content. Part of [Confluence](CONFLUENCE_SITE_SURFACES.md); status in [CLOUD_PARITY.md](CLOUD_PARITY.md).

## Drafts of published content

A published page or blog post may have one unpublished draft, shared by everyone who may edit it.

- `PUT /wiki/api/v2/pages/{id}` (or `/blogposts/{id}`) with `status: "draft"` saves the draft. It must be at version 1 (otherwise 400), replaces any existing draft, and leaves the published version unchanged.
- `get-draft=true` reads the draft, reported as `draft` at version 1.
- Publishing (a `PUT` with `status: "current"`, or Save page in the editor) replaces the draft.

UI: the page editor offers **Save as draft** on a published page. The page view then shows an "unpublished draft" notice with **Edit draft** and **Discard draft** (`POST /wiki/spaces/{space}/pages/{page}/draft/discard`). The space's **Your drafts** tab lists the reader's never-published drafts.

## Deletion

`DELETE /wiki/api/v2/pages/{id}` and `/blogposts/{id}`:

| Request | Content status | Effect |
| --- | --- | --- |
| plain | `current` or `archived` | moves to `trashed` |
| `draft=true` | published | discards its waiting draft (404 if none) |
| `draft=true` | never published | removes the content permanently; it never reaches the trash. Author only. |
| `purge=true` | `trashed` | moves to `deleted`. Needs space administration. |

Refusals (400):

- plain delete of a draft: use `draft=true`;
- plain delete of trashed content: use `purge=true`;
- `purge=true` on content that is not trashed;
- `purge=true` and `draft=true` together;
- `draft=true` on a never-published page that still has child pages or content beneath it.

A plain delete of `deleted` content is 404.

Deleted content is visible only to space administrators: they see it in `status=deleted` reads and lists, and a `PUT` with `status: "current"` restores it with its body intact. Everyone else gets 404.

UI: the space's **Trash** tab lists trashed pages; a trashed page offers **Purge page** to space administrators.

## Blog post reads

A blog post reads like a page:

- every primary body format (see [page writing](PAGE_WRITING.md#body-formats));
- an earlier `version`, reported as `historical`;
- every `include-*` flag a page has: labels, properties, operations, likes, versions, collaborators, favourited-by-current-user status, web resources; `include-version=false` omits the version.

Blog posts are written flat or nested, as storage, `atlas_doc_format` or `wiki`, like pages. `GET /wiki/api/v2/blogposts` lists `current` by default and accepts `current`, `trashed` and `deleted`; `sort=id` compares ids numerically.

## v1 labels on any content

- `POST /wiki/rest/api/content/{id}/label` and `DELETE .../label` (or `.../label/{label}`) act on pages, blog posts and attachments, whichever the id names. A prefixed name such as `my:plan` removes that prefix's label.
- `GET /wiki/rest/api/label?name=` lists pages, blog posts and attachments carrying the label, or one kind with `type` (`page`, `blogpost`, `attachment`, `page_template`). Page templates carry no labels here, so `type=page_template` returns nothing.

## Tests

- `internal/confluence/content_drafts_test.go`
- `e2e/wiki_drafts_purge.spec.ts`

## See also

[Moving, copying and archiving pages](PAGE_MOVES.md), [content history](CONTENT_HISTORY.md), [space permissions](SPACE_PERMISSIONS.md).
