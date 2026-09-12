# The Confluence audit log

Updated: 2026-09-12

The site's record of what administrators did: space exports, group membership
changes, app installations, permission changes.

## Not the organization audit log

This product already has `organization_audit_events`, which the organization
administration surface writes. That is a different log: it belongs to the
organization across its products, and carries a different record — an action, a
target and a detail object.

Confluence's audit record is the site's own and has its own shape: an author, a
remote address, a summary and description, a category, whether the actor was a
system or super administrator, the object affected, the values that changed, and
the objects associated with the change. The two are kept apart because they
answer different questions, and folding one into the other would lose the shape
each surface reports.

## Jira Cloud REST surface

All six pinned operations are implemented. An audit against a running server
found none of them working.

| Method and path | Behavior |
|---|---|
| `GET /wiki/rest/api/audit` | The log, with an optional date range and search. |
| `POST /wiki/rest/api/audit` | Adds a record. |
| `GET /wiki/rest/api/audit/since` | A period back from now. |
| `GET /wiki/rest/api/audit/export` | The log as CSV, or the same CSV zipped. |
| `GET /wiki/rest/api/audit/retention` | How long records are kept. |
| `PUT /wiki/rest/api/audit/retention` | Sets it, to at most a year. |

## Retention deletes

The retention period is how long a record is kept **from its creation date until
it is deleted**, which is Confluence's own wording. So two things follow, and
both are tested.

A record past the retention is not in the log. And **setting the retention
deletes what now falls outside it** rather than hiding it — a record that
reappeared when the period was lengthened would not have been deleted, and the
site said it was.

## A record's author

A record with no author named is the caller's, because the caller is who made
it. A client integrating its own administration can name a different author,
which is what lets the site's log stay complete.

## The export

CSV, or the same CSV zipped — the rows a caller gets are the same either way,
which the test checks by comparing the two bodies rather than trusting the
header.

## Periods

The units are `java.time`'s, which is what Confluence names: `NANOS` through
`FOREVER`. All are accepted for a read; anything over a year is refused for the
retention, whichever unit expresses it — `2 YEARS` and `400 DAYS` are both too
long.

## Evidence and current boundary

- `internal/confluence/audit_test.go` covers all six operations, that the whole
  log is administration, that a record carries its affected object and changed
  values and takes the caller as its author, the search across summary,
  description and category, a date range that matches nothing, both export
  formats carrying identical rows, every refused period, and that shortening the
  retention deletes the records that fall outside it.
- `migrations/154_wiki_audit.sql` is exercised from a clean PostgreSQL schema.

Records are written through the API rather than raised automatically by the
operations that would produce them in Confluence, so the log holds what a client
puts there. Paging beyond the shared list helper, and the `superAdmin` flag
being derived rather than stated, remain.
