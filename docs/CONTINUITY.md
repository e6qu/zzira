# Development continuity

Updated: 2026-09-10

This file is the short-lived handoff for the active branch. Stable scope,
dependencies, and acceptance gates are in [PLAN.md](../PLAN.md). Product and
contract status are in [CLOUD_PARITY.md](CLOUD_PARITY.md).

## Active delivery

- Branch: `feat/pr1-project-versions`
- Base: `origin/main` after merged PR #87 (`202954d`)
- Delivery unit: PR 1 — Jira Platform and project/site administration
- Pull request: pending
- State: project-version checkpoint completes and records a surface that was
  largely built but never assessed. Eleven of the 15 pinned operations already
  worked; this adds the missing five — explicit version ordering and the four
  release related-work operations — and assesses all 15 against verified
  behaviour rather than assuming the rest were fine. Clean migrations, the full
  uncached Go/PostgreSQL suite, vet, native and WebAssembly builds, conformance
  checks, every Playwright journey, 320 px reflow, and the light/dark axe sweep
  pass.
- Blockers: none

## Merged baseline

PR [#87](https://github.com/e6qu/zzira/pull/87) merged select custom fields and
their options on top of
PR [#86](https://github.com/e6qu/zzira/pull/86), which merged context enforcement on
writes on top of
PR [#85](https://github.com/e6qu/zzira/pull/85), which merged bulk edit narrowing on
top of
PR [#84](https://github.com/e6qu/zzira/pull/84), which merged Jira custom field
contexts on top of
PR [#82](https://github.com/e6qu/zzira/pull/82), which merged priority resolution on
edit and the sticky-header scroll offset on top of
PR [#81](https://github.com/e6qu/zzira/pull/81), which merged field-rule enforcement on
edit and transition on top of
PR [#80](https://github.com/e6qu/zzira/pull/80), which merged Jira field configurations
on top of
PR [#79](https://github.com/e6qu/zzira/pull/79), which merged the binding of screens to
work item forms on top of
PR [#78](https://github.com/e6qu/zzira/pull/78), which merged Jira screens, tabs and
tab fields on top of
PR [#77](https://github.com/e6qu/zzira/pull/77), which merged Jira issue security
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

PR #87's final GitHub matrix passed its required suites before merge.

## Resume here

1. Open the project-version pull request, monitor it through every CI job, and
   resolve review threads inline.
2. Several other families are built but unassessed in the same way versions
   were: Dashboards, Issue worklogs, Issue fields, and Jira Software's Board and
   Sprint groups all have tests and browser journeys but no operation-level
   evidence. Auditing one means checking every pinned operation against the
   running server first, since eleven of fifteen worked here and four did not.

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
- [Jira screen schemes](SCREEN_SCHEMES.md)
- [Jira field configurations](FIELD_CONFIGURATIONS.md)
- [Jira custom field contexts](CUSTOM_FIELD_CONTEXTS.md)
- [Jira select custom fields](CUSTOM_FIELD_OPTIONS.md)
- [Jira project versions](PROJECT_VERSIONS.md)

## Continuity rules

- Do not infer completeness from registered routes or grouped endpoint counts.
- Do not mark a journey complete without browser evidence for its outcome.
- Do not claim base-URL-only compatibility for clients that hardcode a central
  Atlassian host.
- Preserve unrelated work, inspect the tree before editing, and commit every
  checkpoint with its tests and documentation.
