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
  records Automation as 8 partial and 7 missing operations; 1,192 operations
  remain explicitly unassessed rather than being inferred from route names.

Validation for this workstream:

- 7 focused Python conformance tests pass.
- Inventory and coverage generated files pass their `--check` modes.
- `go test ./...` passes.
- `GOOS=js GOARCH=wasm go build ./...` passes.
- `git diff --check` passes.

## Current change

1. Model organizations, sites, products, directories, groups, role bindings,
   permission schemes, and audit events with additive migrations.
2. Migrate existing workspace memberships into the new role-binding model.
3. Centralize authorization evaluation for existing and new edges.
4. Deliver the first organization/site administration APIs and complete admin
   browser journey.
5. Add permission-revocation action/tombstone coverage and multi-user tests.

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
