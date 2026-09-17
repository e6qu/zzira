# Confluence operations

`GET …/operations` and the `include-operations` flag report what the caller may
do with a space or a piece of content, as `{operation, targetType}` pairs. Part
of [Confluence](CONFLUENCE_SITE_SURFACES.md). Code:
`internal/confluence/operations.go`.

Operations are served for `spaces`, `pages`, `blogposts`, `footer-comments`,
`inline-comments`, `attachments`, `folders`, `databases`, `whiteboards`,
`embeds` and `custom-content`, all under `/wiki/api/v2/{type}/{id}/operations`.

Each operation depends on three things:

- **The caller's space permissions.** These come from
  [roles](CONFLUENCE_SPACE_ROLES.md) assigned to the caller, their groups or an
  access class, from [direct grants](SPACE_PERMISSIONS.md), or from a space
  with no assignments at all (which is open).
- **The content's restrictions.**
- **Whether the caller administers the space.**

| Target | Operations |
| --- | --- |
| Page | `read` and `export` for anyone who can see it. `update` and `archive` with edit permission, if the page's restrictions allow it. `delete` with delete permission. `copy` with `create/page`. `move` with `create/page` and edit permission. `restrict_content` for anyone allowed to restrict the page. `purge` and `purge_version` for space administrators who may delete it. `create` `comment` with `create/comment`. `create` `attachment` with `create/attachment` and edit permission. |
| Blog post | `read`, `export`, `update`, `delete`. `copy` with `create/blogpost`. `purge` and `purge_version` for space administrators. `create` comments and attachments on the same terms as pages. |
| Folder, database, whiteboard, Smart Link, custom content | `read`, `update`, `delete`. `export` for databases and whiteboards. `copy` with `create/{type}`. `move` with `create/{type}` and edit permission. `purge` for space administrators. `create` `comment` on custom content. |
| Attachment | `read`, `update`, `delete`. `purge` for space administrators. |
| Comment | `read`. `update` and `delete` for its author and administrators. |
| Space | `read` and `export`. `create` for each content type the caller may create. Space administrators also get `update`, `archive`, `delete`, `restrict_content` and `administer`. |

Some reported operations have no working feature behind them:

- **`export` on a whiteboard or database.** No export exists for either.
- **`export` on a single page or blog post.** There is no per-page export. The
  only export is the space HTML export (see [spaces](CONFLUENCE_SPACES.md)).

## Tests

`internal/confluence/operations_test.go`

## See also

[CLOUD_PARITY.md](CLOUD_PARITY.md)
