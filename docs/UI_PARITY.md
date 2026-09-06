# UI, UX, and user-journey ledger

Updated: 2026-09-06

This ledger measures complete user goals across Work, Service, Knowledge,
Insights, and Admin. A page or API route alone does not complete a journey.
Product scope and compatibility limits are in [CLOUD_PARITY.md](CLOUD_PARITY.md),
and delivery order is in [PLAN.md](../PLAN.md).

Status: ✅ browser-tested complete journey · 🟡 usable tested subset · ⛔ absent.

## Journey status

| Persona and goal | State | Current evidence and gap |
|---|---:|---|
| User signs in and orients | Partial | ✅ Password plus simultaneous Shauth, Google, Microsoft and Atlassian provider choice, profile-based identity review/connect/disconnect, issuer-scoped session revocation, responsive shell, theme and session controls; product switcher remains |
| Contributor finds work | ✅ | Project-scoped basic/JQL search, filters, columns, sorting, pagination, keyboard navigation and contextual preview |
| Contributor creates and triages work | ✅ | Shared create metadata, validation recovery, inline fields, security, labels, watchers, links, activity, attachments and worklogs |
| Contributor plans and runs a sprint | ✅ | Backlog grouping/ranking, sprint create/edit/start/complete, board movement, quick/assignee filters, WIP feedback, swimlanes and issue preview |
| Contributor works offline | Partial | ✅ Issue reads/edits, reconnect drain and server reconciliation; other product entities and richer mutations remain online-only |
| Contributor follows code through release | Partial | ✅ Version scope, progress, notes and lifecycle; development/build/deployment evidence and complete release governance remain |
| Manager configures a project | Partial | ✅ Scrum/Kanban create, details, lead/default assignment and workflow selection; roles, types, fields, screens, schemes, security and lifecycle remain |
| Manager plans across teams | Missing | Hierarchy, teams, capacity, dependencies, timeline, scenarios and cross-project plans remain |
| Agile coach diagnoses delivery | Missing | Sprint, velocity, burnup, burndown, cumulative-flow and control-chart journeys remain |
| Manager builds an operating dashboard | Partial | ✅ Configurable dashboards, layouts, favourites, sharing, refresh and native work-item gadgets; report gadgets, complete shares, subscriptions and exports remain |
| Admin designs and publishes a workflow | Partial | ✅ Directory, diagrams, custom transitions and project assignment; status editing, draft/publish, schemes, conditions, validators and post-functions remain |
| Admin automates work | Partial | ✅ Fixed schedule → JQL → label/assign/transition flow, management, run-now and audit; trigger/action catalog, conditions, branches, smart values and templates remain |
| Service customer requests help | Missing | Help center, knowledge discovery, request forms, conversation, approval, notifications and feedback remain |
| Service agent works a queue | Missing | Queue, request workspace, private collaboration, SLA, assignment, approval and escalation remain |
| Service manager runs a service | Missing | Portal/request-type setup, forms, calendars, SLAs, Assets, incident/problem/change configuration and reporting remain |
| Knowledge user authors a page | Partial | ✅ Space/page create, storage editor, drafts, parents, history, search and trash/restore; complete editor, comments, attachments, restrictions and collaboration remain |
| Knowledge team collaborates live | Missing | Live documents, presence, concurrent operations, inline discussion, tasks and notifications remain |
| Knowledge user diagrams or models data | Missing | Whiteboards, diagrams, databases, object links, embeds and exports remain |
| Space manager governs knowledge | Partial | ✅ Public/private space creation and page lifecycle; roles, granular permissions, templates, analytics, archive/import/export remain |
| Site admin manages people and access | Partial | ✅ Organization/site/product foundation, DNS domain claims, policy create/scope/enable/delete, directory group lifecycle, managed profiles, product/invitation access, durable email, directory lifecycle, credential revocation, searchable audit and event APIs; runtime enforcement, providers and remaining identity journeys remain |
| Site admin manages apps | Missing | Install/configure/suspend/upgrade/uninstall, scopes, storage and app audit remain |

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
2. Submit a request from a request-type-specific conditional form.
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
   workflow schemes with impact previews and audit records.
4. Install and govern apps, scopes, callbacks, storage and scheduled work.
5. Export data, set retention, inspect audit events and perform recovery actions.

## Interaction system

The existing shell remains compact and Jira-like. It gains a stable product
switcher for Work, Service, Knowledge, Insights, and Admin. Context navigation
changes by product while global search, create, alerts, help, and account controls
remain stable.

The visual signature is an operating timeline linking work, code, build,
deployment, incident, recovery, and release evidence. Reports use accessible SVG
and equivalent tables. Diagrams expose keyboard editing and a maintained text
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
