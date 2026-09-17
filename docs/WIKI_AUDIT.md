# Confluence audit log

The Confluence site's own audit log, with Confluence's record shape. Every operation needs site administration. Part of [Confluence](CONFLUENCE_SITE_SURFACES.md); status in [CLOUD_PARITY.md](CLOUD_PARITY.md).

This log is separate from the organization audit log (`organization_audit_events`, see [admin](ADMIN.md)), which spans products and uses an action/target/detail shape.

## API

| Method and path | Behavior |
| --- | --- |
| `GET /wiki/rest/api/audit` | Records, newest first. `startDate`, `endDate`, `searchString`, `start`, `limit`. |
| `POST /wiki/rest/api/audit` | Adds a record. |
| `GET /wiki/rest/api/audit/since` | Records from `number` `units` ago, with `searchString`. |
| `GET /wiki/rest/api/audit/export` | The same filters as a CSV file, or the same CSV zipped with `format=zip`. |
| `GET /wiki/rest/api/audit/retention` | The retention period (default 3 `MONTHS`). |
| `PUT /wiki/rest/api/audit/retention` | Sets the retention period. |

## Record

`author` (account id and name), `remoteAddress`, `creationDate`, `summary` (1 to 255 characters), `description`, `category`, `sysAdmin`, `superAdmin`, `affectedObject`, `changedValues`, `associatedObjects`. Without an author, the caller is the author; a client may name another author. `searchString` matches summary, description and category.

## Retention

The retention period is how long a record is kept after creation. Records older than the period are excluded from reads. Setting the period deletes the records that now fall outside it, so lengthening it again does not bring them back.

Units are `java.time` units, `NANOS` to `FOREVER`. Reads accept any unit. Retention must be positive and at most one year in any unit (`2 YEARS` and `400 DAYS` are both 400).

## Storage

`wiki_audit_records`; the retention setting is in `wiki_site_settings`.

## Tests

`internal/confluence/audit_test.go`

## Gaps

See [PLAN.md](../PLAN.md).

- Site operations (space exports, permission and group changes, app installs) do not write audit records; the log holds only records added through `POST`.
- `sysAdmin` and `superAdmin` are stored as sent, not derived from the author.
- No audit log view in the product UI.

## See also

[Confluence groups](WIKI_GROUPS.md), [site settings](SITE_SETTINGS.md), [admin](ADMIN.md).
