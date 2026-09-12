# Content relations

A relation is a named link between two entities. The entities are people,
spaces and content, and the link runs one way: saying that a runbook is the
sibling of a checklist says nothing about what the checklist is the sibling of.

Confluence names one relation itself — `favourite`, the "save for later" a
reader puts on a page or a space — and lets a client name any other it needs.
Nothing has to be declared first: naming `sibling` between two pages creates a
sibling relation, and the next read finds it.

## Endpoints

| Method | Path |
| --- | --- |
| `GET` | `/wiki/rest/api/relation/{name}/from/{sourceType}/{sourceKey}/to/{targetType}` |
| `GET` | `/wiki/rest/api/relation/{name}/from/{sourceType}/{sourceKey}/to/{targetType}/{targetKey}` |
| `PUT` | `/wiki/rest/api/relation/{name}/from/{sourceType}/{sourceKey}/to/{targetType}/{targetKey}` |
| `DELETE` | `/wiki/rest/api/relation/{name}/from/{sourceType}/{sourceKey}/to/{targetType}/{targetKey}` |
| `GET` | `/wiki/rest/api/relation/{name}/to/{targetType}/{targetKey}/from/{sourceType}` |

An entity type is `user`, `space` or `content`. A user is named by account id,
or by `current` for whoever is asking; a space by its key; content by its id.

```
PUT /wiki/rest/api/relation/favourite/from/user/current/to/content/1
PUT /wiki/rest/api/relation/sibling/from/content/1/to/content/2
GET /wiki/rest/api/relation/sibling/from/content/1/to/content?expand=target
```

## Reading a relation

A relation is returned as its name, with its parts named as expandable rather
than sent:

```json
{
  "name": "sibling",
  "_expandable": { "relationData": "", "source": "", "target": "" },
  "_links": { "self": "https://example.test/wiki/rest/api/relation/sibling/from/content/1/to/content/2" }
}
```

`expand` asks for them. `source` and `target` return the entities themselves —
a user bean, a space bean or a content bean. `relationData` returns `createdBy`
and `createdDate`, so a reader can see who made the link and when.

Creating a relation takes no `expand` parameter, so the reply to a `PUT` always
names the relation and leaves its parts to be fetched.

## Direction

The two listings answer different questions:

- `.../from/{sourceType}/{sourceKey}/to/{targetType}` — what this entity is
  related **to**.
- `.../to/{targetType}/{targetKey}/from/{sourceType}` — what is related **to**
  this entity.

A relation created from page 1 to page 2 is found by the first listing on page
1 and by the second listing on page 2. It is not found by the first listing on
page 2: the link does not run that way, and a client that wants it in both
directions creates it twice.

Both listings page with `start` and `limit`, returning 25 relations unless asked
for another number, and both report `results`, `start`, `limit` and `size`.

The listings serve relations a client named. Favourites and likes are read
through their own endpoints, so passing `favourite` or `like` to a listing is
answered as an invalid request rather than as an empty page.

## Content status and version

`sourceStatus`, `targetStatus`, `sourceVersion` and `targetVersion` qualify an
end of the relation that is content. Content is `current`, `draft`, `archived`,
`trashed` or `historical`; a version goes with `historical` and names the one
revision meant.

A relation to a past revision is a different relation from one to the content as
it stands. Both can exist at once, and each listing sees only the status it was
asked for — a listing with no status given sees the current content.

A user and a space have neither a status nor a version, so giving one is an
invalid request rather than a value quietly ignored.

## What may be created

`favourite` is something a person saves, so it runs from a `user` to a `space`
or to `content`. A favourite pointing the other way is refused.

Any other name may link any two of the three entity types. Both ends must exist
and be visible to whoever is asking; a relation to content that is not there, or
that the reader may not open, is a 404.

A relation whose source is a user is that person's own statement. Creating or
removing one for somebody else is an administrative act and requires site
administration; a reader making their own needs only to be able to use the site.

## Idempotence

Creating a relation that already exists leaves it in place and answers 200: the
request asks for the link to be there, and afterwards it is.

Deleting one answers 204 whether or not it was there, for the same reason. An
entity that does not exist is still a 404 — that is a request about something
the site does not have, not a request whose outcome is already true.

## Storage

`wiki_relations` holds one row per relation, keyed by workspace, name, and both
ends with their status and version. Entities are stored by the key the API uses
— account id, space key, content id — so a relation survives a rename of what it
points at, and resolves through the same visibility rules as a direct read.
