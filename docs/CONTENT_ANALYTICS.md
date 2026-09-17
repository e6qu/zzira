# Content analytics

View counts for pages and blog posts: how many times content was opened, and by how many distinct people. Part of [Confluence](CONFLUENCE_SITE_SURFACES.md); status in [CLOUD_PARITY.md](CLOUD_PARITY.md).

## API

| Method | Path | Counts |
| --- | --- | --- |
| `GET` | `/wiki/rest/api/analytics/content/{contentId}/views` | every view |
| `GET` | `/wiki/rest/api/analytics/content/{contentId}/viewers` | distinct viewers |

```json
{ "id": 42, "count": 17 }
```

- `id` is a number, as Confluence's analytics schema declares it.
- `fromDate` counts from a moment: a date (`2026-09-01`, start of that day in UTC) or an RFC 3339 timestamp. Any other value is 400.
- A `contentId` that is not a positive integer is 404.

## UI

`/wiki/spaces/{space}/analytics` lists the space's current pages and blog posts the reader can see, ranked by views, with viewers, over a 7, 30 (default) or 90 day window. It shows the top 50 and the total views.

## What counts as a view

A view is recorded, after a successful read of **current** content, when:

- a page or blog post is opened in the product (not in the editor), or
- a single page or blog post is read through `GET /wiki/api/v2/pages/{id}` or `GET /wiki/api/v2/blogposts/{id}`.

Nothing else records a view: writes, listings, search results, analytics reads, drafts, archived or trashed content, and refused reads. Reading a page three times is three views and one viewer. A failure to record a view is logged and never fails the read.

## Visibility

Analytics are exactly as visible as the content. A reader who cannot open the content (space permission, page restriction, someone else's draft) gets 404.

A bare id is resolved by what exists (page first, then blog post), and then read through that content's own visibility rules. A page the caller may not open is 404, never a blog post that shares its id. Content ids are unique across content kinds; see [Confluence site surfaces](CONFLUENCE_SITE_SURFACES.md).

Only pages and blog posts have analytics. Other content (custom content, attachments, comments, folders) is 404.

## Storage

`wiki_content_views` holds one row per view (workspace, content type, content id, user, time). Counts are computed from those rows on each read, so they are exact and current and `fromDate` can be any moment.

## See also

[Content tree](CONTENT_TREE.md), [page writing](PAGE_WRITING.md), [space permissions](SPACE_PERMISSIONS.md).
