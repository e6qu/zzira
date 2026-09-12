# Development continuity

Updated: 2026-09-13

This file is the short-lived handoff for the active branch. Stable scope,
dependencies, and acceptance gates are in [PLAN.md](../PLAN.md). Product and
contract status are in [CLOUD_PARITY.md](CLOUD_PARITY.md).

## Active delivery

- Branch: `feat/pr1-confluence-relations`
- Base: `origin/main` after merged PR #107 (`389646d`). The review queue is
  empty; this is the only open branch.
- Delivery unit: PR 1 — Jira Platform and project/site administration
- State: content relations checkpoint audited all five pinned operations
  against a running server and found none working: nothing in this product
  modelled a named link between two entities, so once again the gap was in the
  model rather than in a missing handler.
  A relation is one way. The two listings therefore answer different questions,
  and the tests check that a sibling created from one page to another is not
  found by listing from the other — a symmetric implementation would have passed
  every status-code assertion.
  Confluence qualifies a content end of a relation with a status and a version,
  so those are part of the key: a relation to a past revision is a separate
  relation from one to the content as it stands, and a listing sees only the
  status it asked for.
  `favourite` runs from a person to a space or content, and is read through its
  own endpoints rather than by listing relations, so the listings refuse it
  instead of answering with an empty page. A relation whose source is a user is
  that person's own statement, so making or unmaking one for somebody else is
  site administration.
  Both writes are idempotent, which is what the operations mean: creating an
  existing relation answers 200 and deleting a missing one answers 204, while an
  entity that does not exist is still 404.
  Clean migrations, the full uncached Go/PostgreSQL suite, vet, native and
  WebAssembly builds, conformance checks and every Playwright journey pass.
- Blockers: none

## Merged baseline

PR [#107](https://github.com/e6qu/zzira/pull/107) merged content history, macros
and body conversion on top of
PR [#106](https://github.com/e6qu/zzira/pull/106), which merged the Confluence audit
log, on top of
PR [#105](https://github.com/e6qu/zzira/pull/105), which merged content templates and
blueprints, on top of
PR [#104](https://github.com/e6qu/zzira/pull/104), which merged Confluence site
settings, on top of
PR [#103](https://github.com/e6qu/zzira/pull/103), which merged the space lifecycle,
on top of
PR [#102](https://github.com/e6qu/zzira/pull/102), which merged the space permission
transition, on top of
PR [#101](https://github.com/e6qu/zzira/pull/101), which merged space permissions, on
top of
PR [#100](https://github.com/e6qu/zzira/pull/100), which merged Confluence groups, on
top of
PR [#98](https://github.com/e6qu/zzira/pull/98), which merged page moves, copies and
archiving, on top of
PR [#97](https://github.com/e6qu/zzira/pull/97), which merged content states, on top of
PR [#99](https://github.com/e6qu/zzira/pull/99), which merged the Confluence user
surface, on top of
PR [#96](https://github.com/e6qu/zzira/pull/96), which merged Confluence custom content
and carried the app-provided select lists and their options that
PR [#95](https://github.com/e6qu/zzira/pull/95) had prepared; #95 itself was closed
rather than merged, because its branch predated #96 and its diff against `main`
would have deleted the custom content work. Those sit on top of
PR [#94](https://github.com/e6qu/zzira/pull/94), which merged the field association
scheme surface, served from this product's field configuration scheme, on top of
PR [#93](https://github.com/e6qu/zzira/pull/93), which merged the completed issue field
surface, including its trash lifecycle, on top of
PR [#92](https://github.com/e6qu/zzira/pull/92), which merged the completed worklog
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

1. Monitor the content relations pull request through every CI job and resolve
   review threads inline. **A stacked branch gets no CI here**: `.github/workflows/ci.yml`
   triggers only on `pull_request` against `main`, and `gh pr checks` reports
   "no checks reported" rather than a failure, so a stacked PR can look fine and
   be unverified. Base every PR on `main` unless it genuinely needs a helper an
   open branch adds.
2. What is left of `confluence-v1` is five operations: `search` (2, the CQL
   content and user search), `analytics` (2) and `content/search` (1). CQL
   content search is the natural next unit — it is the one of the five that the
   others read against, and this product has a search of its own to serve it
   from. After Confluence, the largest untouched block is
   `jira-service-management` (75).
3. Probe every operation against a running server before assessing; the nine
   audits so far ran 11 of 15, 8 of 33, 5 of 13, 16 of 17, 4 of 14, 2 of 11,
   0 of 17, 0 of 8 and 0 of 5, so the result is not predictable from how well
   tested a surface looks. Three times now an operation was missing because
   something upstream made it impossible, not because the handler was absent.

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
- [Jira field association schemes](FIELD_ASSOCIATION_SCHEMES.md)
- [App-provided select lists](APP_FIELD_OPTIONS.md)
- [Confluence custom content](CUSTOM_CONTENT.md)
- [Confluence users](WIKI_USERS.md)
- [Confluence groups](WIKI_GROUPS.md)
- [Confluence space permissions](SPACE_PERMISSIONS.md)
- [Space permission transition](SPACE_PERMISSION_TRANSITION.md)
- [Confluence space lifecycle](SPACE_LIFECYCLE.md)
- [Confluence site settings](SITE_SETTINGS.md)
- [Confluence content templates](CONTENT_TEMPLATES.md)
- [Confluence audit log](WIKI_AUDIT.md)
- [Confluence content history](CONTENT_HISTORY.md)
- [Confluence content states](CONTENT_STATES.md)
- [Confluence page moves and copies](PAGE_MOVES.md)
- [Confluence content relations](CONTENT_RELATIONS.md)

## Continuity rules

- Do not infer completeness from registered routes or grouped endpoint counts.
- Do not mark a journey complete without browser evidence for its outcome.
- Do not claim base-URL-only compatibility for clients that hardcode a central
  Atlassian host.
- Preserve unrelated work, inspect the tree before editing, and commit every
  checkpoint with its tests and documentation.
