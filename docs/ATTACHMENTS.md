# Jira attachment compatibility

ZZIRA keeps attachment metadata and bytes behind the same workspace and issue
security checks as the parent work item. The Jira Cloud routes currently cover
settings, multipart upload, metadata, byte downloads and ranges, thumbnails,
ZIP archive expansion, and deletion.

| Route | Behavior |
|---|---|
| `GET /rest/api/3/attachment/meta` | Reports whether storage is configured and the 32 MiB request limit |
| `GET /rest/api/3/attachment/{id}` | Returns Jira-shaped metadata, author, content and image-thumbnail links |
| `GET /rest/api/3/attachment/content/{id}` | Streams authorized bytes and supports one or more HTTP byte ranges through `ServeContent` |
| `GET /rest/api/3/attachment/thumbnail/{id}` | Serves image bytes inline and honors `fallbackToDefault=false` for non-images |
| `GET /rest/api/3/attachment/{id}/expand/human` | Lists bounded ZIP entries with paths, media types and display sizes |
| `GET /rest/api/3/attachment/{id}/expand/raw` | Lists bounded ZIP entries with indexes and byte sizes |
| `DELETE /rest/api/3/attachment/{id}` | Deletes authorized metadata atomically and schedules durable blob cleanup |

Issue deletion and direct attachment deletion both write blob cleanup intents in
the same transaction as metadata and action history. Immediate object-store
failures are deferred with a bounded backoff and reclaimed through a lease, so
a worker crash or temporary storage outage cannot permanently orphan bytes.

The current thumbnail route returns the original image because ZZIRA does not
yet maintain derived renditions. Archive inspection supports ZIP within the
upload bound; Jira's other archive formats, redirect-to-signed-object behavior,
virus scanning, configurable limits, project permission schemes and complete
media processing remain compatibility work.
