# Jira Cloud compatibility ledger

Updated: 2026-09-08

ZZIRA is not yet a full Jira Cloud or Confluence Cloud replacement. This ledger
states the current evidence and remaining product surfaces without treating a
registered route as proof of compatible behavior. The execution order is in
[PLAN.md](../PLAN.md), and the active handoff is in
[CONTINUITY.md](CONTINUITY.md).

## Contract inventory

Published OpenAPI documents are vendored in [api/specs](../api/specs). Retrieval
URLs, versions, route prefixes, and SHA-256 checksums are recorded in
[pins.json](../api/specs/pins.json). The generated
[operation inventory](../api/conformance/cloud-operations.json) currently has:

| Published contract | Operations |
|---|---:|
| Jira Cloud Platform REST v3 | 617 |
| Jira Software Cloud REST, including Agile and development integrations | 105 |
| Confluence Cloud REST v2 | 218 |
| Jira Service Management Cloud REST | 75 |
| Confluence Cloud REST v1 | 130 |
| Automation REST | 15 |
| Organizations REST | 47 |
| **Current pinned total** | **1,207** |

These are contract operations, not delivered operations. App descriptors and
module contracts are not published as one OpenAPI document, so they will use a
separately versioned manual contract ledger. Regenerate the OpenAPI inventory
with `python3 api/conformance/inventory.py`; CI runs the same program with
`--check`.

The generated [operation coverage](../api/conformance/cloud-coverage.json)
applies reviewed exact-operation assessments to that denominator. It currently
assesses 384 operations: 377 partial and 7 missing. The remaining 823 operations
are explicitly unassessed at this stricter level. The grouped
[API matrix](../api/conformance/MATRIX.md) records older tested slices; it is not
divided by 1,207 because one row may represent several operations and does not
certify every option or wire type.

## Compatibility promise

For site-scoped Jira Platform, Jira Software, Jira Service Management, and
Confluence APIs, a compatible client must be able to replace its base URL and
retain its ordinary request behavior. Compatibility includes paths, methods,
authentication, request and response bodies, status codes, headers, pagination,
expansion, identifiers, permission failures, transitions, and concurrency rules.

Automation is available through the documented site gateway under
`/gateway/api/automation/public/...`. ZZIRA also exposes local equivalents for
documented central-host operations. Software that hardcodes
`api.atlassian.com` or `auth.atlassian.com` needs a separate endpoint override;
changing a site URL cannot redirect those hosts.

Atlassian billing, proprietary AI models, and Atlassian-hosted Forge compute are
external services. Locally executable app modules belong to the ZZIRA app
runtime. Capability discovery and errors must distinguish these cases precisely.

## Current product status

“Partial” means a tested useful subset exists and the stated work remains.

| Surface | State | Delivered evidence | Remaining completion work |
|---|---:|---|---|
| Identity and authentication | Partial | Password, sessions, API tokens, simultaneous generic OIDC, Google and tenant-scoped Microsoft OIDC, Atlassian OAuth 2.0 3LO, AES-GCM encrypted custom OIDC registration/rotation/deletion, durable provider enable/disable, provider-bound connect state, reviewed identity linking/unlink, issuer-scoped revocation, login audit and logout | Organization/site/product lifecycle and enterprise federation policy |
| Projects and administration | Partial | Project create/edit, Scrum/Kanban setup, project directory and settings entry points; all 47 organization operations reviewed with tested group/user/role/activity/event/domain/policy subsets and admin journeys | Permission, notification, security, workflow, screen and field schemes; runtime policy enforcement and cross-organization domain ownership; templates; project lifecycle; remaining Jira admin APIs and journeys |
| Work items | Partial | Issue CRUD, one-level sub-task hierarchy with Jira parent fields, rich-text subset, comments, attachments, worklogs, links, watchers, versions, issue-bound Advanced Forms lifecycle, fields and security | Complete ADF and metadata; epic and higher-level hierarchy, components, estimates, votes, properties, bulk operations, exact expansions and permissions |
| Search and filters | Partial | Useful JQL subset, navigator, filter CRUD and favourites | Full JQL grammar/functions/history, stable cursors, sharing administration, subscriptions and remaining search options |
| Agile planning | Partial | Boards, backlog, sprints, rank, quick filters, swimlanes, WIP and card configuration | Board CRUD/ownership, epics, estimation, capacity, teams, parallel sprints, dependencies, plans and report calculations |
| Workflows | Partial | Global and project-scoped custom statuses and workflows with isolated ownership, names, Jira scope beans and selectors, project-aware search and capability catalogs, all thirteen Jira status/status-category/usage operations as reviewed subsets, atomic multi-status create/update/delete with simultaneous renames, versioned workflow and scheme drafts, Jira published/draft/bulk scheme resources, modern status/transition expansion, executable transition conditions, validators, post-functions, branch-created development triggers and screens, project-associated workflow preview, structured validation plus atomic workflow/status batches, modern designer metadata, a connected drag/keyboard admin designer with serialized draft saves and visual actor, API-only, typed field-value, previous-status, separation-of-duties, child-status blocking, required-field, changed-field, single-value, regular-expression, date-comparison, date-window, history, permission, parent-status and Advanced Forms attached/submitted validators, assignee, label-update, same-or-parent field-copy, durable registered-webhook trigger and screen controls, nested ALL/ANY reporter/assignee/account restrictions, request-source blocking, typed comparisons, action-log status and transition-actor history, role-backed Jira permission enforcement, ordered atomic post-functions and screen-authorized updates, active work-item status migration with sync actions, directed topology, optimistic versions, active issue-status safety, whole-batch rollback and audit, paged workflow and scheme usage, guarded inactive deletion, impact preview, atomic status replacement, and durable Jira task execution/cancellation/recovery | Remaining system and ecosystem rule types, advanced parameters and exact team-managed workflow routing |
| Releases | Partial | Version lifecycle, fix/affects assignment, progress, notes, release/archive/delete | Ordering, related work, approvers, custom fields, exports, cross-project releases and full permissions; see [RELEASES.md](RELEASES.md) |
| Reports and metrics | Partial | Dashboard statistics/pie tables, release progress, immutable delivery facts, permission-filtered 7/30/90 day DORA summary, and per-desk service volume/open/resolved/SLA-breach/CSAT reports with accessible daily charts and tables | Jira and Agile report catalog, configurable DORA mappings and targets, segmented service metrics, comparisons, exports and scheduled delivery; see [REPORTS.md](REPORTS.md) |
| Dashboards | Partial | CRUD, favourites, layouts, private/user/workspace sharing and native work-item gadgets | Group/project sharing, archive/bulk edit, subscriptions, report/app gadgets and offline data; see [DASHBOARDS.md](DASHBOARDS.md) |
| Automation | Partial | Eight management operations, fixed intervals, durable execution, JQL, three idempotent issue actions and audit | Cron/event/webhook/manual triggers, conditions, branches, smart values, connections, templates, quotas and action catalog; see [AUTOMATION.md](AUTOMATION.md) |
| Notifications and collaboration | Partial | Synchronized in-app notifications, watching and activity | Notification schemes, preferences, mentions, email delivery and broader collaboration semantics |
| Wiki and knowledge | Partial | Filtered/sorted/paged current global space collections, public/private creation, plain/view descriptions, default icons, expanded exact reads, enforced-access permission discovery, built-in/custom space-role CRUD, durable assignments, assignment-enforced content read/create/update/delete authorization, scoped `administer/space` governance and direct user/group/access-class assignment management, versioned JSON space properties, space-admin-controlled default classification with inheritance for new pages and blog posts, permission-filtered page trees with v1/v2 child/ancestor/descendant resources, nested folders, validated HTTP/HTTPS Smart Links, creator-private database and whiteboard containers with heterogeneous page/folder/link/database/whiteboard ancestry, every documented whiteboard template key and locale, bounded descendants, role-shaped space/content operation discovery, versioned JSON properties and built-in classification state, storage subset, multi-status filtered/sorted page collections, expanded and historical reads, drafts, ordered and exact page versions, title-only page updates, trash/restore, page likes and operation discovery, built-in classification, guarded current/history page redaction, registered page custom-content discovery, versioned page JSON properties, threaded page/attachment footer comments, page inline discussions with exact-text anchors and resolution, page tasks with assignment, due dates and completion, labels, direct-user/group page restrictions, versioned page attachments with upload, replacement, download, bounded image thumbnails, history, JSON properties and labels, public/private blog post authoring with optimistic versions, trash/restore/purge, labels and label discovery, likes, operations, versioned JSON properties, built-in classification, guarded current/history redaction (an audited compliance exception to immutable history), registered custom-content discovery, permission-filtered versioned attachments with upload/replace/download/delete UI, threaded footer comments and exact-passage inline discussions with resolution, plus durable page/space/label watches with synchronized deduplicated notifications across v1/v2; 213 reviewed Confluence operations and UI | Remaining Confluence v1/v2; database schemas/rows/views, whiteboard canvas objects/editing and organization-defined classification levels; other non-page content; page children beneath non-page content and manual ordering; complete ADF/storage/macros including task extraction; richer media processing; rich anchor relocation; watch email delivery; CQL, exports/imports and live collaboration |
| Diagrams and graphs | Partial | Read-only workflow diagram, template-backed Confluence whiteboard containers and external Smart Links in the knowledge tree | Whiteboard canvas and diagram editing, rich embeds/exports, object graphs, accessible descriptions and historical visualizations |
| Service Management | Partial | Service-project creation, atomic desk/type/queue provisioning, seeded help/incident/problem/change intake with deterministic labels and agent-managed related-work links, customer help center and request journey, durable request-type-specific system/custom field forms and metadata, typed validation and canonical issue storage, customer-only identity activation/revocation, open/closed portal membership, organizations/members/properties/desk links, participants, public/internal conversation and attachments, approvals, subscriptions, CSAT, status transitions, incident-to-DORA linkage, per-desk agent roles, scoped all-request access, built-in and manager-defined JQL queues, business calendars with audited holiday administration, ordered JQL-based conditional/default first-response and resolution goals with stable cycle snapshots, SLA attention and durable escalation notifications, agent queue/assignment and customer-directory workspace, linked Confluence knowledge suggestions, request-type groups/permissions/properties, Assets workspace discovery, filterable service volume/SLA/CSAT reporting with request-type and channel breakdowns, four-by-four operations risk assessment, CAB policy and automatic approvals, on-call shifts, change windows, rollback plans, an agent-scoped conflict calendar, request-level overlap warnings, a permission-filtered dependency graph over canonical issue links, major-incident declaration with public and internal updates, ordered responder escalation policies with scheduled deduplicated delivery, post-incident review tracking, and all 75 pinned operations reviewed | Complete JQL beyond label matching, conditional and advanced portal fields, SLA rule reordering and advanced criteria, approval configuration, email delivery, CSAT configuration, service report comparisons, SLA goal distributions, exports and scheduled delivery, complete Assets object/schema/import APIs and Assets-backed topology and impact analysis; see [SERVICE_MANAGEMENT.md](SERVICE_MANAGEMENT.md) |
| Development integrations | Partial | All six devinfo, four build and five deployment operations store sequence-ordered source, CI and environment evidence; property cleanup, keyed reads/deletes, site and cloudId proxy paths, issue/release panels, issue-key/ID associations, per-item rejection, deployment gating reads, preventTransitions and branch-created workflow execution are tested | Feature flags, security, operations, components, configurable deployment gates, legacy dev-status summaries, Connect JWT scope enforcement, full validation and rate limits |
| App runtime | Partial | Administrator-authorized install/reinstall with encrypted shared secrets, explicit scopes, HMAC-signed fresh and replay-protected lifecycle/storage requests, isolated versioned JSON storage, scope-safe upgrades, suspension/uninstall, organization audit, validated Jira/Confluence module registrations and host-rendered global navigation pages; core webhooks and entity properties | Connect JWT/QSH and descriptor parity, app principals, outbound lifecycle delivery, remote modules, issue panels, dashboard gadgets, byline items, workflow/custom-field modules, app webhooks, scheduled functions and upgrade migrations; see [APPS.md](APPS.md) |
| Local-first behavior | Partial | Issue replica/outbox, offline issue work, two-client convergence, authorization-before-replay and revoked-access purge of private SQLite data, queued mutations and authenticated page caches | Permission-shaped service, knowledge, report and administration replicas plus broader safe queued writes and schema upgrades |
| Accessibility and interaction | Partial | Core light/dark, keyboard, reflow and axe coverage | Equivalent coverage for each new persona journey, chart table, editor, diagram and administrative surface |

## Delivered compatibility details

Precise behavior and remaining limitations for completed vertical slices are
kept with their owning surface:

- [Scheduled automation](AUTOMATION.md)
- [Custom dashboards](DASHBOARDS.md)
- [Releases](RELEASES.md)
- [Authentication and ShAuth reference configuration](shauth-sso.md)
- [Organization and site administration](ADMIN.md)
- [Accessibility](ACCESSIBILITY.md)
- [User journeys](UI_PARITY.md)

The source code, contract fixtures, integration tests, browser tests, and action
schemas are the final evidence when prose and implementation disagree.

## Completion evidence

A capability is complete only when it has:

- a pinned or manually versioned public contract;
- schema and request/response golden tests;
- permission, failure, pagination, and concurrency tests where relevant;
- a browser journey for user-facing behavior;
- action-log and browser-replica coverage when the entity is local-first;
- keyboard, accessibility, responsive, dark-mode, and reduced-motion coverage;
- a named base-URL client fixture for client compatibility claims.

## Primary contract references

- [Jira Cloud Platform REST v3](https://developer.atlassian.com/cloud/jira/platform/rest/v3/)
- [Jira Software Cloud REST](https://developer.atlassian.com/cloud/jira/software/rest/)
- [Jira Service Management Cloud REST](https://developer.atlassian.com/cloud/jira/service-desk/rest/)
- [Confluence Cloud REST v2](https://developer.atlassian.com/cloud/confluence/rest/v2/)
- [Confluence Cloud REST v1](https://developer.atlassian.com/cloud/confluence/rest/v1/)
- [Automation REST](https://developer.atlassian.com/cloud/automation/rest/)
- [Organizations REST](https://developer.atlassian.com/cloud/admin/organization/rest/)
- [Forge platform boundary](https://developer.atlassian.com/platform/forge/introduction/the-forge-platform/)
- [Connect app descriptor](https://developer.atlassian.com/cloud/jira/platform/connect-app-descriptor/)
