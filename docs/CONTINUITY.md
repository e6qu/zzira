# Development continuity

Updated: 2026-09-09

This file is the short-lived handoff for the active branch. Stable scope,
dependencies, and acceptance gates are in [PLAN.md](../PLAN.md). Product and
contract status are in [CLOUD_PARITY.md](CLOUD_PARITY.md).

## Active delivery

- Branch: `feat/pr1-project-lifecycle-4`
- Base: `origin/main` after merged PR #72 (`c14e08a`)
- Delivery unit: PR 1 — Jira Platform and project/site administration
- Pull request: [#73](https://github.com/e6qu/zzira/pull/73)
- State: project-lifecycle checkpoint implements recent projects, archive,
  restore, trash, synchronous/asynchronous deletion, automatic 60-day cleanup,
  replica coherence, attachment cleanup, and browser administration; the full
  Go/PostgreSQL suite, native and WebAssembly builds, vet, conformance, and all
  62 Playwright journeys pass
- Blockers: none

## Merged baseline

PR [#72](https://github.com/e6qu/zzira/pull/72) merged Jira project governance on
top of PR [#71](https://github.com/e6qu/zzira/pull/71), which merged the Jira
site-configuration checkpoint. PR #70 merged the first PR 1 delivery and
adds durable bulk delete/move/transition work, Jira attachment lifecycle and
browser/CI hardening on top of PR [#69](https://github.com/e6qu/zzira/pull/69),
which merged the PR 0 baseline. PR 0's 22
implementation checkpoints cover saved filters and subscriptions,
expanded JQL and app functions, stable search identity and paging, Jira votes,
watches, project components, login-date functions, JSM approval/SLA functions,
and durable bulk watch/unwatch, editable-field discovery, and field edits.

PR #71's final GitHub matrix passed the full Go and PostgreSQL suite, native and
WebAssembly builds, conformance checks, container build, dependency and security
scans, CodeQL, and all 62 Playwright journeys.

## Resume here

1. Monitor PR #73 through every CI job and resolve review threads inline.
2. Continue roles, templates, and scheme administration after this checkpoint
   merges. Keep each new vertical checkpoint committed, tested, and documented
   on the same PR until its Jira Platform exit gates are satisfied.

## Evidence map

- [Roadmap and completion gates](../PLAN.md)
- [Cloud/API status](CLOUD_PARITY.md)
- [Endpoint groups](../api/conformance/MATRIX.md)
- [Operation coverage](../api/conformance/cloud-coverage.json)
- [UI and persona journeys](UI_PARITY.md)
- [JQL and search](JQL.md)
- [Bulk work-item operations](BULK_ISSUES.md)
- [Jira attachment compatibility](ATTACHMENTS.md)
- [Jira site configuration](JIRA_SITE_CONFIGURATION.md)
- [Jira project governance](PROJECT_GOVERNANCE.md)
- [Jira project lifecycle](PROJECT_LIFECYCLE.md)

## Continuity rules

- Do not infer completeness from registered routes or grouped endpoint counts.
- Do not mark a journey complete without browser evidence for its outcome.
- Do not claim base-URL-only compatibility for clients that hardcode a central
  Atlassian host.
- Preserve unrelated work, inspect the tree before editing, and commit every
  checkpoint with its tests and documentation.
