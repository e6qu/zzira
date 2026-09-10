# Development continuity

Updated: 2026-09-09

This file is the short-lived handoff for the active branch. Stable scope,
dependencies, and acceptance gates are in [PLAN.md](../PLAN.md). Product and
contract status are in [CLOUD_PARITY.md](CLOUD_PARITY.md).

## Active delivery

- Branch: `feat/pr1-project-roles`
- Base: `origin/main` after merged PR #73 (`34c9776`)
- Delivery unit: PR 1 — Jira Platform and project/site administration
- Pull request: [#74](https://github.com/e6qu/zzira/pull/74)
- State: project-role checkpoint implements the complete 15-operation Jira v3
  role/actor surface, reusable defaults, project assignments, delegated project
  administration, safe swaps, role-backed filter sharing, immutable actions,
  and responsive site/project browser journeys; clean migrations, the complete
  Go/PostgreSQL suite, vet, native/WebAssembly builds, conformance checks, all
  63 Playwright journeys, and axe coverage for both new pages pass
- Blockers: none

## Merged baseline

PR [#73](https://github.com/e6qu/zzira/pull/73) merged project lifecycle on top
of PR [#72](https://github.com/e6qu/zzira/pull/72), which merged Jira project
governance. PR #71 merged Jira site configuration. PR #70 merged the first PR 1 delivery and
adds durable bulk delete/move/transition work, Jira attachment lifecycle and
browser/CI hardening on top of PR [#69](https://github.com/e6qu/zzira/pull/69),
which merged the PR 0 baseline. PR 0's 22
implementation checkpoints cover saved filters and subscriptions,
expanded JQL and app functions, stable search identity and paging, Jira votes,
watches, project components, login-date functions, JSM approval/SLA functions,
and durable bulk watch/unwatch, editable-field discovery, and field edits.

PR #73's final GitHub matrix passed its required suites before merge.

## Resume here

1. Monitor PR #74 through every CI job and resolve review threads inline.
2. After it merges, continue permission, notification, issue-security, field,
   and screen scheme administration. Keep each vertical checkpoint committed,
   tested, and documented until the Jira Platform exit gates are satisfied.

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

## Continuity rules

- Do not infer completeness from registered routes or grouped endpoint counts.
- Do not mark a journey complete without browser evidence for its outcome.
- Do not claim base-URL-only compatibility for clients that hardcode a central
  Atlassian host.
- Preserve unrelated work, inspect the tree before editing, and commit every
  checkpoint with its tests and documentation.
