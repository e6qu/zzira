# Confluence redaction

Redaction replaces sensitive text in a page or blog post with a marker. Space
administrators can later restore it. Part of
[Confluence](CONFLUENCE_SITE_SURFACES.md). Code:
`internal/store/wiki_redaction.go`.

| Surface | Path |
| --- | --- |
| API | `POST /wiki/api/v2/pages/{id}/redact`, `POST /wiki/api/v2/blogposts/{id}/redact` |
| UI | **Redact sensitive text** on the page or blog post; restoring uses the page's `…/metadata` form |

## Request

- **Title redactions** point at `/title`.
- **Body redactions** point at one of two places:
  - the stored text, as `/body/storage/value` with rune offsets;
  - a text node in the document format, such as `/content/0/content/1/text`,
    with offsets into that node. The pointer is mapped back to the stored text,
    and an entity counts as one character.
- **Overlapping ranges** are merged into one redaction. Results come back in
  the order the pointers were given.
- **`createdAt`** must be an RFC 3339 timestamp that matches the version being
  redacted.
- **`versionNumber`**:
  - Omitted, or set to the current version: the current version is redacted
    and a new version is created.
  - Set to an earlier version: only that version changes.
  - A stale `createdAt` or an out-of-range `versionNumber` is 400
    (`createdAt or versionNumber is out of date.`).
- **`cleanHistory`** also replaces the removed text in every other version.
  Text removed that way cannot be restored.

## Markers

Each redaction gets a UUID, returned as `redactionId`, and a record of the
text it removed.

| Where the text was | Marker | Restorable |
| --- | --- | --- |
| Body | A `redacted` macro whose `ac:macro-id` is the redaction id and whose body reads `[REDACTED]` | Yes |
| Title | `[REDACTED]` | Yes |
| Code (preformatted text, inline code, a macro's plain-text body or parameters) | Plain `[REDACTED]` | No; the response has no `redactionId` |

## Restoring

Space administrators see the list of redactions on the page or blog post and
restore them from there.

- **In the current version.** The removed text is put back and a new version
  is created.
- **In an earlier version.** The text is restored in the version that was
  redacted.
- **After restoring.** The stored text is discarded.
- **Marker gone.** If the marker has since been removed from the content, the
  redaction can no longer be restored.

Redactions (`wiki.page.redacted`, `wiki.blogpost.redacted`) and
restorations (`….redaction_restored`) are both written to the organization
audit log (see [ADMIN.md](ADMIN.md)). After a redaction, inline comments are anchored
again (see [comments](CONFLUENCE_COMMENTS.md#inline-comment-anchoring)) and
tasks are reconciled (see [tasks](CONFLUENCE_TASKS.md)).

## Permissions

- **Redacting** needs edit permission on the page or blog post.
- **Restoring** needs space administration.

## Tests

`internal/confluence/redaction_test.go`

## See also

[CONTENT_HISTORY.md](CONTENT_HISTORY.md) · [CLOUD_PARITY.md](CLOUD_PARITY.md)
