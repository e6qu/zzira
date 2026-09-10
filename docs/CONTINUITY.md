# Development continuity

Updated: 2026-09-10

This file is the short-lived handoff for the active branch. Stable scope,
dependencies, and acceptance gates are in [PLAN.md](../PLAN.md). Product and
contract status are in [CLOUD_PARITY.md](CLOUD_PARITY.md).

## Active delivery

- Branch: `feat/pr1-permission-schemes`
- Base: `origin/main` after merged PR #74 (`8e66a66`)
- Delivery unit: PR 1 — Jira Platform and project/site administration
- Pull request: [#75](https://github.com/e6qu/zzira/pull/75)
- State: permission-scheme checkpoint implements all 16 pinned scheme,
  assignment, discovery, and evaluation operations; durable grants and defaults;
  delegated project administration; project, issue, search, sync, and navigation
  authorization; immutable actions; and responsive site/project journeys.
  Clean migrations, the full uncached Go/PostgreSQL suite, vet, native and
  WebAssembly builds, conformance checks, all 64 Playwright journeys, and axe
  coverage for both new pages pass.
- Blockers: none

## Merged baseline

PR [#74](https://github.com/e6qu/zzira/pull/74) merged Jira project roles on top
of PR [#73](https://github.com/e6qu/zzira/pull/73), which merged project lifecycle on top
of PR [#72](https://github.com/e6qu/zzira/pull/72), which merged Jira project
governance. PR #71 merged Jira site configuration. PR #70 merged the first PR 1 delivery and
adds durable bulk delete/move/transition work, Jira attachment lifecycle and
browser/CI hardening on top of PR [#69](https://github.com/e6qu/zzira/pull/69),
which merged the PR 0 baseline. PR 0's 22
implementation checkpoints cover saved filters and subscriptions,
expanded JQL and app functions, stable search identity and paging, Jira votes,
watches, project components, login-date functions, JSM approval/SLA functions,
and durable bulk watch/unwatch, editable-field discovery, and field edits.

PR #74's final GitHub matrix passed its required suites before merge.

## Resume here

1. Monitor PR #75 through every CI job and resolve review threads inline.
2. After it merges, continue notification, issue-security, field, and screen
   scheme administration. Keep each vertical checkpoint committed, tested, and
   documented until the Jira Platform exit gates are satisfied.

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
- [Jira project roles and people](PROJECT_ROLES.md)
- [Jira permission schemes](PERMISSION_SCHEMES.md)

## Continuity rules

- Do not infer completeness from registered routes or grouped endpoint counts.
- Do not mark a journey complete without browser evidence for its outcome.
- Do not claim base-URL-only compatibility for clients that hardcode a central
  Atlassian host.
- Preserve unrelated work, inspect the tree before editing, and commit every
  checkpoint with its tests and documentation.
