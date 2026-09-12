# Content templates and blueprints

Updated: 2026-09-12

Confluence has two kinds of template, and the difference decides what the API
may do with each.

A **content template** is written by an administrator through the API, for the
site or for one space. A **blueprint template** comes from a blueprint, so it
cannot be created or updated through the API — the blueprint owns it. It can be
modified for the site or for a space, and removing that modification reverts to
what the blueprint provides.

## Jira Cloud REST surface

All eight pinned operations are implemented. An audit against a running server
found none of them working.

| Method and path | Behavior |
|---|---|
| `POST /wiki/rest/api/template` | Creates a content template. |
| `PUT /wiki/rest/api/template` | Updates one. |
| `GET /wiki/rest/api/template/{contentTemplateId}` | Reads one, content or blueprint. |
| `DELETE /wiki/rest/api/template/{contentTemplateId}` | Deletes a content template. |
| `GET /wiki/rest/api/template/page` | The content templates for the site, or for a space. |
| `GET /wiki/rest/api/template/blueprint` | The templates the blueprints provide. |
| `PUT /wiki/rest/api/content/blueprint/instance/{draftId}` | Publishes a shared draft made from a blueprint. |
| `POST /wiki/rest/api/content/blueprint/instance/{draftId}` | The same for a legacy draft. |

## Blueprints come from blueprints

The blueprint templates are what this site's blueprints provide, not rows anyone
wrote — which is exactly why `POST` and `PUT` refuse a blueprint's template.
Each carries the plugin and module key it came from, so a client can tell which
blueprint it is looking at.

A space inherits every global blueprint, so the space listing is the same set
with any modifications applied. A space's modification wins over the site's for
the same blueprint, because the space is the narrower statement.

## A space inherits the site's content templates

`GET /template/page?spaceKey=` carries the site's templates as well as the
space's. Without a space key it carries only the site's — a space's templates
are not the site's.

## Publishing a blueprint draft

The shared and legacy draft endpoints behave the same way, which is what
Confluence says of them, so one implementation serves both. Publishing turns a
draft into a current page with the title and parent given, records a version,
and refuses a stale version the same way every other content write does.

A draft already published is no longer a draft to publish.

## A bug the first probe found

The create and update answered 404 while succeeding. They wrote the row and read
it back in one statement through a data-modifying CTE, whose rows are not in the
outer query's snapshot — so the template was made and then reported missing. The
write and the read back are one transaction now.

That is the second time this shape has appeared here: a write is only visible to
the transaction that made it, and reading it back any other way reports nothing.

## Evidence and current boundary

- `internal/confluence/templates_test.go` covers all eight operations, that a
  blueprint template names its blueprint and cannot be written or deleted, that
  a space inherits the global blueprints and the site's content templates while
  the site listing carries only its own, the labels and body round trip, every
  refusal, that publishing a draft makes it current and a published draft cannot
  be published again, and that a stale version is refused.
- `migrations/153_content_templates.sql` is exercised from a clean PostgreSQL
  schema.

Blueprints provided by installed apps rather than by the site, the `editor`,
`view` and `export_view` body representations, template `expand` parameters, and
reverting a modified blueprint through the delete remain.
