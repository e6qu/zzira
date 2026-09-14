# Jira attachment compatibility

ZZIRA keeps attachment metadata and bytes behind the same workspace and issue
security checks as the parent work item. The Jira Cloud routes currently cover
settings, multipart upload, metadata, byte downloads and ranges, thumbnails,
ZIP archive expansion, and deletion.

| Route | Behavior |
|---|---|
| `GET /rest/api/3/attachment/meta` | Reports whether attachments are enabled and stored, and the site's maximum attachment size |
| `POST /rest/api/3/issue/{issueIdOrKey}/attachments` | Uploads up to 60 files, each within the maximum size (413 otherwise), for callers with Create attachments; 403 without it or while attachments are disabled |
| `GET /rest/api/3/attachment/{id}` | Returns `AttachmentMetadata` with its numeric id, the author's user bean, properties, content and image-thumbnail links |
| `GET /rest/api/3/attachment/content/{id}` | Answers `303` to `/secure/attachment/{id}/{filename}`, or streams the bytes with `redirect=false`; ranges answer 206, malformed ranges 400 |
| `GET /rest/api/3/attachment/thumbnail/{id}` | Answers `303` to `/secure/thumbnail/{id}/{filename}`, or renders with `redirect=false` |
| `GET /rest/api/3/attachment/{id}/expand/human` | Lists ZIP entries with labels, paths, media types and display sizes |
| `GET /rest/api/3/attachment/{id}/expand/raw` | Lists ZIP entries with indexes, names, abbreviated names and byte sizes |
| `DELETE /rest/api/3/attachment/{id}` | Deletes authorized metadata atomically and schedules durable blob cleanup |

The `/secure/attachment` and `/secure/thumbnail` downloads apply the same
checks as the operations that redirect to them, for signed-in and anonymous
callers alike. While attachments are disabled, every attachment read answers
404.

Thumbnails scale images within `width` and `height`, 200 pixels when unset,
keeping the aspect ratio and never enlarging. JPEG sources stay JPEG and other
images become PNG. Attachments without an image rendition get the default file
thumbnail, or 404 with `fallbackToDefault=false`.

Jira expands only ZIP archives. Empty, corrupt and non-archive attachments
list no entries, and TAR, gzip, bzip2, xz, 7-Zip and RAR archives answer 409.
Raw entries abbreviate names longer than 40 characters, keeping their start and
end.

Administrators set the maximum attachment size (1 byte to 1 GiB, 32 MiB by
default) beside the attachment switch in the Jira features settings. The
browser and REST uploads and the shared attachment command all enforce it.

Issue deletion and direct attachment deletion both write blob cleanup intents in
the same transaction as metadata and action history. Immediate object-store
failures are deferred with a bounded backoff and reclaimed through a lease, so
a worker crash or temporary storage outage cannot permanently orphan bytes.

## Confluence v1 attachments on blog posts, and metadata updates

The v1 attachment routes treat pages and blog posts alike. `{id}` in
`/wiki/rest/api/content/{id}/child/attachment…` may name either.

- **Upload.** `POST` adds files. `PUT` adds them, or makes a new version of
  an attachment with the same name.
- **New file for an existing attachment.** `POST …/{attachmentId}/data` adds
  a new version by attachment id.
- **Download.** `GET …/{attachmentId}/download` redirects to the file under
  its container.
- **Beans** name the container (`page` or `blogpost`) and carry
  `collectionName` (`contentId-{containerId}`), the media type description,
  size, file id and comment.

`PUT /wiki/rest/api/content/{id}/child/attachment/{attachmentId}` changes an
attachment's non-binary data as a new version holding the same file:
- `title` renames it, `metadata.mediaType` retypes it, and
  `metadata.comment` replaces its comment;
- `container` (`{id, type}`) moves it to another page or blog post. The caller
  must be allowed to add attachments there, and a name already used there is
  refused;
- `version.number` must be the next version, or the update is refused with 409.
