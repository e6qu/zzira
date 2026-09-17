# Content history, macros and body conversion

Restoring and deleting page versions, reading a macro as it was in a version, and converting bodies between formats. Part of [Confluence](CONFLUENCE_SITE_SURFACES.md); status in [CLOUD_PARITY.md](CLOUD_PARITY.md).

## API

| Method and path | Behavior |
| --- | --- |
| `POST /wiki/rest/api/content/{id}/version` | Restores a historical version as the latest. |
| `DELETE /wiki/rest/api/content/{id}/version/{versionNumber}` | Deletes a historical version. |
| `GET /wiki/rest/api/content/{id}/history/{version}/macro/id/{macroId}` | The macro as it was in that version: `name`, `body`, `parameters`. |
| `GET …/macro/id/{macroId}/convert/{to}` | That macro's body in another format. |
| `GET …/macro/id/{macroId}/convert/async/{to}` | The same, answering `asyncId`. |
| `POST /wiki/rest/api/contentbody/convert/async/{to}` | Converts a body, answering `asyncId`. |
| `GET /wiki/rest/api/contentbody/convert/async/{id}` | The converted body. |
| `POST /wiki/rest/api/contentbody/convert/async/bulk/tasks` | Converts 1 to 100 bodies (`conversionInputs`). |
| `GET /wiki/rest/api/contentbody/convert/async/bulk/tasks?ids=` | Reads many results. |

The version and macro operations act on pages.

## Restoring a version

The body is `{"operationKey": "restore", "params": {"versionNumber", "message", "restoreTitle"}}`. Restoring writes a **new** version holding the historical body, so history stays complete. `restoreTitle: true` also brings back that version's title. The default message is "Restored version N". A version outside `1..current` is 400.

## Deleting a version

The version's row is removed; the page reads the same afterwards, because later versions already carry its changes (Confluence's "rolled up into the next version"). The current version cannot be deleted (400). An unknown version is 404.

## Macros in storage

Storage bodies accept Confluence's structured macros: `ac:structured-macro`, `ac:parameter`, `ac:rich-text-body` and `ac:plain-text-body`, with the `name`, `macro-id`, `schema-version` and `local-id` attributes. Macros are structure, not markup: a reader sees the body a macro wraps, and parameters are stored but not rendered. Macros are not executed (an `info` macro renders as its body, not as a panel).

## Conversions

| From | To |
| --- | --- |
| `storage` | `atlas_doc_format`, `editor`, `view`, `export_view`, `styled_view` |
| `atlas_doc_format` | `storage`, `editor`, `view`, `export_view`, `styled_view` |
| `wiki` | `storage`, `atlas_doc_format`, `editor`, `view`, `export_view`, `styled_view` |
| `editor` | `storage` |

Any other pair fails with a reason instead of returning a body in the wrong format. The async operations record that failure as a `FAILED` result.

- Storage to `atlas_doc_format` uses an HTML-to-ADF reader (`internal/adf/fromhtml.go`). An element it does not model contributes its text.
- Wiki markup is read by `internal/wikimarkup/notation.go` (see [page writing](PAGE_WRITING.md#body-formats)).
- Conversions finish before `asyncId` is returned, so a result is never `WORKING` or `QUEUED`. Results are kept for five minutes, then 404.
- In a bulk read, an unknown or expired id is reported as `FAILED` without failing the others.
- `spaceKeyContext`, `contentIdContext`, `embeddedContentRender`, `allowCache` and `expand` are accepted and do not change the result.

## Tests

- `internal/confluence/content_history_test.go`
- `internal/adf/fromhtml_test.go`
- `internal/wikimarkup/storage_test.go`

## Gaps

See [PLAN.md](../PLAN.md).

- Restoring and deleting versions, and historical macro reads, for blog posts.
- Macro execution (rendering macros such as `info`, `toc`, `code` as Confluence does).
- The context parameters (`spaceKeyContext`, `contentIdContext`, `embeddedContentRender`) are ignored.

## See also

[Drafts and deletion](CONTENT_DRAFTS.md), [page writing](PAGE_WRITING.md), [content states](CONTENT_STATES.md).
