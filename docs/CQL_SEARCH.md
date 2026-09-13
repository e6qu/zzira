# CQL search

Confluence searches with its own query language. Two endpoints run it:

| Method | Path | Returns |
| --- | --- | --- |
| `GET` | `/wiki/rest/api/search` | search results — what matched, and where it sits |
| `GET` | `/wiki/rest/api/content/search` | the content itself |

`cql` is required on both. An empty query is refused rather than read as "everything".

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
| `space.type` | `= !=` | `global` or `personal` |
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

A field this list does not name is refused. A query whose condition was quietly
dropped would return more than it asked for, which is worse than an error.

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

A function is answered when the query is compiled, against who is asking and
when. Nothing a reader writes decides who `currentUser()` is.

### Dates

A date is `yyyy-mm-dd`, `yyyy-mm-dd hh:mm`, `yyyy-mm` or `yyyy`, read as UTC.
A bare date names a whole day, so `created = "2026-09-01"` matches anything made
that day rather than only at midnight.

### Ordering

`ORDER BY` accepts `created`, `lastmodified`, `title`, `id`, `type` and `space`.
Confluence orders by relevance when nothing is asked for; this product has no
relevance score, so the default is the most recently changed first. Every order
settles ties on type and id, because two rows that tie would otherwise let
paging show one twice and miss another.

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

`_links` carries `next` and `prev` where there is a page in that direction, both
relative to `base`, which is how Confluence hands them back. A cursor this
search did not issue is refused rather than read as whatever position it happens
to decode to. Walking past the end returns nothing and still reports the total.

## What a search can reach

A search never widens what a reader may see. Every part of the searched view
carries the same visibility rules as a direct read — space permissions, page
restrictions, unpublished drafts, private blog posts — so content a reader
cannot open is absent from their results and is not counted in the total either.

## Boundary

- Ordering is by the named field, and by last modified when none is named.
  There is no relevance score, so `ORDER BY relevance` is refused rather than
  silently answered with another order.
- A space records when it was made but not when it last changed, so both stamps
  on a space result are the same one.
- `text ~` matches the words as they were written, in the title or the body. It
  is a substring match, not a stemmed index, so `deploying` does not find
  `deploy`.
- `excerpt` marks the words the query quoted after `~`. It does not mark words
  matched through a field that was compared with `=`.
- `sitePermissionTypeFilter` is accepted and does not change the results,
  because this product has no external collaborators to filter by.
- The user-specific CQL fields Confluence removed from `/search` — `user`,
  `user.fullname`, `user.accountid`, `user.userkey` — are not accepted here
  either. Searching for people is `/wiki/rest/api/search/user`.
