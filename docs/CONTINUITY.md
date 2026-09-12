# Development continuity

Updated: 2026-09-13

This file is the short-lived handoff for the active branch. Stable scope,
dependencies, and acceptance gates are in [PLAN.md](../PLAN.md). Product and
contract status are in [CLOUD_PARITY.md](CLOUD_PARITY.md).

## Active delivery

- Branch: `feat/pr1-confluence-search`
- Base: `origin/main` after merged PR #108 (`5e065be`). The review queue is
  empty; this is the only open branch.
- Delivery unit: PR 1 — Jira Platform and project/site administration
- State: CQL search checkpoint audited both pinned operations against a running
  server and found neither working. They needed a query language the product
  did not have, so `internal/cql` is a new package on the shape of the existing
  JQL engine: a hand-rolled recursive-descent parser and a PostgreSQL compiler,
  pure so the server and the WebAssembly replica share it.
  The compiler reads a normalised view — pages, blog posts, comments,
  attachments and spaces reduced to one set of columns — which the store builds
  with each branch's own visibility rules already applied. A query can then
  only narrow what a reader sees; the test proves it by restricting a page and
  checking it is absent from another reader's results *and* from their total.
  A field the engine does not know is refused rather than dropped, because a
  dropped condition returns more than was asked for. Values never reach the
  statement: everything a reader writes becomes an argument, and a typed `%`
  is escaped so a search for "100%" means those four characters.
  Two behaviours came straight from what Confluence does rather than from
  choice: a bare date names a whole day, so equality is the day; and drafts are
  not searched unless `cqlcontext` asks for them.
  Ordering always settles ties on type and id — without that, paging would show
  one row twice and miss another, which is the same class of bug the sprint and
  page ordering work hit twice.
  Clean migrations, the full uncached Go/PostgreSQL suite, vet, native and
  WebAssembly builds, conformance checks and every Playwright journey pass.
- Blockers: none

## Merged baseline

PR [#108](https://github.com/e6qu/zzira/pull/108) merged Confluence content
relations on top of
PR [#107](https://github.com/e6qu/zzira/pull/107), which merged content history,
macros and body conversion, on top of
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

1. Monitor the CQL search pull request through every CI job and resolve review
   threads inline. **A stacked branch gets no CI here**: `.github/workflows/ci.yml`
   triggers only on `pull_request` against `main`, and `gh pr checks` reports
   "no checks reported" rather than a failure, so a stacked PR can look fine and
   be unverified. Base every PR on `main` unless it genuinely needs a helper an
   open branch adds.
2. `confluence-v1` has two operations left, both `analytics`: the view count on
   a piece of content and the people who viewed it. Nothing records a view yet,
   so that checkpoint is a model before it is a handler — the tenth in a row.
   Finishing it closes `confluence-v1` entirely. After Confluence, the largest
   untouched block is `jira-service-management` (75).
3. Probe every operation against a running server before assessing; the ten
   audits so far ran 11 of 15, 8 of 33, 5 of 13, 16 of 17, 4 of 14, 2 of 11,
   0 of 17, 0 of 8, 0 of 5 and 0 of 2, so the result is not predictable from how
   well tested a surface looks. Several times an operation was missing because
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
- [Confluence CQL search](CQL_SEARCH.md)

## Continuity rules

- Do not infer completeness from registered routes or grouped endpoint counts.
- Do not mark a journey complete without browser evidence for its outcome.
- Do not claim base-URL-only compatibility for clients that hardcode a central
  Atlassian host.
- Preserve unrelated work, inspect the tree before editing, and commit every
  checkpoint with its tests and documentation.
