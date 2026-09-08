# UI, UX, and user-journey ledger

Updated: 2026-09-08

This ledger measures complete user goals across Work, Service, Knowledge,
Insights, and Admin. A page or API route alone does not complete a journey.
Product scope and compatibility limits are in [CLOUD_PARITY.md](CLOUD_PARITY.md),
and delivery order is in [PLAN.md](../PLAN.md).

Status: ✅ browser-tested complete journey · 🟡 usable tested subset · ⛔ absent.

## Journey status

| Persona and goal | State | Current evidence and gap |
|---|---:|---|
| User signs in and orients | Partial | ✅ Password plus simultaneous Shauth, Google, Microsoft and Atlassian provider choice, profile-based identity review/connect/disconnect, durable admin availability controls, issuer-scoped session revocation, responsive shell, theme and session controls; product switcher remains |
| Contributor finds work | ✅ | Project-scoped basic/JQL search, filters, columns, sorting, pagination, keyboard navigation and contextual preview |
| Contributor creates and triages work | ✅ | Shared create metadata, validation recovery, one-level sub-task creation and reassignment, parent/child navigation, inline fields, security, labels, watchers, links, activity, attachments, worklogs and issue-form lifecycle |
| Contributor plans and runs a sprint | ✅ | Backlog grouping/ranking, sprint create/edit/start/complete, board movement, quick/assignee filters, WIP feedback, swimlanes and issue preview |
| Contributor works offline | Partial | ✅ Issue reads/edits, authorization-before-replay, reconnect drain and server reconciliation; suspension while offline purges the private replica, queued mutations and authenticated page cache before sign-out. Other product entities and richer mutations remain online-only |
| Contributor follows code through release | Partial | ✅ Linked branches, commits, pull requests, builds and deployments on the work item, with the same CI/environment evidence rolled into version scope, progress, notes and lifecycle; complete release governance remains |
| Manager configures a project | Partial | ✅ Scrum/Kanban create, details, lead/default assignment and workflow selection; roles, types, fields, screens, schemes, security and lifecycle remain |
| Manager plans across teams | Missing | Hierarchy, teams, capacity, dependencies, timeline, scenarios and cross-project plans remain |
| Agile coach diagnoses delivery | Missing | Sprint, velocity, burnup, burndown, cumulative-flow and control-chart journeys remain |
| Engineering manager reviews delivery | Partial | ✅ Permission-filtered DORA summary, window selection, daily accessible production chart/table, recent environment events, release rollup, dark theme and 320 px reflow; comparisons, targets, filters, exports and subscriptions remain |
| Manager builds an operating dashboard | Partial | ✅ Configurable dashboards, layouts, favourites, sharing, refresh and native work-item gadgets; report gadgets, complete shares, subscriptions and exports remain |
| Admin designs and publishes a workflow | Partial | ✅ Global or project-scoped creation, automatic project assignment, connected status map, drag and keyboard layout editing, serialized draft saves, custom transition drafts with visual reporter/assignee/API-only restrictions, typed system-field comparisons, previous-status, separation-of-duties and child-status conditions, history, required-field, changed-field, single-value, regular-expression, date-comparison, date-window, Jira-permission, parent-status and Advanced Forms validators, assignee effects, label append/replace, same-or-parent field copy, registered-webhook and branch-created development triggers, screen fields, published-version isolation, explicit publish/discard, plus nested API rule configuration enforced in browser and REST transitions; custom-field UI selection, advanced rule parameters and complete scheme administration remain |
| Admin automates work | Partial | ✅ Fixed schedule → JQL → label/assign/transition flow, management, run-now and audit; trigger/action catalog, conditions, branches, smart values and templates remain |
| Service customer requests help | Partial | ✅ Help-center and portal discovery, linked knowledge search/article viewing, request-type-specific forms with required text, number and date-time custom fields, typed help/incident/problem/change request submission, owned request list/detail with structured answers, participant add/remove, public conversation and files, approvals, subscription controls, completed-request feedback, customer-visible SLA state, organization visibility, and status transitions with light/dark accessibility and 320 px reflow; conditional, select, user and Assets-backed form fields remain |
| Service agent works a queue | Partial | ✅ Per-desk agent assignment/revocation, assigned-desk navigation, scoped all-request reads, all-open/SLA-attention/unassigned/assigned-to-me and manager-defined JQL views, responsive request table, take/unassign ownership, raise-on-behalf, request detail, participants, approvals, attachments, private collaboration, customer organizations, incident/problem/change risk assessment, active on-call ownership, change windows, rollback plans, an agent-scoped conflict calendar, request-level overlap warnings, a permission-filtered dependency graph, major-incident declaration with audience-aware status updates, post-incident review tracking, visible related-work links, direct affected/dependency asset links with transitive upstream impact, workflow actions, SLA cycles and deduplicated warning/breach notifications; bulk queue actions, incident command roles, external stakeholder channels and complete JQL remain |
| Service manager runs a service | Partial | ✅ Site administrators manage each desk's request-type field forms/help/requirements, custom JQL queues, agent roster, open/closed customer admission, customer invitation/membership, linked Confluence spaces, business calendar, holidays, default goals, ordered JQL-based conditional first-response/resolution goals, CAB risk threshold and membership, incident-review deadline, on-call shifts, and ordered timed major-incident responder escalations, typed Assets schemas and inventory objects, positioned directional service topology and relationship lifecycle; high-risk changes receive one automatic CAB approval, while agents manage customer organizations, membership, properties and desk links and inspect and filter 7/30/90 day volume, open/resolved, SLA-breach and CSAT reports with request-type/channel breakdowns and accessible charts/tables; conditional fields, SLA rule reordering and advanced criteria, complete public Assets import/API parity, report comparisons, SLA goal distributions, exports and scheduled delivery remain |
| Admin starts a service project | Partial | ✅ Service-management template creation, service-project identity, atomic desk provisioning, default help, incident, problem and change request types with required forms and lifecycle labels, seeded configurable request forms and matching field metadata, customer-only account activation/revocation, agent roster, all-request visibility and internal notes; full portal branding and the remaining service lifecycle remain |
| Knowledge user authors and discusses a page | Partial | ✅ Space/page create, inherited space classification defaults, permission-filtered page/folder/Smart Link/database/whiteboard trees, folder and validated external Smart Link creation beneath heterogeneous content, creator-private database and template-backed whiteboard containers with classification controls, typed database columns, validated record create/edit/delete and saved filter/sort views, positioned whiteboard objects and directional connectors with visual/list editing, public/private blog post authoring with version history, trash/restore/purge, labels, likes, app properties, classification, exact-text current/history redaction, versioned attachment upload/replace/download/delete, threaded comments and exact-passage inline discussions with replies/resolution, safe link opening and empty-content deletion, storage editor, drafts, history, cross-content search, trash/restore, labels, page likes, built-in classification, exact-text current/history redaction, registered page custom-content discovery, versioned page app properties, threaded page/attachment footer comments, exact-passage inline discussions with replies/resolution, page tasks with assignees/due dates/completion, direct-user/group view/edit restrictions, permission-scoped attachment upload/replacement/download/deletion/labels/versioned JSON properties, and page/space/label watches that open deduplicated in-app notifications; complete editor, advanced whiteboard objects/direct manipulation, page children beneath non-page content, manual ordering, mentions, storage-macro task extraction, rich anchor relocation, watch email delivery and live collaboration remain |
| Knowledge team collaborates live | Missing | Live documents, presence and concurrent operations remain; durable page tasks, inline discussions and watch notifications are available |
| Knowledge user diagrams or models data | Partial | ✅ Creator-private database containers and template-backed whiteboards can be placed in the content tree and assigned built-in data classifications; databases provide typed text/number/date/checkbox/select schemas, validated editable records and reusable permission-aware filter/sort views; whiteboards provide positioned sticky/text/shape objects, directional solid/dashed connectors and an accessible visual/list editor; direct manipulation, advanced objects, rich embeds and exports remain |
| Space manager governs knowledge | Partial | ✅ Public/private space creation, role-shaped operation discovery, built-in/custom permission bundles, direct user/group/access-class assignment add/remove flows, runtime content permission enforcement, and scoped `administer/space` control of assignments, labels, default classification and versioned app properties; templates, analytics, archive/import/export and organization-defined classification levels remain |
| Site admin manages people and access | Partial | ✅ Organization/site/product foundation, DNS domain claims, policy create/scope/enable/delete, directory group lifecycle, managed profiles, product/invitation access, durable email, directory lifecycle, encrypted OIDC provider registration/rotation/deletion, provider availability, credential revocation, searchable audit and event APIs; runtime policy enforcement and remaining enterprise identity journeys remain |
| Site admin manages apps | Partial | ✅ Encrypted native or standard Connect descriptor installation and reinstallation, translated scopes, stable non-human principals with HMAC or Connect JWT/QSH scope-enforced Jira/Agile/JSM/Confluence API access, explicit descriptor format/scope/module/callback review, active/suspended/uninstalled lifecycle controls, organization audit, isolated storage, host-rendered or sandboxed signed remote global navigation and Connect web items, static and dynamically registered issue panels, persisted issue quick-add content with signed remote panels, tenant-scoped scalar issue fields that join create/edit/view and REST/JQL journeys, custom-dashboard gadgets and wiki byline items, Jira/Confluence-path dynamic-module register/list/remove for panels, navigation items, scalar fields and keyed webhooks, plus descriptor-declared outbound lifecycle callbacks with Connect JWT, JQL-filtered delivery, four schedule intervals, durable recovery and recent outcome inspection; remaining Connect module families, dynamic module types and webhook options, issue-content presence conditions/native rendering, web-item conditions/locations, select/read-only field options, Forge-hosted compute, workflow modules and upgrade migrations remain |

## Required persona journeys

### Contributor

1. Choose an identity provider and enter the correct site and product context.
2. Find or create work without losing project context.
3. Refine requirements with rich content, relationships, files and discussion.
4. Plan work in a backlog and sprint, then move it through an enforced workflow.
5. Inspect code, build, deployment, incident and release evidence.
6. Continue safe work offline and reconcile it on reconnect.

### Product or project manager

1. Create a project from a real template and configure its work model and access.
2. Plan hierarchy, teams, capacity, dependencies, dates and alternative scenarios.
3. Govern versions, readiness, approvals and release communication.
4. Build, share, subscribe to and export dashboards and reports.
5. Drill every aggregate into the issues and events that produced it.

### Agile coach

1. Configure Scrum or Kanban policy, estimation, columns, WIP and parallel work.
2. Inspect sprint report, velocity, burnup, burndown, cumulative flow and control
   charts with stable historical calculations.
3. Segment measures by team, type, priority, component, release and saved filter.
4. Open the exact scope changes, blocked intervals and transitions behind a point.

### Service customer

1. Search a branded help center and linked knowledge base.
2. Submit a request from a request-type-specific form, including conditional
   fields where configured.
3. See customer-visible status and SLA information, add comments and files, add
   participants, approve or decline, and manage notifications.
4. Reopen where allowed and leave feedback when the request completes.

### Service agent and manager

1. Work prioritized queues with bulk assignment and impending-breach indicators.
2. Resolve requests using public replies and private team collaboration.
3. Coordinate incidents, problems, changes, approvals, assets and knowledge.
4. Configure portals, request types, forms, calendars, queues, SLAs and automation.
5. Diagnose service volume, response, resolution, breach and satisfaction trends.

### Knowledge collaborator and space manager

1. Create pages, blogs, live documents, whiteboards, diagrams and databases.
2. Organize content into a navigable tree and move/copy it safely.
3. Co-edit, comment inline, mention, assign tasks, watch, like and share.
4. Embed work and service objects with permission-safe Smart Links.
5. Govern roles, restrictions, templates, versions, archive and retention.
6. Search using UI filters and CQL, then import or export supported formats.

### Project, site and organization administrator

1. Manage organizations, sites, products, directories, users, groups and roles.
2. Configure authentication providers, domains, access policy, sessions and tokens.
3. Configure permission, notification, security, field, screen, work-type and
   workflow schemes with impact previews and audit records. The current status
   directory covers global and project-scoped status creation, classification,
   ownership labels, editing, live usage counts, protected built-ins, and safe
   deletion. Workflow-scheme journeys now
   cover defaults, issue-type overrides, draft publishing, project usage, and
   explicit replacement choices that migrate incompatible statuses as part of
   the project assignment transaction.
4. Install and govern apps, scopes, callbacks, storage and scheduled work.
5. Export data, set retention, inspect audit events and perform recovery actions.

## Interaction system

The existing shell remains compact and Jira-like. It gains a stable product
switcher for Work, Service, Knowledge, Insights, and Admin. Context navigation
changes by product while global search, create, alerts, help, and account controls
remain stable.

The visual signature is an operating timeline linking work, code, build,
deployment, incident, recovery, and release evidence. Reports use accessible SVG
and equivalent tables. The first DORA report uses immutable delivery updates
and visible issue history; see [REPORTS.md](REPORTS.md). Diagrams expose keyboard editing and a maintained text
description. Dense lists keep sorting and filtering visible rather than hiding
routine work inside cards or modal layers.

## Quality gate for every journey

- The browser test starts from the persona's entry point and finishes at their
  observable outcome.
- API tests exercise equivalent state changes through the shared command path.
- Authorization is tested with an allowed user and a plausible denied user.
- Keyboard, focus, names, contrast, dark mode, reduced motion, and 320px reflow
  remain usable.
- Offline and two-client convergence are tested whenever the journey promises
  local-first behavior.
- Errors preserve entered data and tell the user which action can fix the problem.
- The UI contains no enabled control whose behavior is a placeholder.
