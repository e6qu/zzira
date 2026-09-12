# Content history, macros and body conversion

Updated: 2026-09-12

A content item's history is the versions behind it. One can be restored as the
latest, one can be removed, and a macro can be read as it was in any of them.

## Storage macros had to exist first

The macro operations address `ac:structured-macro` elements in the storage
format — and the storage validator rejected the whole `ac` namespace, so a body
containing a macro could not be saved at all. The operations had nothing to read.

Confluence's storage format is built around macros, so the validator accepts
them now: the macro, the parameters that configure it, and the body it wraps. A
macro is **structure rather than markup**, so none of the `ac` elements reach
the rendered HTML — a reader sees the body the macro holds, and a parameter,
which describes how the macro behaves rather than what it shows, is stored and
not rendered.

The existing storage test asserted that a macro was rejected. That assertion was
correct for the old limitation and is wrong now, so it was replaced by one that
checks what a macro renders as.

## Jira Cloud REST surface

All nine pinned operations are implemented. An audit against a running server
found none of them working.

| Method and path | Behavior |
|---|---|
| `POST /wiki/rest/api/content/{id}/version` | Restores a historical version as the latest. |
| `DELETE /wiki/rest/api/content/{id}/version/{versionNumber}` | Removes a historical version. |
| `GET /wiki/rest/api/content/{id}/history/{version}/macro/id/{macroId}` | A macro as it was in that version. |
| `GET …/macro/id/{macroId}/convert/{to}` | That macro in another format. |
| `GET …/macro/id/{macroId}/convert/async/{to}` | The same, answering an id to fetch it with. |
| `POST /wiki/rest/api/contentbody/convert/async/{to}` | Converts a body, answering an id. |
| `GET /wiki/rest/api/contentbody/convert/async/{id}` | The converted body. |
| `POST /wiki/rest/api/contentbody/convert/async/bulk/tasks` | Converts many at once. |
| `GET /wiki/rest/api/contentbody/convert/async/bulk/tasks` | Reads many results back. |

The asynchronous convert and the result endpoint are in one checkpoint on
purpose. An operation that answers an id nothing can fetch is not usable, which
is a mistake this work has already made once — the space delete pointed at a
long task the long task read refused to report.

## Restoring adds a version

Confluence creates a new version holding the historical content rather than
moving the page back, so the history stays a record of everything that happened.
`restoreTitle` decides whether the old title comes back with the body.

## Deleting a version does not undo it

The changes that version made are already carried by the versions after it —
which is what Confluence means by rolling them up into the next version. So the
page reads the same afterwards, and the test checks that. **The current version
cannot be deleted**, because its changes have nowhere to roll into.

## The conversions Confluence supports

`atlas_doc_format` to editor, export_view, storage, styled_view and view;
`storage` to those and to `atlas_doc_format`; `editor` to `storage`. Anything
else fails with a reason rather than answering a body in the wrong format.

Converting storage to the document format needed an HTML-to-ADF reading, which
is the inverse of the renderer this product already had. It degrades the way the
renderer does: an element it does not model contributes its text, because losing
the words would be worse than losing the formatting.

## Evidence and current boundary

- `internal/confluence/content_history_test.go` covers all nine operations, the
  macro read in the version that had it and its absence from the one that did
  not, both conversions and the refused one, the async id being fetchable, that
  restoring adds a version and brings the title back, that deleting a version
  leaves the page unchanged and the current version cannot be deleted, and that
  one unreadable id in a bulk read does not fail the others.
- `internal/adf/fromhtml_test.go` covers the HTML-to-document-format reading.
- `internal/wikimarkup/storage_test.go` covers what a macro renders as.
- `migrations/155_body_conversions.sql` is exercised from a clean PostgreSQL
  schema.

Conversions complete before the id is answered, so a caller never sees `WORKING`
or `QUEUED`. Macros are stored and read but not executed — an `info` macro is
its body, not a rendered panel — and the `spaceKeyContext`, `contentIdContext`
and `embeddedContentRender` parameters are accepted and do not change the
result.
