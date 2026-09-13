# Content analytics

Confluence keeps two numbers for every piece of content: how many times it was
viewed, and how many distinct people viewed it.

| Method | Path | Reports |
| --- | --- | --- |
| `GET` | `/wiki/rest/api/analytics/content/{contentId}/views` | every view |
| `GET` | `/wiki/rest/api/analytics/content/{contentId}/viewers` | each person once |

```json
{ "id": 42, "count": 17 }
```

`id` is the content id as a number, which is how Confluence's analytics shape
declares it. `count` is the number asked for.

`fromDate` counts from a moment rather than from the beginning. It accepts a date
such as `2026-09-01`, meaning the start of that day in UTC, or a timestamp such
as `2026-09-01T09:30:00Z`. Anything else is refused rather than read as "ever".

## What a view is

A view is one person opening one piece of content. It is recorded when:

- a page or blog post is opened to read in the product, and
- a single page or blog post is read through `GET /wiki/api/v2/pages/{id}` or
  `GET /wiki/api/v2/blogposts/{id}`.

It is not recorded when:

- content is created or edited — writing it is not reading it;
- content appears in a listing or a search result — it was shown, not opened;
- the analytics themselves are read;
- the content is a draft, trashed or archived — only published content is
  viewed in the sense these numbers report;
- the read was refused. A view is recorded only after the content was read
  successfully, so nobody accumulates views on content they could not open.

Every open is a view, so reading a page three times is three views and one
viewer. That is the difference between the two numbers.

## Visibility

The analytics for a piece of content are exactly as visible as the content. A
reader who may not open a page — because of space permissions, a page
restriction, or because it is someone else's draft — gets a 404 from its
analytics, the same answer the page itself gives. The numbers never confirm that
content exists to someone who cannot see it.

## Content ids

A bare id in the analytics path is resolved by what exists, not by what the
caller may see: a page first, then a blog post. The resolved content is then read
through its own visibility rules, so a page the caller may not open is a 404 —
never a blog post that happens to share its id. New content ids are unique across
every kind of content since migration 158; ids created earlier can still collide,
which is exactly the case that rule protects. See
[CONFLUENCE_SITE_SURFACES.md](CONFLUENCE_SITE_SURFACES.md#content-ids-are-unique-across-content).
An id that is not a positive number names no content and is a 404.

## Storage

`wiki_content_views` holds one row per view: the workspace, the content type and
id, who viewed it, and when. Views are counted from those rows on each read, so
`fromDate` can be any moment rather than one of a few fixed windows.

## Boundary

- Views are counted for pages and blog posts. Custom content, attachments and
  comments are not opened as pages are, and record no views.
- Views made before this change were never recorded, so content that existed
  earlier counts from when recording began.
- Counting reads the rows directly. There is no rollup, so the numbers are exact
  and current rather than refreshed on a schedule.
