# Development continuity

Updated: 2026-09-09

This file is the short-lived handoff for the active branch. Stable scope, PR
boundaries, dependencies, and acceptance gates belong in
[PLAN.md](../PLAN.md). Product and contract status belong in
[CLOUD_PARITY.md](CLOUD_PARITY.md).

## Active delivery

- Branch: `feat/jira-platform-admin-completion`
- Base: `origin/main` after merged PR #62 and PR #68
- Delivery unit: PR 1 — Jira Platform and administration completion
- Pull request: not opened yet
- State: implementation in progress
- Current checkpoint: saved-filter management, JQL/search expansion, and seven
  JQL helper operations implemented and tested; app precomputations next
- Blockers: none

## Contract baseline

The generated inventory contains 1,207 pinned operations. Jira Cloud Platform
REST v3 owns 617 of them. The coverage ledger measures reviewed compatibility
evidence rather than route count; a registered path is not sufficient evidence.

PR 1 closes the Jira Platform and administration surface described in
[PLAN.md](../PLAN.md), including exact API behavior, the browser journeys that
exercise it, authorization, audit, durable background work, and ledger updates.

## Current checkpoint

The 19 pinned Jira filter operations now have reviewed partial evidence. The
shared model covers permission-filtered collections, view/edit shares,
favorites, columns, ownership, default scope, audit, and subscription schema.
The browser directory completes the owner and site-administrator management
journey and is connected to REST-created filters by Playwright.

The first shared JQL checkpoint adds `NOT IN`, relative date functions,
immutable `WAS`/`CHANGED` history predicates, seven-field deterministic
ordering, and seven-day enhanced-search cursors bound to the query, workspace,
and user. Legacy, enhanced, and approximate-count search paths now have direct
integration evidence. Issue visibility remains enforced before pagination and
serialization.

The seven pinned reference/suggestion, parse, match, sanitize, and personal-data
migration operations now share the same parser, compiler, field registry, and
permission-filtered issue search. The next checkpoint implements the three
app-function precomputation operations with tenant-scoped durable storage, then
continues field/function and reconciliation semantics.

## Validation baseline

Merged PR #68 passed the complete GitHub CI matrix, including all 58 Playwright
journeys, Go tests, WebAssembly and native builds, `go vet`, CodeQL, gosec,
Semgrep, govulncheck, npm audit, container build, conformance tests, and generated
ledger freshness checks.

Each PR 1 checkpoint must run its focused tests before commit. Database changes
also run the PostgreSQL integration suite from an empty migrated database.

## Resume here

1. Implement and test the three app-function precomputation resources.
2. Complete function, multi-value field, expansion, property, reconciliation,
   and snapshot/keyset search semantics.
3. Add filter-subscription scheduling and delivery after shared scheduled-work
   primitives are ready.
4. Continue into bulk work-item and project administration slices.
5. Update compatibility evidence and commit each independently buildable
   checkpoint.

## Evidence map

- [Roadmap and gates](../PLAN.md)
- [Cloud/API status](CLOUD_PARITY.md)
- [Endpoint groups](../api/conformance/MATRIX.md)
- [Operation coverage](../api/conformance/cloud-coverage.json)
- [UI and persona journeys](UI_PARITY.md)
- [Administration behavior](ADMIN.md)
- [Saved filters and sharing](FILTERS.md)
- [JQL and issue search](JQL.md)

## Continuity rules

- Do not infer API completeness from registered routes or the grouped matrix.
- Do not mark a journey complete without a browser test spanning its outcome.
- Do not claim base-URL-only compatibility for clients that hardcode a central
  Atlassian host.
- Preserve unrelated changes and inspect the working tree before editing.
- Commit every checkpoint with its tests and documentation.
