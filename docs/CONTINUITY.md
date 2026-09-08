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
- Current checkpoint: saved-filter API, permission model, and browser management
  journey implemented and tested; JQL/search completion next
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

The next checkpoint expands JQL grammar and evaluation shared by issue search,
saved filters, subscriptions, service queues, automation, and later analytics.
It must preserve deterministic ordering and enforce issue visibility before
pagination or serialization.

## Validation baseline

Merged PR #68 passed the complete GitHub CI matrix, including all 58 Playwright
journeys, Go tests, WebAssembly and native builds, `go vet`, CodeQL, gosec,
Semgrep, govulncheck, npm audit, container build, conformance tests, and generated
ledger freshness checks.

Each PR 1 checkpoint must run its focused tests before commit. Database changes
also run the PostgreSQL integration suite from an empty migrated database.

## Resume here

1. Expand JQL grammar, functions, history predicates, and deterministic paging.
2. Add filter-subscription scheduling and delivery after shared scheduled-work
   primitives are ready.
3. Continue into bulk work-item and project administration slices.
4. Update compatibility and persona evidence with each tested behavior.
5. Commit each independently buildable checkpoint and keep this handoff current.

## Evidence map

- [Roadmap and gates](../PLAN.md)
- [Cloud/API status](CLOUD_PARITY.md)
- [Endpoint groups](../api/conformance/MATRIX.md)
- [Operation coverage](../api/conformance/cloud-coverage.json)
- [UI and persona journeys](UI_PARITY.md)
- [Administration behavior](ADMIN.md)
- [Saved filters and sharing](FILTERS.md)

## Continuity rules

- Do not infer API completeness from registered routes or the grouped matrix.
- Do not mark a journey complete without a browser test spanning its outcome.
- Do not claim base-URL-only compatibility for clients that hardcode a central
  Atlassian host.
- Preserve unrelated changes and inspect the working tree before editing.
- Commit every checkpoint with its tests and documentation.
