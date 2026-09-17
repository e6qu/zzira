# Attachments

Work-item attachments keep metadata and bytes behind the same workspace and
issue-security checks as their work item. This page also covers the
Confluence v1 attachment routes for pages and blog posts. Part of the
[Jira platform](JIRA_PLATFORM.md); see [CLOUD_PARITY.md](CLOUD_PARITY.md) for
status.

## Jira API

| Route | Behavior |
|---|---|
| `GET /rest/api/3/attachment/meta` | Whether attachments are enabled, and the maximum size |
| `POST /rest/api/3/issue/{issueIdOrKey}/attachments` | Upload up to 60 files, each within the maximum size (413 otherwise); 403 without Create attachments or while attachments are disabled |
| `GET /rest/api/3/attachment/{id}` | `AttachmentMetadata`: numeric id, author, properties, content and thumbnail links |
| `GET /rest/api/3/attachment/content/{id}` | 303 to `/secure/attachment/{id}/{filename}`, or the bytes with `redirect=false`; ranges answer 206, malformed ranges 400 |
| `GET /rest/api/3/attachment/thumbnail/{id}` | 303 to `/secure/thumbnail/{id}/{filename}`, or the image with `redirect=false` |
| `GET /rest/api/3/attachment/{id}/expand/human` | ZIP entries with labels, paths, media types and display sizes |
| `GET /rest/api/3/attachment/{id}/expand/raw` | ZIP entries with indexes, names, abbreviated names and byte sizes |
| `DELETE /rest/api/3/attachment/{id}` | Delete; needs Delete all attachments, or Delete own attachments for the caller's own |

## Behavior

- `/secure/attachment` and `/secure/thumbnail` apply the same checks as the
  routes that redirect to them, for signed-in and anonymous callers.
- While attachments are disabled, every attachment read is 404.
- **Size limit.** 1 byte to 1 GiB, 32 MiB by default. Enforced by the REST
  upload, the browser upload and the shared attachment command.
- **Thumbnails** fit within `width` and `height` (200 px default), keep the
  aspect ratio and never enlarge. JPEG stays JPEG; other images become PNG.
  Non-images get the default file thumbnail, or 404 with
  `fallbackToDefault=false`.
- **Archive expansion** covers ZIP only. Empty, corrupt and non-archive files
  list no entries; TAR, gzip, bzip2, xz, 7-Zip and RAR answer 409. Raw entry
  names longer than 40 characters are abbreviated, keeping start and end.
- **Deletion.** Deleting an attachment or its work item writes blob cleanup
  intents in the same transaction as the metadata change and action history.
  Object-store failures are retried with bounded backoff through a leased
  worker, so a crash or outage cannot orphan bytes.

## UI

The work item page uploads (`POST /issues/{key}/attachments`) and deletes
attachments. Administrators turn attachments on or off and set the maximum
size under **Administration → Jira features**.

## Confluence v1 attachments

The v1 routes under `/wiki/rest/api/content/{id}/child/attachment` treat pages
and blog posts alike; `{id}` may name either.

- `POST` adds files. `PUT` adds them, or adds a new version of a same-named
  attachment.
- `POST …/{attachmentId}/data` adds a new version by attachment id.
- `GET …/{attachmentId}/download` redirects to the file under its container.
- Beans name the container (`page` or `blogpost`) and carry `collectionName`
  (`contentId-{containerId}`), the media type description, size, file id and
  comment.
- `PUT …/{attachmentId}` changes non-binary data as a new version of the same
  file: `title` renames, `metadata.mediaType` retypes, `metadata.comment`
  replaces the comment, and `container` (`{id, type}`) moves it to another
  page or blog post (the caller must be allowed to add attachments there, and
  the name must be free). `version.number` must be the next version, or 409.

## See also

[ISSUE_SURFACE.md](ISSUE_SURFACE.md) · [BULK_ISSUES.md](BULK_ISSUES.md) ·
[JIRA_SITE_CONFIGURATION.md](JIRA_SITE_CONFIGURATION.md) ·
[ANONYMOUS_ACCESS.md](ANONYMOUS_ACCESS.md) ·
[CONFLUENCE_SITE_SURFACES.md](CONFLUENCE_SITE_SURFACES.md)
