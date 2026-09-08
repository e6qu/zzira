# Development continuity

Updated: 2026-09-09

This file is the short-lived handoff for the active branch. Stable scope, PR
boundaries, dependencies, and acceptance gates belong in
[PLAN.md](../PLAN.md). Product and contract status belong in
[CLOUD_PARITY.md](CLOUD_PARITY.md).

## Active delivery

- Branch: `feat/jira-platform-admin-completion`
- Base: `origin/main` after merged PR #68
- Delivery unit: PR 1 — Jira Platform and administration completion
- Pull request: not opened yet
- State: implementation in progress
- Current checkpoint: saved filters, sharing, subscriptions, and complete JQL
- Blockers: none

## Contract baseline

The generated inventory contains 1,207 pinned operations. Jira Cloud Platform
REST v3 owns 617 of them. The coverage ledger measures reviewed compatibility
evidence rather than route count; a registered path is not sufficient evidence.

PR 1 closes the Jira Platform and administration surface described in
[PLAN.md](../PLAN.md), including exact API behavior, the browser journeys that
exercise it, authorization, audit, durable background work, and ledger updates.

## Current checkpoint

The saved-filter implementation currently has basic create, read, update,
delete, and per-user favorite behavior. The pinned Jira contract contains 19
filter and sharing operations. This checkpoint will add:

- visible-filter permission evaluation and paginated filter search;
- exact favorite and owned-filter collections;
- private, authenticated, global, user, group, project, and project-role shares;
- filter-specific navigator columns and ownership transfer;
- default share scope and durable filter subscriptions;
- validation, audit records, API tests, and the browser management journey; and
- the JQL grammar and evaluation needed by filters, subscriptions, queues,
  automation, and later analytics work.

## Validation baseline

Merged PR #68 passed the complete GitHub CI matrix, including all 58 Playwright
journeys, Go tests, WebAssembly and native builds, `go vet`, CodeQL, gosec,
Semgrep, govulncheck, npm audit, container build, conformance tests, and generated
ledger freshness checks.

Each PR 1 checkpoint must run its focused tests before commit. Database changes
also run the PostgreSQL integration suite from an empty migrated database.

## Resume here

1. Complete the saved-filter persistence and permission model.
2. Expose the pinned filter operations with exact methods and response shapes.
3. Add the user and administrator browser journeys.
4. Expand JQL grammar, functions, history predicates, and deterministic paging.
5. Update the compatibility and persona ledgers with reviewed evidence.
6. Commit each independently buildable checkpoint and keep this handoff current.

## Evidence map

- [Roadmap and gates](../PLAN.md)
- [Cloud/API status](CLOUD_PARITY.md)
- [Endpoint groups](../api/conformance/MATRIX.md)
- [Operation coverage](../api/conformance/cloud-coverage.json)
- [UI and persona journeys](UI_PARITY.md)
- [Administration behavior](ADMIN.md)

## Continuity rules

- Do not infer API completeness from registered routes or the grouped matrix.
- Do not mark a journey complete without a browser test spanning its outcome.
- Do not claim base-URL-only compatibility for clients that hardcode a central
  Atlassian host.
- Preserve unrelated changes and inspect the working tree before editing.
- Commit every checkpoint with its tests and documentation.
