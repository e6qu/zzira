# Development continuity

Updated: 2026-09-09

This file is the short-lived handoff for the active branch. Stable scope,
dependencies, and acceptance gates are in [PLAN.md](../PLAN.md). Product and
contract status are in [CLOUD_PARITY.md](CLOUD_PARITY.md).

## Active delivery

- Branch: `feat/pr1-jira-platform-completion`
- Base: `origin/main` after merged PR #69
- Delivery unit: PR 1 — Jira Platform and project/site administration
- Pull request: not opened
- State: all 62 fresh-database browser journeys pass; final non-browser CI validation is next
- Blockers: none

## Merged baseline

PR [#69](https://github.com/e6qu/zzira/pull/69) merged the PR 0 baseline. Its 22
implementation checkpoints cover saved filters and subscriptions,
expanded JQL and app functions, stable search identity and paging, Jira votes,
watches, project components, login-date functions, JSM approval/SLA functions,
and durable bulk watch/unwatch, editable-field discovery, and field edits.

PR 0's final GitHub matrix passed the full Go and PostgreSQL suite, native and
WebAssembly builds, conformance checks, container build, dependency and security
scans, CodeQL, and all 59 Playwright journeys.

## Resume here

1. Run the full Go/PostgreSQL suite, native and WebAssembly builds, vet,
   conformance and repository security checks.
2. Push and open PR 1 with its completed checkpoints, validation evidence and
   remaining Jira Platform exit gates.
3. Reconcile the remaining Jira Platform operation inventory in subsequent PR 1
   checkpoints, with project and site administration next.

## Evidence map

- [Roadmap and completion gates](../PLAN.md)
- [Cloud/API status](CLOUD_PARITY.md)
- [Endpoint groups](../api/conformance/MATRIX.md)
- [Operation coverage](../api/conformance/cloud-coverage.json)
- [UI and persona journeys](UI_PARITY.md)
- [JQL and search](JQL.md)
- [Bulk work-item operations](BULK_ISSUES.md)
- [Jira attachment compatibility](ATTACHMENTS.md)

## Continuity rules

- Do not infer completeness from registered routes or grouped endpoint counts.
- Do not mark a journey complete without browser evidence for its outcome.
- Do not claim base-URL-only compatibility for clients that hardcode a central
  Atlassian host.
- Preserve unrelated work, inspect the tree before editing, and commit every
  checkpoint with its tests and documentation.
