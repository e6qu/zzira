# Writing and reading pages

Updated: 2026-09-14

A Confluence page is more than a storage body under a title. It is written in
one of three formats, lands beneath the space homepage unless told otherwise,
may be a live doc, may be private to its creator, has an owner who can hand it
on, and reads back in any of Confluence's primary formats with its
collaborators, the reader's star and the resources needed to render it. The v1
content bean describes all of that on request.

## Where a new page goes

`POST /pages` without a `parentId` puts the page beneath the space homepage, as
Confluence documents. `root-level=true` puts it at the root of the space
instead, and refuses a `parentId` alongside it. A space without a homepage
takes the page at its root either way.

## Body formats

A body is written flat (`representation` and `value`) or nested under the name
of its one format (`{"storage": {...}}`). Storage is kept as written; the
document format (`atlas_doc_format`) and wiki markup (`wiki`) are converted to
storage, which is what the site keeps. A nested body naming two formats, or a
representation Confluence does not accept, is refused.

Wiki markup had no converter at all. `internal/wikimarkup/notation.go` reads
Confluence's notation — `h1.`–`h6.` headings, `*bold*`, `_emphasis_`,
`-strike-`, `+underline+`, `{{monospace}}`, nested `*`/`#` lists,
`[title|url]` links, `{code}`/`{noformat}` blocks, `{quote}`, `bq.`, `----`
and `||header||`/`|cell|` tables. Text is escaped before any markup is
applied, so the notation can only produce the elements it names, and links go
only to `http`, `https` and `mailto` destinations. The body conversion
operations accept wiki markup as a source for the same reason.

A single page reads back as `storage`, `atlas_doc_format`, `view`,
`export_view`, `anonymous_export_view`, `styled_view` or `editor`; page lists
as `storage` or `atlas_doc_format`, which are the formats Confluence lists
them in.

## Live docs

A page created with `subtype: "live"` is a live doc: always published, so a
live doc draft is refused on create and a live doc cannot be turned into a
draft later. Its subtype is fixed when it is created. `GET /pages?subtype=live`
lists live docs and `subtype=page` leaves them out. The editor offers a live
doc when creating a page, and the page calls itself one.

## Private pages

`private=true` creates a page only its creator can view and edit — a view
restriction and an edit restriction naming the creator, written in the same
transaction as the page, so there is no moment when anyone else can see it.
`embedded=true` is accepted: it tells Confluence's collaborative editor where
to keep the page, and this site has one place for pages.

## Owners

A page is owned by its author until ownership is handed on. `PUT /pages/{id}`
with an `ownerId` gives the page to another member of the site, and the page
reports `lastOwnerId`. The page view names the owner, and anyone who can edit
the page can change it from Page controls; that change writes no new version,
because changing hands is not an edit.

## What a page read can include

- `include-collaborators` lists everyone who has written a version, in the
  order they first did.
- `include-favorited-by-current-user-status` reports the reader's star. Pages
  are starred from the page view and listed under Starred on the wiki home.
- `include-webresources` names the stylesheets and script this site renders
  page content with.

## The v1 content bean

The v1 copy answers the copy as v1 content with the parts named in `expand`
(at most eight): `space`, `container`, `version`, `history`, `body.*` in any
primary format, `ancestors`, `metadata.labels` and `metadata.properties`,
`operations`, `restrictions`, `childTypes`, `children.*` and `descendants.*`.
Everything not expanded is listed under `_expandable`.

The v1 descendant read lists each kind it is asked to expand — pages,
comments, attachments, folders, whiteboards, databases and Smart Links — and
names the rest under `_expandable`, linking to the typed read where Confluence
has one. The typed read counts depth the way the content hierarchy does: a
page's comments and attachments are one level beneath it, a reply one level
beneath the comment it answers, and a child page's own comments and
attachments one level beneath that page.

## Attachments

Attachments carry Confluence's description of their kind (`PDF Document`,
`PNG Image`) in v2 and in the v1 extensions, and `include-collaborators` lists
everyone who uploaded a version.

## Smart Links and archived content in the browser

A Smart Link opens as a card showing where it points, with a preview when the
destination is served over HTTPS, since a sandboxed frame is the only way to
show another site inside this one. Archived whiteboards and databases still
open, read-only, with Restore where the reader may restore them.

## Evidence

- `internal/confluence/page_formats_test.go` covers default and root-level
  parenting, wiki markup and document-format bodies, refused body forms, every
  reading format, live docs and their filters, private pages, ownership and
  its refusal, collaborators, stars, web resources, the v1 copy expansions and
  their limit, the v1 descendant expansions and typed reads with depth, and
  attachment descriptions and collaborators.
- `internal/wikimarkup/notation_test.go` covers the notation, escaping, unsafe
  links and that the result is valid storage.
- `e2e/wiki_page_details.spec.ts` creates a live doc, stars it, finds it under
  Starred, changes its owner, opens a Smart Link card, and archives a
  whiteboard, opens it read-only and restores it.
