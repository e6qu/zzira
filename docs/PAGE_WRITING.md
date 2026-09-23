# Writing and reading pages

Page creation and placement, body formats, live docs, private pages, ownership, read flags, the v1 content bean, and Smart Link and archived content views. Part of [Confluence](CONFLUENCE_SITE_SURFACES.md); status in [CLOUD_PARITY.md](CLOUD_PARITY.md).

## Where a new page goes

`POST /wiki/api/v2/pages` without `parentId` puts the page beneath the space homepage. `root-level=true` puts it at the space root and cannot be combined with `parentId` (400). In a space without a homepage the page goes to the root either way. `parentId` may be any tree node (see [content tree](CONTENT_TREE.md)).

## Body formats

A body is written flat (`representation`, `value`) or nested under its format (`{"storage": {...}}`). The site stores `storage`:

- `storage` is kept as written;
- `atlas_doc_format` and `wiki` are converted to storage.

A nested body naming two formats, or an unsupported representation, is 400.

Wiki markup (`internal/wikimarkup/notation.go`) supports `h1.` to `h6.`, `*bold*`, `_emphasis_`, `-strike-`, `+underline+`, `{{monospace}}`, nested `*`/`#` lists, `[title|url]` links, `{code}`, `{noformat}`, `{quote}`, `bq.`, `----`, and `||header||`/`|cell|` tables. Text is escaped before markup is applied, and links are limited to `http`, `https` and `mailto`. The [body conversion](CONTENT_HISTORY.md#conversions) operations accept wiki markup too.

Read formats (`body-format`):

- single page: `storage`, `atlas_doc_format`, `view`, `export_view`, `anonymous_export_view`, `styled_view`, `editor`;
- page lists: `storage`, `atlas_doc_format`.

## The editor

`/wiki/spaces/{space}/pages/new` and the edit view write a page in a rich
editor: a box that holds the page as it will read, with a toolbar above it.
The toolbar sets the block the caret is in -- paragraph, heading 2, 3 or 4, a
quote or a code block -- makes text bold or emphasised, starts a bullet or
numbered list, adds a link from an address typed beside the button, inserts a
table with a header row and a row under it, and mentions somebody. What the
editor holds is written back as storage when the page is saved, so the
site keeps the same format however a page was written.

- **Source mode** swaps the editor for the storage itself, which is how a page
  carrying markup the toolbar does not write is edited.
- **A link address** is held to `http`, `https` or `mailto`, because a link in
  a page is a link every reader can follow.
- Code: `web/static/wiki.js`, `internal/wikimarkup/storage.go` (the tags a
  page may carry). Browser test: `e2e/wiki_editor.spec.ts`.

## Live docs

`subtype: "live"` on create makes a live doc. Live docs are always published: creating one as a draft, or later saving it as a draft, is 400. The subtype cannot change after creation (400). `GET /pages?subtype=live` lists live docs; `subtype=page` excludes them. The editor offers **Live doc** on a new page, and the page view labels it.

## Private pages

`private=true` creates a page only its creator can view and edit, by writing view and edit restrictions for the creator in the same transaction as the page. `embedded=true` is accepted and has no effect (the site has a single page store).

## Owners

The author owns a page until ownership is transferred. `PUT /pages/{id}` with `ownerId` gives it to another site member, and the page reports `ownerId` and `lastOwnerId`. The page view shows the owner; anyone who can edit the page can change it from **Page controls** (`POST /wiki/spaces/{space}/pages/{page}/owner`). Changing the owner writes no version.

## Read flags

`GET /wiki/api/v2/pages/{id}` accepts:

- `include-collaborators`: everyone who wrote a version, in order of their first version;
- `include-favorited-by-current-user-status`: the reader's star. Pages are starred from the page view and listed under **Starred** on the wiki home;
- `include-webresources`: the stylesheets and script used to render page content;
- `include-direct-children`: the page's direct children;
- `include-labels`, `include-properties`, `include-operations`, `include-likes`, `include-versions`, `include-version`, `get-draft`, `status`, `version`.

## The v1 content bean

The v1 page copy (`POST /wiki/rest/api/content/{id}/copy`) answers v1 content with the parts named in `expand` (at most 8): `space`, `container`, `version`, `history`, `body.*` in any primary format, `ancestors`, `metadata.labels`, `metadata.properties`, `operations`, `restrictions`, `childTypes`, `children.*`, `descendants.*`. Unexpanded parts are listed under `_expandable`.

`GET /wiki/rest/api/content/{id}/descendant` expands pages, comments, attachments, folders, whiteboards, databases and Smart Links, and names the rest under `_expandable` with links to the typed read where Confluence has one. The typed read (`/descendant/{type}`) counts depth by the content hierarchy: a page's comments and attachments are one level below it, a reply one level below its comment, and a child page's comments and attachments one level below that child.

## Attachments

Attachments report Confluence's kind description (`PDF Document`, `PNG Image`) in v2 and in the v1 extensions. `include-collaborators` on an attachment lists everyone who uploaded a version. See [attachments](ATTACHMENTS.md).

## Smart Links and archived content in the product

A Smart Link opens as a card showing its destination, with a sandboxed preview when the destination uses HTTPS. Archived whiteboards and databases open read-only, with **Restore** for readers allowed to restore them.

## Tests

- `internal/confluence/page_formats_test.go`
- `internal/wikimarkup/notation_test.go`
- `e2e/wiki_page_details.spec.ts`

## See also

[Drafts and deletion](CONTENT_DRAFTS.md), [page moves](PAGE_MOVES.md), [content templates](CONTENT_TEMPLATES.md), [space permissions](SPACE_PERMISSIONS.md), [presence and live editing](CONFLUENCE_LIVE.md).
