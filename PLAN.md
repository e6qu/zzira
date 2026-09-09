# ZZIRA — Jira Cloud compatibility delivery plan

Updated: 2026-09-09

ZZIRA is a self-hosted Jira Cloud, Jira Software, Jira Service Management,
Confluence, administration, automation, analytics, and app platform. Delivery is
split into large, dependency-ordered pull requests. Each PR must finish coherent
user journeys across persistence, commands, public API, browser UI,
authorization, audit, background work, tests, and documentation.

## Compatibility target

The pinned public-contract inventory is the measurable API denominator:

| Contract | Operations |
|---|---:|
| Jira Cloud Platform REST v3 | 617 |
| Jira Software Cloud REST | 105 |
| Jira Service Management Cloud REST | 75 |
| Confluence Cloud REST v1 | 130 |
| Confluence Cloud REST v2 | 218 |
| Automation REST | 15 |
| Organizations REST | 47 |
| **Total** | **1,207** |

Connect descriptors, app modules, Assets, and other public surfaces without one
authoritative OpenAPI document require separately versioned manual inventories.

Site-scoped Jira, Jira Software, Jira Service Management, and Confluence clients
must work by changing their base URL to ZZIRA. Compatibility includes paths,
methods, authentication, bodies, status codes, headers, pagination, expansions,
identifiers, permissions, errors, transitions, idempotency, and concurrency.
Clients that hardcode `auth.atlassian.com` or `api.atlassian.com` must support an
endpoint override. Atlassian billing, proprietary AI models, and
Atlassian-hosted Forge compute remain explicit external-service boundaries.

These files are the evidence sources:

- [docs/CLOUD_PARITY.md](docs/CLOUD_PARITY.md) records product status and known
  compatibility limits.
- [api/conformance/cloud-operations.json](api/conformance/cloud-operations.json)
  is the generated operation inventory.
- [api/conformance/cloud-coverage.json](api/conformance/cloud-coverage.json)
  records reviewed operation-level evidence.
- [api/conformance/MATRIX.md](api/conformance/MATRIX.md) groups implemented API
  slices without implying complete operation fidelity.
- [docs/UI_PARITY.md](docs/UI_PARITY.md) records persona journeys and browser
  evidence.
- [docs/CONTINUITY.md](docs/CONTINUITY.md) is the short-lived active-branch
  handoff.

## Completion rules

A feature is complete only when all applicable rules below are satisfied:

- Public API behavior matches the pinned contract and has reviewed evidence.
- Browser journeys cover the intended personas and reach a persisted outcome.
- REST, UI, automation, apps, search, reports, exports, and replicas use the
  same command and permission model.
- State and its immutable action/audit record commit in one transaction.
- Background work is durable, leased, bounded, retryable, observable,
  idempotent, and safe across worker restarts.
- Search, indexing, sync, reports, exports, and notifications filter access
  before serialization or delivery.
- Invalid, unsupported, and unavailable behavior returns an explicit error.
- Migrations work from a clean database and every supported prior schema.
- Focused tests, the full Go suite, `go vet`, native and WebAssembly builds,
  browser journeys, security checks, and conformance freshness pass as relevant.
- Documentation and the conformance/UI ledgers change in the same checkpoint.
- Each checkpoint is committed and independently buildable.

The program is complete when every in-boundary operation and manual contract is
reviewed with no missing or unassessed entries, every promised persona journey
has browser evidence, named third-party clients and apps pass compatibility
fixtures, and the release-certification gate in PR 8 passes.

## PR 0 — Cloud compatibility baseline

PR 0 merges the implementation accumulated so far and freezes the baseline for
all subsequent completion PRs. The repository already contains shared identity,
organization, administration, projects, work items, workflows, boards, sprints,
releases, dashboards, DORA reporting, automation, Service Management,
Confluence, local-first sync, and Connect-compatible app foundations.

The PR 0 branch adds the following reviewed checkpoints to that foundation:

- saved-filter administration, shares, subscriptions, scheduled delivery, and
  browser management;
- expanded JQL grammar, built-ins, history predicates, helper APIs, app-provided
  functions, durable precomputations, and autocomplete;
- stable Jira issue IDs, field projections, expansions, changelogs,
  transitions, properties, strong-consistency reconciliation, and durable
  enhanced-search paging;
- Jira votes, watches, project components, default assignment, login-date JQL,
  JSM approval JQL, and calendar-aware JSM SLA JQL;
- durable Jira bulk watch/unwatch operations, editable-field discovery, field
  edits, progress, access rechecks, bounded concurrency, and idempotent replay;
  and
- the associated PostgreSQL migrations, permissions, audit/actions, UI,
  contract tests, conformance evidence, and documentation.

PR 0 establishes an honest measured baseline. It does not claim full Jira Cloud
compatibility; every remaining gap is owned by one of the PRs below.

## Remaining delivery sequence

| PR | Deliverable | Depends on |
|---:|---|---|
| 1 | Jira Platform and project/site administration | PR 0 |
| 2 | Jira Software, Plans, releases, dashboards, and analytics | PR 1 |
| 3 | Automation and workflow ecosystem | PR 1; integrates PR 2 |
| 4 | Jira Service Management and Assets | PRs 1–3 |
| 5 | Confluence, wiki, diagrams, and live collaboration | PRs 1 and 3 |
| 6 | App/plugin platform compatibility | PRs 1–5 |
| 7 | Organization administration, identity, and data governance | PRs 1–6 |
| 8 | Local-first closure and compatibility certification | PRs 1–7 |

### PR 1 — Jira Platform and project/site administration

Complete Jira's shared work-management and configuration layer:

- finish bulk delete, move, transition, discovery, notification, and remaining
  editable field families with durable progress and per-item results;
- finish JQL grammar, operators, functions, history, permissions, saved filters,
  subscriptions, search variants, paging, expansions, and sanitization;
- finish ADF validation, storage, rendering, conversion, mentions, and media at
  every Jira field boundary;
- finish hierarchy, work types, fields, contexts, screens, schemes, components,
  versions, roles, templates, team-managed/company-managed configuration,
  archive/restore, import/export, and project lifecycle; project categories,
  JSON properties, software feature state, sender email, installed project
  types, key/name validation, and their API/admin journeys are delivered;
- finish permission, notification, issue-security, workflow, and field-scheme
  administration with impact previews and audited migrations;
- finish user preferences, notifications, votes, watches, properties,
  attachments, email, retention, and remaining platform REST families; site
  configuration now covers announcements, feature controls, time tracking,
  navigator defaults, application properties, audit, and API/UI coherence; and
- prove contributor, project-admin, site-admin, and support-admin journeys.

**Exit gate:** all 617 Jira Platform operations are assessed; every in-scope
operation is exact or has a documented external boundary; Jira configuration is
available through API and UI; and the Jira client compatibility fixtures pass.

### PR 2 — Jira Software, Plans, releases, dashboards, and analytics

Complete planning, delivery governance, and management insight:

- finish Scrum and Kanban boards, ownership, card configuration, ranking,
  backlogs, epics, estimation, capacity, parallel sprints, and dependencies;
- add cross-project Plans with hierarchy, teams, scenarios, baselines,
  forecasts, warnings, sharing, and export;
- finish releases, readiness, approvals, change evidence, related work,
  cross-project releases, notes, ordering, governance, and export;
- implement velocity, burnup, burndown, cumulative-flow, control, cycle-time,
  throughput, created/resolved, epic, version, forecast, and release reports
  from immutable historical facts;
- finish custom dashboards, gadgets, ownership, sharing, archive, bulk actions,
  refresh, subscriptions, accessible data tables, and export; and
- finish configurable DORA definitions, targets, comparisons, segmentation,
  evidence drill-down, scheduled delivery, and export.

**Exit gate:** all 105 Jira Software operations are assessed and exact within
the compatibility boundary; every aggregate drills into immutable source facts;
and product-manager, project-manager, agile-coach, release-manager, and
engineering-manager journeys are browser-proven.

### PR 3 — Automation and workflow ecosystem

Complete cross-product rule authoring and durable execution:

- implement event, webhook, manual, cron, SLA, deployment, release, and
  scheduled triggers;
- finish conditions, nested branches, related-object traversal, smart values,
  templates, secrets, connections, quotas, and the supported action catalog;
- enforce actor/run-as permissions, loop protection, idempotency, rate limits,
  retries, cancellation, recovery, and dead-letter administration;
- add rule import/export, versioning, validation, test execution, trace detail,
  audit, and actionable failure diagnostics;
- finish workflow conditions, validators, post-functions, advanced parameters,
  and team-managed routing; and
- provide durable mail, scheduled-report, retention, webhook, and product-event
  dispatch primitives used by later PRs.

**Exit gate:** all 15 Automation operations and the manual workflow-component
inventory are assessed; deterministic replay and duplicate/restart behavior are
proven; and administrator and rule-author journeys reach verified outcomes.

### PR 4 — Jira Service Management and Assets

Complete the bundled Service Desk product:

- finish help centers, branding, portals, request types, conditional forms,
  validation, localization, knowledge deflection, email intake, and customer
  notification preferences;
- finish customer accounts, organizations, participants, sharing, approvals,
  subscriptions, reopen rules, files, comments, feedback, statuses, and SLAs;
- finish queues, routing, assignment, escalation, collaboration, bulk actions,
  calendars, reusable SLA policies, pause rules, and retroactive calculation;
- finish incidents, problems, changes, risks, CABs, conflicts, on-call,
  escalation, stakeholder communication, post-incident reviews, dependencies,
  and service ownership;
- complete Assets schemas, types, attributes, objects, relations, AQL, imports,
  mapping, reconciliation, attachments, icons, permissions, history, APIs, and
  management UI; and
- complete service, SLA, satisfaction, incident, change, asset, and team reports
  with comparisons, drill-down, scheduling, and export.

**Exit gate:** all 75 JSM operations and the manual Assets inventory are
assessed and exact within the boundary; customer-to-resolution,
incident/change, asset-manager, agent, and service-admin journeys are
browser-proven.

### PR 5 — Confluence, wiki, diagrams, and live collaboration

Complete the bundled knowledge and collaboration product:

- finish every v1/v2 content operation and type, hierarchy, ordering, move/copy,
  archive/restore, retention, and lifecycle behavior;
- finish ADF and storage-format editing/conversion, macros, media, embeds,
  mentions, tasks, templates, blueprints, anchors, and CQL;
- implement live documents, presence, concurrent operations, conflict recovery,
  ordered history, drafts, publishing, comments, watches, and notifications;
- finish whiteboards, connectors, grouping, alignment, keyboard editing,
  diagrams, graph layouts, Smart Links, and accessible text equivalents;
- finish databases, schemas, relations, supported formulas, records, views,
  filtering, sorting, grouping, permissions, imports, and exports;
- finish space roles, restrictions, classification, analytics, audit,
  administration, backup/restore, and PDF/SVG/PNG/knowledge exports; and
- prove author, reviewer, knowledge-manager, space-admin, and site-admin
  journeys, including embedded Jira and JSM objects.

**Exit gate:** all 348 Confluence operations are assessed and exact within the
boundary; live collaboration converges under multi-user tests; and creation,
review, governance, search, diagramming, and export journeys are browser-proven.

### PR 6 — App/plugin platform compatibility

Complete locally executable app behavior across every bundled product:

- create a versioned manual inventory for descriptors, modules, conditions,
  callbacks, context, permissions, lifecycle, and migration contracts;
- finish global, site, project, space, page, issue, dashboard, report, workflow,
  field, search, navigation, administration, and dynamic modules;
- finish signed remote and local rendering, configuration pages, persistence,
  presence conditions, web-item locations, and module validation;
- finish custom fields, options, contexts, properties, indexing, JQL hooks,
  webhooks, schedules, lifecycle retry, upgrades, rollback, secret rotation,
  uninstall cleanup, delivery inspection, isolation, and resource limits;
- expose explicit capability boundaries for Atlassian-hosted Forge services;
  and
- add developer/admin consoles and representative third-party app fixtures.

**Exit gate:** every supported manual app contract is assessed and versioned;
install, upgrade, suspend, uninstall, reinstall, and tenant-isolation suites
pass; and named Jira/JSM/Confluence apps work after endpoint configuration.

### PR 7 — Organization administration, identity, and data governance

Complete enterprise administration across the product suite:

- finish organizations, sites, products, directories, accounts, groups,
  invitations, domains, claims, roles, policies, sessions, tokens, and audit;
- finish Login with Atlassian, Google, Microsoft, generic OIDC, account linking,
  provider rotation/revocation, recovery, and administrator diagnostics;
- finish federation, lifecycle provisioning, managed-account controls,
  authentication policies, retention, legal hold, classification, residency,
  organization import/export, backup, restore, and deletion;
- enforce policy consistently across API, UI, search, sync, automation, apps,
  exports, notifications, and background workers; and
- prove organization-admin, identity-admin, security-admin, auditor, and data
  steward journeys.

**Exit gate:** all 47 Organizations operations and the manual identity/provider
contracts are assessed and exact within the boundary; policy and revocation
propagate safely across products; and administration/recovery journeys pass.

### PR 8 — Local-first closure and compatibility certification

Close cross-product gaps and produce the release evidence:

- extend permission-shaped snapshots, deltas, and safe queued mutations to
  Jira, Software, Service, Knowledge, dashboards, releases, and administration;
- prove conflict handling, outbox ordering, cross-tab ownership, reconnect,
  compaction, quota handling, account/site switching, schema upgrades, full
  resnapshot, and immediate access-revocation purge;
- eliminate every remaining missing or unassessed in-boundary contract entry;
- run golden request/response, header, paging, expansion, identifier,
  permission, error, transition, idempotency, and concurrency suites;
- run Go, TypeScript, and Python client fixtures with only the site base URL
  changed, plus named third-party client and app fixtures;
- prove fresh install, migrations, backup/restore, worker failure, tenant
  isolation, security, accessibility, supported browsers, responsive layouts,
  localization, time zones, scale, and performance budgets; and
- publish the release evidence, supported-version matrix, known external
  boundaries, upgrade/rollback runbook, and operator documentation.

**Exit gate:** the pinned inventories contain no missing or unassessed
in-boundary operations, all required persona and compatibility suites pass, CI
is green, and release evidence supports the claimed compatibility version.

## Execution protocol

Work starts with PR 1 after PR 0 merges. Within each PR, implement and commit
independently buildable vertical checkpoints in dependency order: schema and
commands, public API, browser journey, workers/integrations, evidence, and docs.
Update `docs/CONTINUITY.md` after each checkpoint. Do not advance to the next PR
while the current PR has failing CI, unresolved review threads, unrecorded
contract gaps, or uncommitted work.

The first PR 1 checkpoint is durable Jira bulk deletion with attachment cleanup,
permission rechecks, bounded execution, per-item results, action/audit records,
and REST/UI lifecycle evidence. Bulk move and transition follow on the same
shared task model.
