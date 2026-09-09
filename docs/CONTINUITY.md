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
  and multi-value fields, installed-app JQL declarations, signed evaluation,
  durable expansion, and autocomplete, plus version/project/history/watch/vote
  built-ins and the complete issue-vote REST/browser journey, plus durable
  filter email schedules, recipient expansion, runs, and outbox delivery
  implemented; Jira project components and their REST, issue-field, assignment,
  JQL, audit, sync, and administrator journeys are implemented; durable login
  boundaries and `currentLogin()`/`lastLogin()` JQL are implemented; the full
  JSM approval and SLA JQL function families are implemented; durable bulk
  field discovery and durable watch/unwatch submission, execution and progress
  are implemented
- Blockers: none

## Contract baseline

The generated inventory contains 1,207 pinned operations. Jira Cloud Platform
REST v3 owns 617 of them. The coverage ledger measures reviewed compatibility
evidence rather than route count; a registered path is not sufficient evidence.

PR 1 closes the Jira Platform and administration surface described in
[PLAN.md](../PLAN.md), including exact API behavior, the browser journeys that
exercise it, authorization, audit, durable background work, and ledger updates.

## Current checkpoint

The current bulk checkpoints implement Jira's shared editable-field discovery,
watch, unwatch and progress operations. Field discovery intersects the canonical
project metadata with search and bidirectional 50-field cursor pages. Submission
enforces visibility, a 1,000-item bound and five-active task cap; the durable
worker rechecks visibility and atomically commits watcher state, synchronization
actions and the terminal task result.
The browser directory completes the owner and site-administrator management
journey and is connected to REST-created filters by Playwright. Owners can add
or remove daily and weekly email schedules with active-member recipients. A
durable, retryable runner evaluates the saved JQL as the owner, caps rendered
results, records outcomes, and deduplicates each recipient in the shared mail
outbox.

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
Connect and native descriptors now persist typed app-function declarations.
Search compilation records each invocation, calls the signed app endpoint on a
cache miss, reuses seven-day precomputations, exposes functions in autocomplete,
and parses returned fragments through bounded recursive JQL expansion before
the normal visibility-scoped search. REST, navigator, board/quick-filter,
dashboard, service queue/SLA, automation, and webhook JQL paths share that
runtime hook.

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

This built-in slice adds current Jira Cloud work-item aliases, multi-link
selectors, release-state and boundary-version functions, watched/voted issue
selectors, date-bounded `updatedBy()`, and project lead/role selectors. Natural
date increments use the function's own day/week/month/year period. Votes are a
durable issue relation with idempotent self-service REST and browser actions,
voter reads, action-log visibility, and JQL evaluation.

The component slice adds stable project-scoped ownership and assignment state,
all eight pinned REST resources, full and paged collections, canonical
multi-value issue fields, counts, rename propagation, move-on-delete, action
and organization audit, project-settings management, create metadata, and
`componentsLeadByUser()`.

The login-function slice atomically keeps each account's current and previous
successful sign-in boundaries across password, generic OIDC, Google, Microsoft,
and Atlassian sessions. Session deletion and provider revocation do not erase
the boundaries. Date clauses and history predicates resolve `currentLogin()`
and `lastLogin()` through the same compiler used by every JQL surface, including
typed custom date-time fields and their app aliases.

The JSM approval-function slice compiles all eight Jira Cloud approval
functions against the same durable request approval and per-user decision state
used by the portal and REST API. It distinguishes completed, pending, answered,
and unanswered steps; accepts current and explicit user identities; implements
the supported non-equality forms without matching empty approval fields; and
advertises the approval field and functions through autocomplete.

The JSM SLA-function slice compiles all seven Jira Cloud SLA functions against
named workspace SLA fields. PostgreSQL business-time functions use each
metric's calendar, time zone, working window, and holidays. Durable pause
intervals distinguish a condition-paused clock from one merely outside calendar
hours; manager-authored pause JQL is validated, reconciled immediately, and
re-evaluated on request creation and transitions. REST cycle values, escalation
workers, and JQL share the same goals, cycle history, and pause state.

## Validation baseline

Merged PR #68 passed the complete GitHub CI matrix, including all 58 Playwright
journeys, Go tests, WebAssembly and native builds, `go vet`, CodeQL, gosec,
Semgrep, govulncheck, npm audit, container build, conformance tests, and generated
ledger freshness checks.

Each PR 1 checkpoint must run its focused tests before commit. Database changes
also run the PostgreSQL integration suite from an empty migrated database.

The filter-subscription checkpoint passes the focused store and REST contract
tests, the Chromium saved-filter owner journey, the full uncached PostgreSQL Go
suite, `go vet`, native and WebAssembly builds, and conformance freshness checks.

The project-component checkpoint passes its PostgreSQL REST/JQL lifecycle
contract, the responsive Chromium project-manager journey, the full uncached
PostgreSQL Go suite, `go vet`, native and WebAssembly builds, and conformance
freshness checks.

The login-boundary checkpoint passes its password/OIDC/provider rotation and
enhanced-search contract, JQL compiler tests, both Chromium identity-provider
journeys, the full uncached PostgreSQL Go suite, `go vet`, native and
WebAssembly builds, and conformance freshness checks.

The JSM approval-function checkpoint passes the customer/agent PostgreSQL
request contract across pending, answered, approved, declined, explicit-user,
current-user, and non-equality queries; the Chromium service journey; the full
uncached PostgreSQL Go suite; `go vet`; native and WebAssembly builds; and
conformance freshness checks.

The JSM SLA-function checkpoint passes an empty-schema migration run and the
PostgreSQL request contract for autocomplete, manager pause-rule authorization
and validation, immediate pause/resume reconciliation, calendar-aware
remaining time, current and historical breach state, completion, running state,
and non-equality behavior. The responsive and accessible Chromium service
manager journey, full uncached PostgreSQL Go suite, `go vet`, native and
WebAssembly builds, and conformance freshness checks also pass.

The bulk watch/unwatch checkpoint passes its PostgreSQL REST and durable-worker
lifecycle, including execution-time access revocation, idempotent watcher state,
atomic synchronization actions, Jira-shaped progress and the five-active-task
cap. The full uncached Go suite from an empty migrated database, focused package
tests, `go vet`, native and WebAssembly builds, and conformance inventory and
freshness checks pass.

The bulk editable-field checkpoint passes administrator and denied-member
PostgreSQL contracts, cross-project canonical metadata intersection, Jira-shaped
options, field search, invalid selection/cursor handling and forward/backward
50-field cursor traversal.

## Resume here

1. Continue bulk work items with edit/delete/move/transition and discovery.
2. Add the remaining permission and customer/organization JQL functions with
   their owning policy state.
3. Update compatibility evidence and commit each independently buildable
   checkpoint.

## Evidence map

- [Roadmap and gates](../PLAN.md)
- [Cloud/API status](CLOUD_PARITY.md)
- [Endpoint groups](../api/conformance/MATRIX.md)
- [Operation coverage](../api/conformance/cloud-coverage.json)
- [UI and persona journeys](UI_PARITY.md)
- [Administration behavior](ADMIN.md)
- [Saved filters and sharing](FILTERS.md)
- [Project components](COMPONENTS.md)
- [JQL and issue search](JQL.md)
- [Bulk work-item operations](BULK_ISSUES.md)

## Continuity rules

- Do not infer API completeness from registered routes or the grouped matrix.
- Do not mark a journey complete without a browser test spanning its outcome.
- Do not claim base-URL-only compatibility for clients that hardcode a central
  Atlassian host.
- Preserve unrelated changes and inspect the working tree before editing.
- Commit every checkpoint with its tests and documentation.
