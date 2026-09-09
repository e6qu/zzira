# Development continuity

Updated: 2026-09-09

This file is the short-lived handoff for the active branch. Stable scope,
dependencies, and acceptance gates are in [PLAN.md](../PLAN.md). Product and
contract status are in [CLOUD_PARITY.md](CLOUD_PARITY.md).

## Active delivery

- Branch: `feat/jira-platform-admin-completion`
- Base: `origin/main` after merged PR #62 and PR #68
- Delivery unit: PR 0 — Cloud compatibility baseline consolidation
- Pull request: [#69](https://github.com/e6qu/zzira/pull/69)
- State: published; awaiting review and CI before merge
- Blockers: none

## PR 0 branch result

The branch contains 22 implementation commits covering saved filters and subscriptions,
expanded JQL and app functions, stable search identity and paging, Jira votes,
watches, project components, login-date functions, JSM approval/SLA functions,
and durable bulk watch/unwatch, editable-field discovery, and field edits.

The last implementation checkpoint passed the full uncached Go suite from an
empty migrated PostgreSQL database, focused bulk lifecycle tests, `go vet`,
native and WebAssembly builds, and conformance inventory/freshness checks. The
working tree was clean after commit `5411276`.

## Resume after PR 0

1. Create PR 1 from current `origin/main` for Jira Platform and project/site
   administration completion.
2. Implement durable bulk deletion with attachment cleanup, execution-time
   permission rechecks, bounded processing, per-item results, idempotent action
   records, and REST/UI lifecycle evidence.
3. Continue on the shared task model with bulk move and bulk transition.
4. Update compatibility evidence and commit every independently buildable
   checkpoint.

## Evidence map

- [Roadmap and completion gates](../PLAN.md)
- [Cloud/API status](CLOUD_PARITY.md)
- [Endpoint groups](../api/conformance/MATRIX.md)
- [Operation coverage](../api/conformance/cloud-coverage.json)
- [UI and persona journeys](UI_PARITY.md)
- [JQL and search](JQL.md)
- [Bulk work-item operations](BULK_ISSUES.md)

## Continuity rules

- Do not infer completeness from registered routes or grouped endpoint counts.
- Do not mark a journey complete without browser evidence for its outcome.
- Do not claim base-URL-only compatibility for clients that hardcode a central
  Atlassian host.
- Preserve unrelated work, inspect the tree before editing, and commit every
  checkpoint with its tests and documentation.
