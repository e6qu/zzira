# Development continuity

Updated: 2026-09-09

This file is the short-lived handoff for the active branch. Stable scope, PR
boundaries, dependencies, and acceptance gates belong in
[PLAN.md](../PLAN.md). Product and contract status belong in
[CLOUD_PARITY.md](CLOUD_PARITY.md).

## Active delivery

- Branch: `feat/jira-platform-admin-completion`
- Base: `origin/main` after merged PR #62 and PR #68
- Delivery unit: PR 1 — Jira Platform and administration completion
- Pull request: not opened yet
- State: implementation in progress
- Current checkpoint: saved-filter management, JQL grammar/helpers/app
  precomputations, complete search projection/expansion plumbing, immutable
  numeric Jira issue IDs, strong-consistency reconciliation, and durable
  enhanced-search result snapshots, plus relation-backed Jira list functions
  and multi-value fields implemented
- Blockers: none

## Contract baseline

The generated inventory contains 1,207 pinned operations. Jira Cloud Platform
REST v3 owns 617 of them. The coverage ledger measures reviewed compatibility
evidence rather than route count; a registered path is not sufficient evidence.

PR 1 closes the Jira Platform and administration surface described in
[PLAN.md](../PLAN.md), including exact API behavior, the browser journeys that
exercise it, authorization, audit, durable background work, and ledger updates.

## Current checkpoint

The 19 pinned Jira filter operations now have reviewed partial evidence. The
shared model covers permission-filtered collections, view/edit shares,
favorites, columns, ownership, default scope, audit, and subscription schema.
The browser directory completes the owner and site-administrator management
journey and is connected to REST-created filters by Playwright.

The first shared JQL checkpoint adds `NOT IN`, relative date functions,
immutable `WAS`/`CHANGED` history predicates, seven-field deterministic
ordering, and seven-day enhanced-search cursors bound to the query, workspace,
and user. Legacy, enhanced, and approximate-count search paths now have direct
integration evidence. Issue visibility remains enforced before pagination and
serialization.

The seven pinned reference/suggestion, parse, match, sanitize, and personal-data
migration operations share the same parser, compiler, field registry, and
permission-filtered issue search. The three app-function precomputation
operations now use durable installation-owned records, app-principal
authorization, paging and filtering, ID search, and atomic value/error updates.
Compiler invocation of registered app functions remains separate work.

Legacy and enhanced search now share strict option validation, selected-field
projection, installed-app field-key aliases, names/schema/rendered expansion,
permission-safe requested issue properties, executable transitions, issue
operations, edit metadata, immutable changelogs, and current versioned
representations. Approximate count enforces its bounded-query contract.
REST issue resources, search, JQL bulk matching, and JSM now expose immutable
numeric Jira IDs while sync/actions retain stable `iss_*` identities. Enhanced
search accepts reconciliation IDs; its primary-database query is already
strongly consistent. Continuation tokens page through durable result positions,
remain fixed when matching issues change, and recheck visibility on every page.
Numeric issue-ID JQL now targets the public Jira identity. Group membership,
linked issue, sprint-state, and standard/subtask type functions compile against
their canonical relations; sprint, label, and version negation excludes empty
values consistently with Jira.

## Validation baseline

Merged PR #68 passed the complete GitHub CI matrix, including all 58 Playwright
journeys, Go tests, WebAssembly and native builds, `go vet`, CodeQL, gosec,
Semgrep, govulncheck, npm audit, container build, conformance tests, and generated
ledger freshness checks.

Each PR 1 checkpoint must run its focused tests before commit. Database changes
also run the PostgreSQL integration suite from an empty migrated database.

## Resume here

1. Add installed-app precomputation invocation and the remaining built-in
   function and multi-value field semantics.
2. Add filter-subscription scheduling and delivery after shared scheduled-work
   primitives are ready.
3. Continue into bulk work-item and project administration slices.
4. Update compatibility evidence and commit each independently buildable
   checkpoint.

## Evidence map

- [Roadmap and gates](../PLAN.md)
- [Cloud/API status](CLOUD_PARITY.md)
- [Endpoint groups](../api/conformance/MATRIX.md)
- [Operation coverage](../api/conformance/cloud-coverage.json)
- [UI and persona journeys](UI_PARITY.md)
- [Administration behavior](ADMIN.md)
- [Saved filters and sharing](FILTERS.md)
- [JQL and issue search](JQL.md)

## Continuity rules

- Do not infer API completeness from registered routes or the grouped matrix.
- Do not mark a journey complete without a browser test spanning its outcome.
- Do not claim base-URL-only compatibility for clients that hardcode a central
  Atlassian host.
- Preserve unrelated changes and inspect the working tree before editing.
- Commit every checkpoint with its tests and documentation.
