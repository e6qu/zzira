# Custom content

Updated: 2026-09-11

Custom content is content an app defines: a widget, a record, a diagram —
anything with a title, a body and a type the app chose. It lives in a space and
may hang off a page, a blog post, or other custom content.

## It was two tables and could only be read

Custom content existed here as `wiki_page_custom_content` and
`wiki_blog_custom_content`: two tables that could express the same thing twice
and neither of which could put custom content in a space, under other custom
content, or anywhere without a page or blog post above it. There was no create,
no update, no versions, no properties and no labels — only two reads.

It is now stored in `wiki_content`, with everything else that lives in a space.
One model means the hierarchy, permissions, versions and properties are the ones
that already govern a space rather than a second set that would drift from them.
The two reads that existed still answer exactly as they did; the test that
covers them now creates through `POST /custom-content` instead of seeding a row,
so it proves the write and the read agree.

## Jira Cloud REST surface

All 19 pinned operations are implemented. An audit against a running server
found two working — the page and blog post reads.

| Method and path | Behavior |
|---|---|
| `GET /wiki/api/v2/custom-content` | Pages custom content of a type across spaces. |
| `POST /wiki/api/v2/custom-content` | Creates custom content in a space, or under a page, blog post or other custom content. |
| `GET/PUT/DELETE /wiki/api/v2/custom-content/{id}` | Reads, replaces or removes one. |
| `GET /wiki/api/v2/custom-content/{id}/versions` and `/versions/{n}` | Lists the versions, and reads one with the body it had. |
| `GET /wiki/api/v2/custom-content/{id}/properties` and the three `properties/{id}` operations | Entity properties, as on any other content. |
| `GET /wiki/api/v2/custom-content/{id}/labels`, `/operations`, `/children`, `/attachments`, `/footer-comments` | What hangs off it. |
| `GET /wiki/api/v2/spaces/{id}/custom-content` | Pages a space's custom content. |
| `GET /wiki/api/v2/pages/{id}/custom-content` and `/blogposts/{id}/custom-content` | The two reads that already existed. |

## What the write refuses

**Two parents.** `pageId`, `blogPostId` and `customContentId` name three
different parents. Supplying more than one is a contradiction rather than a
precedence question, so it is refused instead of silently resolved.

**Two body representations.** The same reasoning: a body arrives in one
representation, and the type declares which one it uses.

**An unregistered type.** A type an app has not declared is content nobody can
create, so it answers 404 rather than inventing the type on first use.

**A changed type.** An update may restate the type but not change it: the type
is what the app defined, and content that changed type would be a different
thing wearing the same id.

**A stale version.** The update states the version it replaces, which is what
stops two writers silently overwriting one another. The conflict says *the
content changed* — it used to borrow the page message and tell a caller a page
had changed when none had.

The space comes from the parent when there is one, so content cannot be filed
under a page in one space while claiming to belong to another.

## Attachments and footer comments

Both reads answer an empty page rather than a 404. The content exists and has
none: nothing can yet be attached to or commented on custom content, and saying
"no such content" would be false.

## Evidence and current boundary

- `internal/confluence/custom_content_test.go` covers all 19 operations, all
  three parents and the reads that report each, the space and global listings
  and the type the global one requires, both versions and an earlier body, the
  properties, the expanded read, and every refusal above — including that the
  conflict message does not name a page.
- `internal/confluence/http_test.go` covers the two pre-existing reads, now
  through the create endpoint.
- `migrations/145_custom_content.sql` moves the existing rows into the one
  table and is exercised from a clean PostgreSQL schema.

Creating attachments or footer comments on custom content, the `atlas_doc_format`
body representation, cursor paging beyond the shared list helper, and custom
content in the browser journeys remain.
