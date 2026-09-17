# Confluence comments

Footer and inline comments on pages, blog posts, attachments and custom
content. Comment bodies are written and read the same way as page bodies. Part
of [Confluence](CONFLUENCE_SITE_SURFACES.md).

## API

| Path (under `/wiki/api/v2`) | Methods |
| --- | --- |
| `/footer-comments`, `/inline-comments` | `GET`, `POST` |
| `/footer-comments/{id}`, `/inline-comments/{id}` | `GET`, `PUT`, `DELETE` |
| `…/{id}/children`, `…/{id}/versions`, `…/{id}/versions/{n}`, `…/{id}/operations` | `GET` |
| `…/{id}/likes/count`, `…/{id}/likes/users` | `GET` |
| `/pages/{id}/footer-comments`, `/pages/{id}/inline-comments` | `GET` |
| `/blogposts/{id}/footer-comments`, `/blogposts/{id}/inline-comments` | `GET` |
| `/attachments/{id}/footer-comments`, `/custom-content/{id}/footer-comments` | `GET` |
| `/comments/{id}/properties…` | See [comment properties](CONFLUENCE_SITE_SURFACES.md#comment-properties) |

Browser: the page and blog post views post to `…/comments`,
`…/inline-comments` and `…/comments/{comment}/like`.

## Behavior

- **Formats.**
  - **Writing.** A body may be flat or nested, in storage format, the
    document format (`atlas_doc_format`) or wiki markup. It is stored as
    storage format.
  - **Single comment reads.** Any primary format: `storage`,
    `atlas_doc_format`, `view`, `export_view`, `anonymous_export_view`,
    `styled_view` or `editor`.
  - **Collections and versions.** `storage` or `atlas_doc_format` only.
- **Includes.** A single comment read accepts `include-properties`,
  `include-operations`, `include-likes`, `include-versions` and
  `include-version=false`.
- **Statuses.** Collections accept every status Confluence defines. A comment
  stays `current` until it is deleted, so a filter that names only other
  statuses returns nothing. It is not rejected.
- **Custom content.**
  - **Creating.** A footer comment on custom content takes `customContentId`
    on create, and replies inherit it.
  - **Visibility.** Such a comment is visible to anyone who can see the
    custom content, which depends on its space, its privacy and its parent
    page.
- **Stars.** Starring a page or blog post creates the `favourite` relation
  (see [relations](CONTENT_RELATIONS.md)). CQL's `favourite` field searches
  it.

## Inline comment anchoring

An inline comment is anchored to a passage of text. If that passage appears
more than once, the comment records which occurrence it belongs to. After an
edit, a version restore or a redaction, every comment is anchored again:

- **The passage is still there.** The comment stays, and its match count and
  index are updated.
- **The passage is gone.** The comment becomes `dangling`. Find it with
  `resolution-status=dangling`.
- **The passage comes back.** A dangling comment is anchored again and
  reopened.

Replicas receive every comment whose anchor changed.

## Permissions

- **Reading.** A comment is visible to anyone who can see what it is on.
- **Editing and deleting.** Only the comment's author or an administrator,
  and only in a space whose permissions allow it. See
  [operations](CONFLUENCE_OPERATIONS.md).

## Tests

`internal/confluence/comment_lifecycle_test.go`

## See also

[CONFLUENCE_NOTIFICATIONS.md](CONFLUENCE_NOTIFICATIONS.md) ·
[CONFLUENCE_REDACTION.md](CONFLUENCE_REDACTION.md) ·
[CLOUD_PARITY.md](CLOUD_PARITY.md)
