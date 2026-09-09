# Development continuity

Updated: 2026-09-09

This file is the short-lived handoff for the active branch. Stable scope,
dependencies, and acceptance gates are in [PLAN.md](../PLAN.md). Product and
contract status are in [CLOUD_PARITY.md](CLOUD_PARITY.md).

## Active delivery

- Branch: `feat/pr1-project-administration-3`
- Base: `origin/main` after merged PR #71 (`c9d834d`)
- Delivery unit: PR 1 — Jira Platform and project/site administration
- Pull request: not opened yet
- State: project-governance checkpoint in development and locally verified
- Blockers: none

## Merged baseline

PR [#71](https://github.com/e6qu/zzira/pull/71) merged the Jira site-configuration
checkpoint on top of PR [#70](https://github.com/e6qu/zzira/pull/70), which merged the first PR 1 delivery. PR #70
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

1. Finish and commit the project-governance checkpoint: categories, properties,
   features, sender email, project types, validation, and browser administration.
2. Continue project lifecycle, roles, templates, and scheme administration.
3. Keep each new vertical checkpoint committed, tested and documented on the
   same PR until its Jira Platform exit gates are satisfied.

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

## Continuity rules

- Do not infer completeness from registered routes or grouped endpoint counts.
- Do not mark a journey complete without browser evidence for its outcome.
- Do not claim base-URL-only compatibility for clients that hardcode a central
  Atlassian host.
- Preserve unrelated work, inspect the tree before editing, and commit every
  checkpoint with its tests and documentation.
