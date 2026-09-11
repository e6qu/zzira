# Development continuity

Updated: 2026-09-11

This file is the short-lived handoff for the active branch. Stable scope,
dependencies, and acceptance gates are in [PLAN.md](../PLAN.md). Product and
contract status are in [CLOUD_PARITY.md](CLOUD_PARITY.md).

## Active delivery

- Branch: `feat/pr1-field-audit`
- Base: `origin/main` after merged PR #92 (`d5b8509`)
- Delivery unit: PR 1 — Jira Platform and project/site administration
- State: field checkpoint audited all 11 pinned issue-field operations against a
  running server and found two working, `GET /field` and `POST /field`. The
  other nine are new here: the live and trashed paginated searches, the rename,
  the project associations and contexts reads, the project field read, and the
  trash lifecycle. Jira does not delete a custom field on request — it trashes
  it, and only a trashed field can be deleted — so `trashed_at` is the state and
  every read that serves live work excludes it, leaving recorded values intact
  for a restore. `GET /field` reported five hand-written system fields and now
  reports the set the search resolves through. `POST /field` rejected Jira's
  canonical type keys, which is what a real client sends. And
  `GET /field/{id}/contexts` answered 405 because the context router matched any
  path containing `/context`.
  Clean migrations, the full uncached Go/PostgreSQL suite, vet, native and
  WebAssembly builds, conformance checks, every Playwright journey, 320 px
  reflow, and the light/dark axe sweep pass.
- Blockers: none

## Merged baseline

PR [#92](https://github.com/e6qu/zzira/pull/92) merged the completed worklog
surface, whose feeds needed a stamp on the worklog and a tombstone on the
delete, on top of
PR [#91](https://github.com/e6qu/zzira/pull/91), which merged the completed dashboard
surface on top of
PR [#90](https://github.com/e6qu/zzira/pull/90), which merged the completed sprint
surface on top of
PR [#89](https://github.com/e6qu/zzira/pull/89), which merged the completed board
surface on top of
PR [#88](https://github.com/e6qu/zzira/pull/88), which merged the completed project
version surface on top of
PR [#87](https://github.com/e6qu/zzira/pull/87), which merged select custom fields and
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

PR #90's final GitHub matrix passed its required suites before merge.

## Resume here

1. Monitor the field pull request through every CI job and resolve review
   threads inline.
2. The field families adjacent to this one are still unassessed and were probed
   as missing: `PUT`/`DELETE /field/association` (2), the legacy
   `/field/{fieldKey}/option` surface (8) and `/config/fieldschemes` (3). The
   option surface is Jira's older per-field option API and overlaps the
   context-scoped options already delivered, so check whether it can reach the
   same store before adding a second one.
3. Probe every operation against a running server before assessing; the six
   audits so far ran 11 of 15, 8 of 33, 5 of 13, 16 of 17, 4 of 14 and 2 of 11,
   so the result is not predictable from how well tested a surface looks.

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
- [Jira Software boards and sprints](AGILE_BOARDS.md)
- [Jira dashboards](DASHBOARDS_API.md)
- [Jira worklogs](WORKLOGS.md)
- [Jira issue fields](ISSUE_FIELDS.md)

## Continuity rules

- Do not infer completeness from registered routes or grouped endpoint counts.
- Do not mark a journey complete without browser evidence for its outcome.
- Do not claim base-URL-only compatibility for clients that hardcode a central
  Atlassian host.
- Preserve unrelated work, inspect the tree before editing, and commit every
  checkpoint with its tests and documentation.
