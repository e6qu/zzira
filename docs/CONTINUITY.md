# Development continuity

Updated: 2026-09-10

This file is the short-lived handoff for the active branch. Stable scope,
dependencies, and acceptance gates are in [PLAN.md](../PLAN.md). Product and
contract status are in [CLOUD_PARITY.md](CLOUD_PARITY.md).

## Active delivery

- Branch: `feat/pr1-screens`
- Base: `origin/main` after merged PR #77 (`440cadc`)
- Delivery unit: PR 1 — Jira Platform and project/site administration
- Pull request: [#78](https://github.com/e6qu/zzira/pull/78)
- State: screen checkpoint implements all 17 pinned screen, screen-tab, and
  tab-field operations, sequence-backed identifiers, a validated field catalog
  covering built-in and custom fields, dense tab and field ordering with every
  Jira move form, structural guards that keep one tab per screen and protect the
  workspace default screen, immutable configuration actions, and a responsive
  admin journey. Screens are a definition layer: screen schemes and issue type
  screen schemes bind them to work item forms in the next checkpoint. Clean
  migrations, the full uncached Go/PostgreSQL suite, vet, native and WebAssembly
  builds, conformance checks, every Playwright journey, 320 px reflow, and the
  light/dark axe sweep pass.
- Blockers: none

## Merged baseline

PR [#77](https://github.com/e6qu/zzira/pull/77) merged Jira issue security
schemes and enforcement on top of
PR [#76](https://github.com/e6qu/zzira/pull/76), which merged Jira notification schemes
and issue-event delivery on top of PR
[#75](https://github.com/e6qu/zzira/pull/75), which merged Jira permission
schemes and authorization on top of
PR [#74](https://github.com/e6qu/zzira/pull/74), which merged Jira project roles on top
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

PR #77's final GitHub matrix passed its required suites before merge.

## Resume here

1. Monitor PR #78 through every CI job and resolve review threads inline.
2. After merge, add screen schemes and issue type screen schemes so a screen
   reaches create, edit, and view forms, then field configurations and field
   configuration schemes.

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
- [Jira issue security schemes](ISSUE_SECURITY_SCHEMES.md)
- [Jira screens](SCREENS.md)

## Continuity rules

- Do not infer completeness from registered routes or grouped endpoint counts.
- Do not mark a journey complete without browser evidence for its outcome.
- Do not claim base-URL-only compatibility for clients that hardcode a central
  Atlassian host.
- Preserve unrelated work, inspect the tree before editing, and commit every
  checkpoint with its tests and documentation.
