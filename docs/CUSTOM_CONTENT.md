# Custom content

Custom content is app-defined content: a title, a body and a type. It lives in a space and may sit under a page, a blog post or other custom content. It is stored in `wiki_content` (`type='custom'`, `custom_type`) and shares that table's hierarchy, permissions, versions and properties. Part of [Confluence](CONFLUENCE_SITE_SURFACES.md); status in [CLOUD_PARITY.md](CLOUD_PARITY.md).

## API

| Method and path | Behavior |
| --- | --- |
| `GET /wiki/api/v2/custom-content?type=` | Custom content across spaces. `type` (or `id`) is required; an unknown type is 404. |
| `POST /wiki/api/v2/custom-content` | Creates custom content in a space, or under a page, blog post or other custom content. |
| `GET` / `PUT` / `DELETE /wiki/api/v2/custom-content/{id}` | Reads, replaces or deletes one. |
| `GET /wiki/api/v2/custom-content/{id}/versions`, `/versions/{n}` | Versions; one version with its body. |
| `GET` / `POST /wiki/api/v2/custom-content/{id}/properties`; `GET` / `PUT` / `DELETE .../properties/{propertyId}` | Content properties. |
| `GET /wiki/api/v2/custom-content/{id}/labels`, `/operations`, `/children`, `/attachments`, `/footer-comments` | What hangs off it. |
| `GET /wiki/api/v2/spaces/{id}/custom-content` | A space's custom content. |
| `GET /wiki/api/v2/pages/{id}/custom-content`, `/blogposts/{id}/custom-content` | Custom content under a page or blog post. |

Lists accept `sort` (`id`, `created-date`, `modified-date`, `title`, each with `-`), `status` (`current` default, `archived`, `trashed`), `body-format` (`storage` or `raw`) and cursor paging.

The bean reports the app type as `type` and its parent as `pageId`, `blogPostId` or `customContentId`.

## Types

A type is registered in `wiki_custom_content_types` with the body representation it uses (`storage` or `raw`). The seeded types are `com.zzira:diagram` (storage) and `com.zzira:metric-snapshot` (raw).

## Write rules

- More than one of `pageId`, `blogPostId`, `customContentId`: 400.
- A body in both `storage` and `raw`: 400.
- An unregistered type: 404.
- An update that changes the type: 400.
- An update whose `version.number` is stale: 409, with a message about content (not a page).
- With a parent, the space comes from the parent, so content cannot claim a different space.

## Comments and attachments

Footer comments are created with `POST /wiki/api/v2/footer-comments` and `customContentId`, and listed by `/custom-content/{id}/footer-comments` (see [Confluence comments](CONFLUENCE_COMMENTS.md)). `/custom-content/{id}/attachments` always returns an empty page.

## Copying and moving

A page copy with `copyCustomContents` copies the custom content directly under the page, starting at version 1. Moving a page to another space takes its custom content along. See [page moves](PAGE_MOVES.md).

## Tests

- `internal/confluence/custom_content_test.go`
- `internal/confluence/http_test.go` (page and blog post reads)
- `internal/confluence/comment_lifecycle_test.go` (footer comments)

## Gaps

See [PLAN.md](../PLAN.md).

- Apps cannot register custom content types (Forge `confluence:customContent` modules); only the seeded types exist.
- Attachments on custom content.
- The `atlas_doc_format` body representation.
- Custom content in the product UI.

## See also

[Apps](APPS.md), [content tree](CONTENT_TREE.md), [content states](CONTENT_STATES.md).
