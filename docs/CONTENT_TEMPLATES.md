# Content templates and blueprints

Page and blog post templates for the site or a space, the blueprint templates the site provides, and publishing blueprint drafts. Part of [Confluence](CONFLUENCE_SITE_SURFACES.md); status in [CLOUD_PARITY.md](CLOUD_PARITY.md).

- A **content template** is written by an administrator, for the site or for one space.
- A **blueprint template** comes from a blueprint and cannot be created, updated or deleted through the API.

## API

| Method and path | Behavior |
| --- | --- |
| `POST /wiki/rest/api/template` | Creates a content template. |
| `PUT /wiki/rest/api/template` | Updates one (`templateId` in the body). |
| `GET /wiki/rest/api/template/{contentTemplateId}` | Reads a content or blueprint template. |
| `DELETE /wiki/rest/api/template/{contentTemplateId}` | Deletes a content template. |
| `GET /wiki/rest/api/template/page` | Content templates for the site, or with `spaceKey` for a space. |
| `GET /wiki/rest/api/template/blueprint` | Blueprint templates, optionally for a `spaceKey`. |
| `PUT /wiki/rest/api/content/blueprint/instance/{draftId}` | Publishes a shared blueprint draft. |
| `POST /wiki/rest/api/content/blueprint/instance/{draftId}` | Publishes a legacy blueprint draft (same behavior). |

Lists page with `start` and `limit` (default and max 200).

## Content templates

- `name` 1 to 255 characters; `templateType` is `page` (default) or `blogpost`; body is `body.storage`; `labels` round-trip.
- Site templates need site administration; space templates (`space.key`) need space administration.
- `template/page?spaceKey=` returns the space's templates and the site's. Without `spaceKey` it returns only the site's.

## Blueprint templates

The site provides six: Meeting notes, Decision, Product requirements, How-to article, Troubleshooting article and Retrospective. Ids are `blueprint:<name>`; each bean carries `originalTemplate` (`pluginKey`, `moduleKey`) and `referencingBlueprint`.

`POST`/`PUT` naming a blueprint template, and `DELETE` of a `blueprint:` id, are 400. A space lists every site blueprint. Stored site and space modifications of a blueprint are applied on read, the space's over the site's.

## Publishing a blueprint draft

Turns a draft page into a current page with the given `title` (default: the draft's) and parent (last of `ancestors`, which must be a current page in the same space), and records a version. `version.number`, when given, must be the next version (409 otherwise). `space.key`, when given, must be the draft's space. A page that is no longer a draft is 404.

## UI

`/wiki/spaces/{space}/templates` lists the space's templates, the site's and the blueprints. Space administrators create, edit and delete the space's own templates there. `/wiki/spaces/{space}/pages/new?template={id}` starts a new page from a page template the space can use.

## Tests

- `internal/confluence/templates_test.go`
- `e2e/wiki_space_tools.spec.ts`

## Gaps

See [PLAN.md](../PLAN.md).

- Modifying a blueprint template for the site or a space, and reverting it through `DELETE` (modifications are read but nothing writes them).
- Blueprints provided by installed apps.
- Template bodies in `editor`, `view` and `export_view`; `expand` is accepted and ignored.
- Managing site templates in the UI.

## See also

[Page writing](PAGE_WRITING.md), [drafts](CONTENT_DRAFTS.md), [apps](APPS.md).
