# Content relations

A relation is a named, one-way link between two entities (a user, a space or content). Confluence defines `favourite` itself; clients may name any other relation without declaring it first. Part of [Confluence](CONFLUENCE_SITE_SURFACES.md); status in [CLOUD_PARITY.md](CLOUD_PARITY.md).

## API

| Method | Path |
| --- | --- |
| `GET` | `/wiki/rest/api/relation/{name}/from/{sourceType}/{sourceKey}/to/{targetType}` |
| `GET` / `PUT` / `DELETE` | `/wiki/rest/api/relation/{name}/from/{sourceType}/{sourceKey}/to/{targetType}/{targetKey}` |
| `GET` | `/wiki/rest/api/relation/{name}/to/{targetType}/{targetKey}/from/{sourceType}` |

Entity types are `user` (account id, or `current` for the caller), `space` (key) and `content` (id).

```
PUT /wiki/rest/api/relation/favourite/from/user/current/to/content/1
PUT /wiki/rest/api/relation/sibling/from/content/1/to/content/2
GET /wiki/rest/api/relation/sibling/from/content/1/to/content?expand=target
```

## Response

```json
{
  "name": "sibling",
  "_expandable": { "relationData": "", "source": "", "target": "" },
  "_links": { "self": "https://example.test/wiki/rest/api/relation/sibling/from/content/1/to/content/2" }
}
```

`expand` accepts `source`, `target` (the user, space or content bean) and `relationData` (`createdBy`, `createdDate`); anything else is 400. `PUT` ignores `expand` and always returns the unexpanded relation.

## Direction and listings

- `.../from/{type}/{key}/to/{targetType}` lists what that entity is related **to**.
- `.../to/{type}/{key}/from/{sourceType}` lists what is related **to** that entity.

A relation from page 1 to page 2 appears in the first listing for page 1 and the second for page 2, never in the first for page 2. For both directions, create two relations.

Listings page with `start` and `limit` (default 25, max 200) and return `results`, `start`, `limit`, `size`. Listing `favourite` or `like` is 400: those are read through their own endpoints. A listing path without a target type is 400.

## Content status and version

`sourceStatus`, `targetStatus`, `sourceVersion` and `targetVersion` qualify a content end:

- status is `current` (default), `draft`, `archived`, `trashed` or `historical`, and must match the content's actual status (otherwise 400);
- a version is required with `historical` and refused with any other status.

A relation to a historical version is distinct from one to current content, and a listing sees only the status it asks for. Giving a status or version for a user or space end is 400.

## Rules

- **Names**: 1 to 255 characters.
- **`favourite`** runs from a `user` to a `space` or `content`; any other shape is 400. Favourites share storage with page and space stars, so a favourite made here shows as a star in the product and in CQL `favourite`.
- **Other names** may link any two entity types.
- **Visibility**: both ends must exist and be visible to the caller, otherwise 404.
- **Authority**: a relation whose source is a user is that user's statement. Creating or deleting one for another user needs site administration.
- **Idempotence**: `PUT` of an existing relation returns 200 and leaves it unchanged. `DELETE` returns 204 whether or not the relation existed. A missing entity is still 404.

## Storage

`wiki_relations` holds one row per relation, keyed by workspace, name, and both ends with their status and version. Ends are stored by API key (account id, space key, content id) and resolved through the normal visibility rules on read.

## Tests

`internal/confluence/relations_test.go`

## Gaps

See [PLAN.md](../PLAN.md).

- A `content` end resolves only to pages; blog posts and other content cannot be related.

## See also

[CQL search](CQL_SEARCH.md), [people](WIKI_USERS.md), [spaces](CONFLUENCE_SPACES.md).
