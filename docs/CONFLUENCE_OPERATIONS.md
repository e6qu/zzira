# Confluence operations

`GET …/operations` and the `include-operations` flag report what the caller may
do with a space or a piece of content, as `{operation, targetType}` pairs. Each
operation is decided by:
- the space permissions the caller holds, through roles assigned to them,
  their groups or an access class, through direct grants, or through a space
  that names nobody;
- the content's own restrictions;
- whether the caller administers the space.

| Target | Operations |
| --- | --- |
| Page | `read` and `export` for anyone who can see it. `update` and `archive` with edit permission and restrictions. `delete` with delete permission. `copy` with `create/page`, and `move` with that plus edit. `restrict_content` for who may restrict it. `purge` and `purge_version` for space administrators who may delete it. `create` `comment` with `create/comment`. `create` `attachment` with `create/attachment` plus edit. |
| Blog post | `read`, `export`, `update`, `delete`. `copy` with `create/blogpost`. `purge` and `purge_version` for space administrators. `create` comments and attachments on the same terms as pages. |
| Folder, database, whiteboard, Smart Link, custom content | `read`, `update` and `delete`. `export` for databases and whiteboards. `copy` with `create/{type}`, and `move` with that plus edit. `purge` for space administrators. `create` `comment` on custom content. |
| Attachment | `read`, `update` and `delete`, plus `purge` for space administrators. |
| Comment | `read`, plus `update` and `delete` for its author and administrators. |
| Space | `read` and `export`. `create` for every kind of content the caller holds permission to create. For space administrators, `update`, `archive`, `delete`, `restrict_content` and `administer`. |
