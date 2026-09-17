# CQL search

Confluence Query Language (CQL) is Confluence's search language. It is
compiled to SQL over one searchable view, and that view applies the same
visibility rules as a direct read. Part of
[Confluence](CONFLUENCE_SITE_SURFACES.md). Code: `internal/cql`,
`internal/confluence/search.go`, `internal/store/wiki_search.go`.

Two endpoints run CQL:

| Method | Path | Returns |
| --- | --- | --- |
| `GET` | `/wiki/rest/api/search` | search results — what matched, and where it sits |
| `GET` | `/wiki/rest/api/content/search` | the content itself |

`cql` is required on both. An empty query is rejected, not treated as "match everything".

```
GET /wiki/rest/api/search?cql=type%3Dpage%20and%20text~%22runbook%22&limit=25
GET /wiki/rest/api/content/search?cql=label%3Drelease
```

## The language

```
query   := orExpr [ ORDER BY field [ASC|DESC] (, field [ASC|DESC])* ]
orExpr  := andExpr ( OR andExpr )*
andExpr := unit ( AND unit )*
unit    := NOT unit | '(' orExpr ')' | clause
clause  := field op value | field [NOT] IN '(' value (, value)* ')'
op      := = | != | ~ | !~ | < | <= | > | >=
```

Keywords are case-insensitive. `AND` binds tighter than `OR`, so
`a = 1 and b = 2 or c = 3` means `(a = 1 and b = 2) or c = 3`; brackets change
it. Values may be bare words or quoted with `"` or `'`.

### Fields

| Field | Operators | Matches |
| --- | --- | --- |
| `type` | `= != IN NOT IN` | `page`, `blogpost`, `comment`, `attachment`, `space` |
| `id` | `= != IN NOT IN` | the content id |
| `space` | `= != IN NOT IN` | the space key |
| `space.type` | `= !=` | the space type (`global`, `personal`, `collaboration`, …) |
| `status` | `= != IN NOT IN` | the content status |
| `title` | `= != ~ !~` | the title |
| `text` | `~ !~` | the title and the body together |
| `label` | `= != ~ !~ IN NOT IN` | any label it carries |
| `creator` | `= != IN NOT IN` | who wrote it |
| `contributor` | `= != IN NOT IN` | anyone who wrote any version of it |
| `watcher` | `= != IN NOT IN` | anyone watching it |
| `favourite` | `= !=` | anyone who saved it for later |
| `mention` | `= != IN NOT IN` | anyone mentioned in the body |
| `macro` | `= != ~ !~ IN NOT IN` | any macro in the body |
| `parent` | `= != IN NOT IN` | the content directly above |
| `ancestor` | `= != IN NOT IN` | any content above |
| `container` | `= != IN NOT IN` | the space or content it lives in |
| `created` | `= != < <= > >=` | when it was made |
| `lastmodified` | `= != < <= > >=` | when it last changed |

Any field not in this table is rejected, so a condition is never silently
dropped.

### Functions

- `currentUser()` — whoever is asking. It belongs with a field that holds a
  person, so `title = currentUser()` is refused.
- `currentSpace()` — the space the search was scoped to with `cqlcontext`. With
  no space given there is no current space, and the query is refused.
- `favouriteSpaces()` — the spaces the asker saved for later. It compiles to the
  set itself rather than to a list of keys, so it stays current.
- `now()`, `startOfDay()`, `endOfDay()`, `startOfWeek()`, `endOfWeek()`,
  `startOfMonth()`, `endOfMonth()`, `startOfYear()`, `endOfYear()` — each takes
  an optional offset written as a signed number and a unit, as in `now("-7d")`.
  Units are `m` minutes, `h` hours, `d` days, `w` weeks, `M` months, `y` years.
  A week starts on Monday.

Functions are evaluated when the query is compiled, using the caller and the
current time. Nothing in the query text can change who `currentUser()` is.

### Dates

A date is `yyyy-mm-dd`, `yyyy-mm-dd hh:mm`, `yyyy-mm` or `yyyy`, read as UTC.
A bare date names a whole day, so `created = "2026-09-01"` matches anything made
that day rather than only at midnight.

### Ordering

`ORDER BY` accepts `created`, `lastmodified`, `title`, `id`, `type` and `space`.
Without `ORDER BY`, results are sorted by most recently changed first. Ties are
always broken by type and then id, so paging is stable. `ORDER BY relevance`
is rejected.

## Scope

`cqlcontext` is a JSON object narrowing where the search runs:

```
cqlcontext={"spaceKey":"DEV","contentId":"123","contentStatuses":["current","draft"]}
```

- `spaceKey` restricts the search to one space, and is what `currentSpace()`
  means.
- `contentId` restricts it to one piece of content, and names content in
  `spaceKey`, so giving it without a space is refused.
- `contentStatuses` is which content is searched. It defaults to `current`, so a
  draft is not found unless it was asked for.

`includeArchivedSpaces=true` brings in archived spaces; `excludeCurrentSpaces=true`
leaves out everything else, so only archived spaces are searched.

## Results

`/search` returns search results. Each names the match, an excerpt, the URL to
open it, its `entityType`, when it last changed, and where it sits. Content
results carry the content under `content`; a space result carries the space
under `space`. The envelope reports `cqlQuery`, `totalSize` and `searchDuration`.

`/content/search` returns the content itself: id, type, status, title, space and
links. It reads content, so a space is never a result there however the query is
written.

### Excerpts

`excerpt` chooses what is shown under a result:

| Value | Shows |
| --- | --- |
| `highlight` (default) | the passage, HTML-escaped, with the searched words wrapped in `@@@hl@@@`…`@@@endhl@@@` |
| `highlight_unescaped` | the same, not escaped |
| `indexed` | the passage, HTML-escaped, unmarked |
| `indexed_unescaped` | the same, not escaped |
| `none` | nothing |

## Paging

`limit` returns 25 results unless given another number, up to 200. `limit=0`
returns none, which is how a client asks only how many there are. `start` is an
offset; `cursor` is the opaque pointer the previous response handed back.

`_links` carries `next` and `prev` when those pages exist, relative to `base`.
A cursor this search did not issue is 400. Paging past the end returns no
results and still reports the total.

## What a search can reach

Search applies the same visibility rules as a direct read: space
permissions, page restrictions, unpublished drafts and private blog posts.
Content the caller cannot open is left out of the results and out of the
total.

## Behavior notes

- **Space timestamps.** A space stores only its creation time, so both
  timestamps on a space result are the creation time.
- **`excerpt` highlighting.** Only words matched with `~` are highlighted.
  Words matched with `=` are not.
- **`sitePermissionTypeFilter`.** Accepted but has no effect, because there
  are no external collaborators.
- **Removed user fields.** `user`, `user.fullname`, `user.accountid` and
  `user.userkey` are rejected, as Confluence removed them from `/search`. Use
  `/wiki/rest/api/search/user` to search for people.

## Gaps

Tracked in [PLAN.md](../PLAN.md).

- **Relevance.** No relevance score and no relevance ordering.
- **Text matching.** `text ~` is a case-insensitive substring match, with no
  stemming, fuzzy matching or stop words. `deploying` does not find `deploy`.
- **Content types.** Whiteboards, databases, folders, Smart Links and custom
  content cannot be searched; `type` covers only `page`, `blogpost`,
  `comment`, `attachment` and `space`.
- **Fields.** No `content`, `space.title`, `space.category` or content
  property fields.
- **Browser.** No Confluence search page. The space page only filters its
  pages and blog posts by title.

## See also

[JQL.md](JQL.md) · [CONTENT_RELATIONS.md](CONTENT_RELATIONS.md) ·
[CLOUD_PARITY.md](CLOUD_PARITY.md)
