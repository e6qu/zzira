# Confluence redaction

`POST /wiki/api/v2/pages/{id}/redact` and `POST /wiki/api/v2/blogposts/{id}/redact`
replace sensitive text with a redaction marker.

## What a redaction names
- **`title` redactions** point at `/title`.
- **`body` redactions** point in one of two ways:
  - at the stored text, with `/body/storage/value` and rune offsets into it;
  - at a text node of the body in the document format, such as
    `/content/0/content/1/text`, with offsets into that node's text. The
    pointer is resolved to the stored text the node came from, and entities
    count as one character.
- **Overlap.** Ranges that overlap are merged into one redaction. Results keep
  the order the pointers were given in.
- **Freshness.** `createdAt` must match the version being redacted, or the
  request is refused with 400.

## Markers and restoration
Each redaction gets a UUID, returned as `redactionId`, and is recorded with
what it removed.

| Where the text was | Marker | Restorable |
| --- | --- | --- |
| Body | A `redacted` macro whose `ac:macro-id` is the redaction's id and whose body reads `[REDACTED]` | Yes |
| Title | `[REDACTED]` | Yes |
| Code block (preformatted text, inline code, or a macro's plain body or parameters) | Plain `[REDACTED]` | No, as in Confluence; the response has no `redactionId` |

Space administrators see a page's or blog post's redactions and restore them
from the page.
- **Restoring from the current version** puts the removed text back and makes
  a new version.
- **Restoring from an earlier version** restores it in that version. This
  applies when that version was the one redacted.
- **Once restored**, the kept text is discarded. A redaction whose marker is
  gone can no longer be restored.

Restorations are audited alongside redactions.

## Versions
- **Without `versionNumber`**, or with the current one, the current version
  is redacted and a new version is made.
- **With an earlier `versionNumber`**, only that version is changed.
- **`cleanHistory`** additionally replaces the removed text in every other
  version. That text cannot be restored.
