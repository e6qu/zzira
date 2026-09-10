# Development continuity

Updated: 2026-09-10

This file is the short-lived handoff for the active branch. Stable scope,
dependencies, and acceptance gates are in [PLAN.md](../PLAN.md). Product and
contract status are in [CLOUD_PARITY.md](CLOUD_PARITY.md).

## Active delivery

- Branch: `feat/pr1-notification-schemes`
- Base: `origin/main` after merged PR #75 (`f6cbec8`)
- Delivery unit: PR 1 — Jira Platform and project/site administration
- Pull request: pending
- State: notification-scheme checkpoint implements all nine pinned scheme and
  mapping operations, all 12 recipient types, default/project assignments,
  immutable configuration actions, idempotent permission and issue-security
  filtered inbox delivery, and durable leased email delivery for core issue
  events. Clean migrations, the full uncached Go/PostgreSQL suite, vet, native
  and WebAssembly builds, conformance checks, all 65 Playwright journeys, 320 px
  reflow, and the light/dark axe sweep pass.
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

PR #75's final GitHub matrix passed its required suites before merge.

## Resume here

1. Finish the complete local verification matrix, commit and open the
   notification-scheme pull request, then monitor every CI job and resolve
   review threads inline.
2. After merge, continue issue-security, field, and screen scheme
   administration, followed by custom notification events and preferences.

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
- [Jira notification schemes](NOTIFICATION_SCHEMES.md)

## Continuity rules

- Do not infer completeness from registered routes or grouped endpoint counts.
- Do not mark a journey complete without browser evidence for its outcome.
- Do not claim base-URL-only compatibility for clients that hardcode a central
  Atlassian host.
- Preserve unrelated work, inspect the tree before editing, and commit every
  checkpoint with its tests and documentation.
