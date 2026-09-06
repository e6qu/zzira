# Development continuity

Updated: 2026-09-06

This file is the local handoff for the Jira Cloud completion program. Keep it
short, factual, and current after each meaningful commit. Stable architecture
and final acceptance rules belong in [PLAN.md](../PLAN.md).

## Active delivery

- Branch: `feat/cloud-surface-completion`
- Base: `origin/main` after PR #65, scheduled automation
- Delivery shape: one pull request with ordered, independently green commits
- Last completed workstream: 1 of 15 — contract and continuity
- Active workstream: 2 of 15 — organizations and authorization
- Blockers: none

## Last verified baseline

The merged product includes core work items, projects, people, search, issue
triage, boards, backlog and sprint lifecycle, notifications, elementary workflow
administration, releases, configurable dashboards, initial wiki spaces/pages,
and durable fixed-interval automation. Password, API-token, browser-session and
one generic OIDC configuration are present.

The pinned contract inventory contains 1,207 operations:

| Contract | Operations |
|---|---:|
| Jira Cloud Platform REST v3 | 617 |
| Jira Software Cloud REST | 105 |
| Jira Service Management Cloud REST | 75 |
| Confluence Cloud REST v1 | 130 |
| Confluence Cloud REST v2 | 218 |
| Automation REST | 15 |
| Organizations REST | 47 |

This is an inventory denominator, not an implementation count. App descriptors
and module contracts need a separately versioned manual ledger because Atlassian
does not publish them as one OpenAPI document.

## Completed in this branch

- Replaced the obsolete V0–V6 future roadmap with the active one-PR execution
  map and removed landed follow-up sections.
- Added this handoff and assigned clear ownership to planning, parity, matrix,
  and surface documents.
- Pinned Jira Service Management, Confluence v1, Automation, and Organizations
  OpenAPI contracts, increasing the reproducible denominator from 940 to 1,207.
- Added contract family, host class, source metadata, stable operation IDs,
  strict pin validation, and focused generator tests.
- Added exact-operation coverage generation. The first reviewed assessment
  records Automation as 8 partial and 7 missing operations. The organization
  slices add 38 partial assessments; 1,154 operations remain explicitly
  unassessed rather than being inferred from route names.
- Added organization, site, product, internal-directory, directory-user, group,
  group-member, role-binding, and organization-audit persistence with automatic
  provisioning for new workspaces and a repair path for older workspaces.
- Migrated legacy workspace roles into centrally evaluated site and product
  bindings, including group-derived administration and immediate revocation.
- Added bearer-authenticated organization discovery, directory/group listing,
  group creation, and group-membership mutation endpoints with strict request,
  pagination, conflict, and authorization behavior.
- Added the `/admin` journey for organization context, product visibility,
  group creation, member assignment/removal, and the resulting audit trail.
- Fixed bootstrap administration so an existing member is promoted when the
  configured bootstrap identity already exists.
- Made bootstrap and first-time OIDC assignment prefer `ws_default`
  deterministically when additional workspaces exist.
- Added product-workspace discovery with Atlassian resource identifiers plus
  direct and group role assignment/revocation APIs. Effective user access
  reports direct and group-derived methods.
- Extended `/admin` so site administrators grant and revoke each directory
  group's Jira Software, Jira Service Management, and Confluence access.
- Added managed-account list/detail/invite/suspend/restore/remove APIs and UI.
  Suspension and removal revoke sessions and API tokens after the final active
  directory, mutations are audited, and administrators cannot suspend or remove
  themselves.
- Added group detail, filtered count, search, statistics, and audited deletion,
  plus directory-user search and statistics. Search supports documented
  identity, directory, membership, lifecycle, resource, role, domain, sort,
  expansion, all-directory, and opaque-pagination behavior.
- Completed invitation-time product roles and group membership with per-account
  result details and 206 partial responses. Optional email delivery now uses a
  configured SMTP sender and a durable, leased, bounded-retry outbox; absent or
  partial SMTP configuration fails explicitly.
- Added stored managed-account profiles and an audited administrator editing
  journey. Directory memberships now carry independent active/suspended state;
  access is removed only in that directory, and sessions and API tokens are
  revoked after the account loses its final active directory.
- Completed the remaining directory-user contract operation with durable
  two-second visible product activity and exact per-product last-active dates,
  mapped product keys, organization-add timestamps, and cursor validation.
- Exposed immutable organization audit evidence through event query, polling,
  detail, and action-catalog APIs. Added text/action/actor/network/product/time
  filters, ascending polling cursors, and shared browser audit search.
- Added durable organization domain claims with strict DNS normalization,
  generated TXT challenges, fail-closed verification, removal, audit evidence,
  administrator UI, and exact domain list/detail API resources.

Validation after the organization foundation:

- 7 focused Python conformance tests pass.
- Inventory and coverage generated files pass their `--check` modes.
- All Go packages pass with the PostgreSQL integration suite enabled.
- `go vet ./...` passes.
- `GOOS=js GOARCH=wasm go build ./...` passes.
- All 45 Chromium user-journey tests pass, including WCAG scans in light and
  dark themes and 320 px reflow for the new administration page.
- `git diff --check` passes.

Validation after product access and role assignments:

- All Go packages pass with the PostgreSQL integration suite enabled.
- `go vet ./...`, the WebAssembly build, and all conformance checks pass.
- The focused administration browser journey passes with account lifecycle,
  product grant, effective membership, audit, revocation, accessibility, and
  reflow coverage.

Validation after group and directory search completion:

- All Go packages pass with the PostgreSQL integration suite enabled.
- `go vet ./...`, the WebAssembly build, and all conformance checks pass.
- The focused Chromium administration journey passes group deletion, audit,
  WCAG scans in light and dark themes, and 320 px reflow.
- Exact coverage at this checkpoint was 46 of 1,207 operations assessed: 39
  partial, 7 missing, and 1,161 explicitly unassessed.

Validation after invitation access and delivery:

- All Go packages pass with migration 034 and the PostgreSQL integration suite
  enabled; SMTP protocol delivery and durable outbox state are covered.
- `go vet ./...`, the WebAssembly build, and all conformance checks pass.
- The focused Chromium journey passes invitation-time product/group access,
  account cleanup, WCAG scans, dark mode, and 320 px reflow.

Validation after managed profiles and directory-scoped suspension:

- Focused store, organization API, web, authorization, and server package tests
  pass with migration 035 and PostgreSQL enabled.
- The focused Chromium administration journey passes profile editing, account
  lifecycle, WCAG scans in light and dark themes, and 320 px reflow.
- `git diff --check` passes.

Validation after directory-user activity completion:

- The focused organization API integration test passes with migration 036,
  empty and populated product activity, mapped Jira Service Management keys,
  timestamps, missing users, and malformed cursor behavior.
- The focused Chromium administration journey proves that a visible product
  page records activity only after its two-second threshold.
- Exact coverage is 47 of 1,207 operations assessed: 40 partial, 7 missing, and
  1,160 explicitly unassessed.

Validation after organization audit completion:

- The organization API integration journey passes query validation and filters,
  500-item limits, ascending polling and next cursors, event detail, missing
  events, and the action catalog.
- The focused Chromium administration journey passes audit text/action filters,
  clear-filter navigation, WCAG scans, dark mode, and 320 px reflow.
- Exact coverage is 51 of 1,207 operations assessed: 44 partial, 7 missing, and
  1,156 explicitly unassessed.

Validation after organization domain claims:

- The organization API integration journey passes empty/populated domain lists,
  normalization, invalid input, opaque cursor rejection, detail/not-found, and
  verified claim state with migration 037.
- The focused Chromium administration journey passes domain add/remove, TXT
  challenge visibility, WCAG scans, dark mode, and 320 px reflow.
- Exact coverage is 53 of 1,207 operations assessed: 46 partial, 7 missing, and
  1,154 explicitly unassessed.

## Current change

1. Add authentication policies and identity-provider configuration.
2. Extend revocation coverage from browser/API authorization to local replicas
   and queued mutations where the affected resource can already be cached.

## Resume here

1. Run `git status --short --branch` and read this file.
2. Run `python3 api/conformance/inventory.py --check` before changing contract
   inputs.
3. Continue the first unfinished item in **Current change**.
4. Run the focused inventory tests, `go test ./...`, the WASM build, and
   `git diff --check` before the workstream commit.
5. Update this file with the completed workstream, exact validation, next
   workstream, and any real blocker.

## Evidence map

- [Cloud/API status](CLOUD_PARITY.md)
- [Endpoint groups](../api/conformance/MATRIX.md)
- [Operation coverage](../api/conformance/cloud-coverage.json)
- [UI and persona journeys](UI_PARITY.md)
- [Automation behavior](AUTOMATION.md)
- [Dashboard behavior](DASHBOARDS.md)
- [Release behavior](RELEASES.md)
- [SSO behavior](shauth-sso.md)

## Continuity rules

- Do not infer API completeness from the grouped endpoint matrix.
- Do not mark a journey complete without a browser test spanning its user goal.
- Do not claim base-URL-only compatibility for clients that hardcode a central
  Atlassian identity or administration host.
- Do not carry placeholders, silent fallbacks, dead code, or swallowed errors.
- Preserve unrelated user changes and inspect the working tree before editing.
- Answer review comments in their original threads, resolve addressed threads,
  and require the full CI matrix to pass before merge.
