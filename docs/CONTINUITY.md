# Development continuity

Updated: 2026-09-08

This file is the short-lived handoff for the active branch. Stable scope, PR
boundaries, dependencies, and acceptance gates belong in
[PLAN.md](../PLAN.md). Product and contract status belong in
[CLOUD_PARITY.md](CLOUD_PARITY.md).

## Active delivery

- Branch: `feat/cloud-surface-completion`
- Base: `origin/main` after PR #65
- Delivery unit: PR 0 — Integrated Cloud foundation
- Pull request: #68
- State: open; PR security hardening is locally clean and awaiting CI confirmation
- Last product checkpoint: Connect site administration pages
- Blockers: none

PR 0 preserves the existing dependency-ordered commits. After it merges, the
remaining program proceeds through PRs 1–8 in [PLAN.md](../PLAN.md).

## Verified contract baseline

The generated inventory contains 1,207 pinned operations:

| Contract | Operations |
|---|---:|
| Jira Cloud Platform REST v3 | 617 |
| Jira Software Cloud REST | 105 |
| Jira Service Management Cloud REST | 75 |
| Confluence Cloud REST v1 | 130 |
| Confluence Cloud REST v2 | 218 |
| Automation REST | 15 |
| Organizations REST | 47 |

The strict coverage ledger currently assesses 437 operations: 430 partial and
7 missing. The other 770 remain explicitly unassessed. These figures measure
reviewed compatibility evidence, not route count.

Connect descriptors and module contracts need the separate manual inventory
planned for PR 6 because Atlassian does not publish them as one OpenAPI
document.

## PR 0 contents

PR 0 establishes:

- contract pinning, operation coverage, parity ledgers, and CI freshness checks;
- organizations, directories, people, groups, product access, policies, domains,
  audit, provider administration, and Google/Microsoft/Atlassian sign-in;
- Jira status, workflow, scheme, draft, publishing, transition-rule, migration,
  durable task, and visual workflow-designer foundations;
- development facts, releases, dashboards, and initial DORA reporting;
- bundled JSM help-center, request, agent, queue, SLA, operations, reporting,
  service-topology, and initial Assets journeys;
- bundled Confluence space, page, blog, attachment, discussion, task, watch,
  database, whiteboard, classification, and governance journeys; and
- Connect-compatible installation, identities, scopes, JWT/QSH, storage,
  lifecycle, schedules, webhooks, and the delivered Jira/Confluence modules.

The precise shipped behavior and remaining gaps are in the parity and surface
documents. PR 0 does not claim complete Jira Cloud fidelity.

## Validation baseline

The PR 0 CI harness now provisions package-scoped bootstrap administrators for
integration tests that require administrative state. The complete suite passes
from an empty PostgreSQL database without running the demo seed command.

The PR boundary hardening removes the security-scan backlog found on the first
GitHub run: app and attachment redirects are origin/path constrained, Atlassian
OAuth requests use pinned Cloud endpoints and checked redirect chains, stored
JSON is safely re-encoded, multipart bodies stay explicitly capped, and numeric
inputs and report allocations no longer narrow or allocate from unchecked input.

The last implementation checkpoint passed:

- the complete PostgreSQL Go suite;
- native server and load-test builds;
- the WebAssembly build;
- `go vet`;
- gosec 2.29 with zero findings;
- seven conformance tests;
- generated inventory and coverage freshness checks; and
- diff validation.

Before merge, rerun the repository-required CI matrix and address failures
against the PR head.

## Resume here

1. Inspect PR 0 checks and review conversations.
2. Fix failures in focused commits and rerun the affected checks.
3. Answer review comments in their original threads and resolve addressed
   threads.
4. Keep this handoff current after each meaningful checkpoint.
5. After PR 0 merges, branch PR 1 — Jira Platform and administration completion
   from the updated `origin/main`.

## Evidence map

- [Roadmap and gates](../PLAN.md)
- [Cloud/API status](CLOUD_PARITY.md)
- [Endpoint groups](../api/conformance/MATRIX.md)
- [Operation coverage](../api/conformance/cloud-coverage.json)
- [UI and persona journeys](UI_PARITY.md)
- [Automation behavior](AUTOMATION.md)
- [Dashboard behavior](DASHBOARDS.md)
- [Release behavior](RELEASES.md)
- [Reports and DORA behavior](REPORTS.md)
- [Service Management behavior](SERVICE_MANAGEMENT.md)
- [App runtime behavior](APPS.md)
- [SSO behavior](shauth-sso.md)

## Continuity rules

- Do not infer API completeness from registered routes or the grouped matrix.
- Do not mark a journey complete without a browser test spanning its outcome.
- Do not claim base-URL-only compatibility for clients that hardcode a central
  Atlassian host.
- Preserve unrelated changes and inspect the working tree before editing.
- Commit every checkpoint with its tests and documentation.
