# ZZIRA × Atlassian Cloud REST API — compatibility matrix

A grouped, hand-maintained ledger of the delivered API surface. It is not a
certification.
- **Legend:** ✅ tested slice with no known gaps in the listed scope · 🟡 known
  subset.
- **Per-operation truth:** [cloud-coverage.json](cloud-coverage.json), generated
  by `coverage.py` from [coverage-assessments.json](coverage-assessments.json).
  All 1,207 pinned operations in [cloud-operations.json](cloud-operations.json)
  are assessed as partial:

  | Family | Operations |
  |---|---|
  | Jira platform | 617 |
  | Jira Software | 105 |
  | Jira Service Management | 75 |
  | Confluence | 348 |
  | Automation | 15 |
  | Organization administration | 47 |

- **Pins and checks:** see [PINNED.md](../PINNED.md).
- **Overall status:** see [CLOUD_PARITY.md](../../docs/CLOUD_PARITY.md).
- **Control-plane endpoints:** ZZIRA's own use `/rest/zzira/1`.

## Jira platform — work items and search

| Surface | Status | Notes |
|---|---|---|
| `GET /rest/api/3/serverInfo` | ✅ | |
| `/myself`, `/mypreferences`, `/mypreferences/locale` | 🟡 | Groups/applicationRoles expansions, per-site preferences, supported locales |
| Users, user search and query, groups, pickers, user properties, application roles, avatars (52 ops) | 🟡 | Email visibility rules; `is <relation> of` and `[property]` query language; swap-group deletion; email lookups need an approved app. [PEOPLE.md](../../docs/PEOPLE.md) |
| `/project`, `/project/search`, `/project/{keyOrId}` | 🟡 | Business, service and software (Scrum/Kanban) creation, categories, paging. Complete expansions remain |
| `POST /issue` | ✅ | Key/id project, ADF, versions, security, typed context-aware custom fields; unsupported fields are explicit errors |
| `GET/PUT/DELETE /issue/{idOrKey}` | ✅ | `expand=renderedFields` |
| `/issue/{idOrKey}/properties` | 🟡 | Jira limits and status codes. Anonymous access remains |
| `/issue/{idOrKey}/assignee` | ✅ | |
| `/issue/{idOrKey}/editmeta`, `/issue/createmeta` (+ paged) | ✅ | Resolved from screens, field configurations and contexts |
| `/issue/{idOrKey}/transitions` | ✅ | Conditions, validators and post-functions enforced ([WORKFLOW_RULES.md](../../docs/WORKFLOW_RULES.md)) |
| `/jira/forms/cloud/{cloudId}/issue/{idOrKey}/form` | 🟡 | Attach, save, submit, reopen, workflow validators. Templates, exports, attachments and external data remain |
| Comments, links, link types, remote links, watchers, changelogs, picker, notify, events, archive, redaction, bulk properties, issue panels (61 ops) | 🟡 | Numeric ids, Jira link direction, comment visibility, redaction with history scrubbing. Expression-sourced property values are refused. [ISSUE_SURFACE.md](../../docs/ISSUE_SURFACE.md) |
| Bulk watch/unwatch, fields, edit, delete, move, transition, `/bulk/queue/{taskId}` | 🟡 | Durable tasks with per-item results and access rechecks. Configurable Bulk change permission, 14-day retention, screen-field input and bulk email remain ([BULK_ISSUES.md](../../docs/BULK_ISSUES.md)) |
| `/attachment` metadata, content, thumbnail, archive, settings, delete | 🟡 | Ranges, ZIP listings, leased blob cleanup. Renditions, signed redirects, scanning and more archive formats remain ([ATTACHMENTS.md](../../docs/ATTACHMENTS.md)) |
| `/issue/{idOrKey}/votes` | 🟡 | Site voting policy remains |
| Comment CRUD | ✅ | ADF bodies |
| `/issue/{idOrKey}/changelog` | ✅ | From the action log |
| Worklogs (14 ops) | 🟡 | Updated/deleted feeds, bulk fetch, properties. Started-date filters, `adjustEstimate` and visibility restriction remain ([WORKLOGS.md](../../docs/WORKLOGS.md)) |
| `/search`, `/search/jql`, `/search/approximate-count` | 🟡 | Permission-filtered JQL, history operators, app functions, snapshots, reconciliation ([JQL.md](../../docs/JQL.md)) |
| JQL autocomplete, suggestions, parse, match, sanitize, migration (7 ops) | 🟡 | Personal-data migration and some validation warnings remain |
| `/jql/function/computation` | 🟡 | App-owned precomputations. Forge identity remains ([APPS.md](../../docs/APPS.md#jql-functions)) |
| `/permissions`, `/mypermissions`, `/permissions/check`, `/permissions/project`, `/user/permission/search` | 🟡 | Scheme-based evaluation, app permissions. Anonymous discovery remains ([PERMISSION_SCHEMES.md](../../docs/PERMISSION_SCHEMES.md)) |
| `/issueLinkType`, `/issueLink` | ✅ | |
| `/label`, `/issuetype`, `/priority`, `/status`, `/statuscategory`, `/resolution` | ✅ | |
| Issue types, schemes, properties, priorities, priority schemes, resolutions (46 ops) | 🟡 | Per-site numeric ids; async deletion tasks; work types on a hierarchy extended above Epic, administered in the browser. Team-managed scoping and priority scheme editing remain ([ISSUE_METADATA.md](../../docs/ISSUE_METADATA.md)) |
| Jira expressions: `/expression/analyse`, `/eval`, `/evaluate` | 🟡 | Jira's limits and context variables ([JIRA_SOFTWARE.md](../../docs/JIRA_SOFTWARE.md#jira-expressions)) |

## Jira platform — configuration and administration

| Surface | Status | Notes |
|---|---|---|
| `/field`, custom fields in beans and JQL | ✅ | |
| Issue fields (11 ops) | 🟡 | Trash/restore, usage counts. Translations, `lastUsed` and `stableId` remain ([ISSUE_FIELDS.md](../../docs/ISSUE_FIELDS.md)) |
| Custom field contexts (14 ops) | 🟡 | [CUSTOM_FIELD_CONTEXTS.md](../../docs/CUSTOM_FIELD_CONTEXTS.md) |
| Custom field options (7 ops) | 🟡 | [CUSTOM_FIELD_OPTIONS.md](../../docs/CUSTOM_FIELD_OPTIONS.md) |
| App-provided select field options (8 ops) | 🟡 | `projects2` scope, `defaultValue` and screen-security overrides remain ([APP_FIELD_OPTIONS.md](../../docs/APP_FIELD_OPTIONS.md)) |
| Field configurations and schemes (15 ops) | 🟡 | Enforced on create, edit and transition ([FIELD_CONFIGURATIONS.md](../../docs/FIELD_CONFIGURATIONS.md)) |
| Field association schemes (17 ops) | 🟡 | `rendererType`, `matchedFilters` and non-project contexts remain ([FIELD_ASSOCIATION_SCHEMES.md](../../docs/FIELD_ASSOCIATION_SCHEMES.md)) |
| Screens, tabs, tab fields (17 ops) | 🟡 | [SCREENS.md](../../docs/SCREENS.md) |
| Screen schemes, issue type screen schemes (15 ops) | 🟡 | [SCREEN_SCHEMES.md](../../docs/SCREEN_SCHEMES.md) |
| Workflows: search, modern create/update/preview/capabilities, project workflows | ✅ | Enforced at runtime ([WORKFLOW_SCHEMES.md](../../docs/WORKFLOW_SCHEMES.md)) |
| `/workflow/history`, `/workflows`, `/workflow/rule/config` | 🟡 | 60-day history. App transition rules are stored but no remote module runs ([JIRA_PLATFORM.md](../../docs/JIRA_PLATFORM.md)) |
| Permission schemes (11 ops) | 🟡 | [PERMISSION_SCHEMES.md](../../docs/PERMISSION_SCHEMES.md) |
| Issue security schemes (20 ops) | 🟡 | [ISSUE_SECURITY_SCHEMES.md](../../docs/ISSUE_SECURITY_SCHEMES.md) |
| Notification schemes (9 ops) | 🟡 | [NOTIFICATION_SCHEMES.md](../../docs/NOTIFICATION_SCHEMES.md) |
| Project roles and actors (15 ops) | 🟡 | App and service actor types remain ([PROJECT_ROLES.md](../../docs/PROJECT_ROLES.md)) |
| Project categories, properties, features, email, types, validation (20 + 5 ops) | 🟡 | Custom-domain verification and app features remain ([PROJECT_GOVERNANCE.md](../../docs/PROJECT_GOVERNANCE.md)) |
| Recent, archive, restore, trash, delete projects (5 ops) | 🟡 | 60-day purge ([PROJECT_LIFECYCLE.md](../../docs/PROJECT_LIFECYCLE.md)) |
| Project components (8 ops) | 🟡 | Compass components remain ([COMPONENTS.md](../../docs/COMPONENTS.md)) |
| Project versions (15 ops) | 🟡 | Expansion beyond issue counts and related-work ordering remain ([PROJECT_VERSIONS.md](../../docs/PROJECT_VERSIONS.md), [RELEASES.md](../../docs/RELEASES.md)) |
| Custom project templates | 🟡 | Team-managed projects and new configuration within the request are 400 ([JIRA_PLATFORM.md](../../docs/JIRA_PLATFORM.md)) |
| Announcement banner, configuration, application properties, time tracking, navigator defaults (13 ops) | 🟡 | [JIRA_SITE_CONFIGURATION.md](../../docs/JIRA_SITE_CONFIGURATION.md) |
| Filters, sharing, columns, default scope (19 ops) | 🟡 | Daily/weekly email subscriptions ([FILTERS.md](../../docs/FILTERS.md)) |
| Dashboards and gadgets (17 ops) | 🟡 | Per-dashboard bulk edit. `extendAdminPermissions` and item property expansion remain ([DASHBOARDS_API.md](../../docs/DASHBOARDS_API.md), [DASHBOARDS.md](../../docs/DASHBOARDS.md)) |
| Webhooks: `/webhook`, `/webhook/refresh`, `/webhook/failed`, `/rest/webhooks/1.0/webhook` | 🟡 | App and admin webhooks with JQL/field filters, 30-day expiry, five attempts ([JIRA_PLATFORM.md](../../docs/JIRA_PLATFORM.md)) |
| App properties (8 ops), Forge UI modifications (4 ops) | 🟡 | [JIRA_PLATFORM.md](../../docs/JIRA_PLATFORM.md) |
| Connect app migration, service registry, app custom field configuration and values | 🟡 | [JIRA_PLATFORM.md](../../docs/JIRA_PLATFORM.md) |
| Plans, plan teams (16 ops), Atlassian teams | 🟡 | No scheduling runs from plans ([JIRA_PLATFORM.md](../../docs/JIRA_PLATFORM.md)) |
| Classification levels, data policy, licensing, audit records, project statuses and hierarchy (17 ops) | 🟡 | [JIRA_PLATFORM.md](../../docs/JIRA_PLATFORM.md), [CLASSIFICATION_LEVELS.md](../../docs/CLASSIFICATION_LEVELS.md) |
| `/task/{taskId}`, `/task/{taskId}/cancel` | ✅ | Durable progress, cancellation, stale-claim recovery |
| `/rest/zzira/1/notifications`, `/notifications/read-all` | ✅ | ZZIRA-owned, private, synchronized |

## Jira Software

| Surface | Status | Notes |
|---|---|---|
| Boards (33 ops, `/rest/agile/1.0` and `/rest/software/1.0`) | 🟡 | Configuration, quick filters, backlog, features, reports, properties, filter-backed boards ([AGILE_BOARDS.md](../../docs/AGILE_BOARDS.md)) |
| Sprints (13 ops) | 🟡 | Partial update, swap, properties. Ranking parameters and issue filters remain |
| Rank: `/issue/rank`, `/backlog/{boardId}/issue`, `/board/{boardId}/issue` | 🟡 | Site-wide LexoRank, 50 issues, 207 per-issue results |
| Epics, `/issue/{id}/estimation`, Agile `/issue/{id}` | 🟡 | Fourteen epic colors; cross-project epic parents ([JIRA_SOFTWARE.md](../../docs/JIRA_SOFTWARE.md)) |
| `/rest/devinfo/0.10` (6), `/rest/builds/0.1` (4), `/rest/deployments/0.1` (5) | 🟡 | Sequence-ordered ingestion, issue and release evidence, `cloudId` aliases, deployment gating, rate limits. Processing is synchronous |
| `/rest/operations/1.0`, `/rest/security/1.0`, `/rest/devopscomponents/1.0`, `/rest/featureflags/0.1`, `/rest/remotelinks/1.0` (29 ops) | 🟡 | Schema validation, accepted/failed/unknown reporting, deletes by property. Processing is synchronous; no issue view panels for these entities |

## Jira Service Management and Automation

| Surface | Status | Notes |
|---|---|---|
| `/rest/servicedeskapi` (75 ops) | 🟡 | Desks, request types, fields, requests, comments, participants, queues, SLAs, approvals, attachments, feedback, customers, organizations, knowledge, Assets workspace discovery. Public Assets API, request type restrictions and email channel remain ([SERVICE_MANAGEMENT.md](../../docs/SERVICE_MANAGEMENT.md)) |
| Automation rule management, manual rules, templates (15 ops) | 🟡 | Scheduled, event, webhook and manual runtime with a component subset. Connections, usage limits and Confluence triggers remain ([AUTOMATION.md](../../docs/AUTOMATION.md)) |

## Confluence

| Surface | Status | Notes |
|---|---|---|
| Spaces, pages, folders, Smart Links, databases, whiteboards, hierarchy, labels, restrictions, attachments, comments, tasks, watches | 🟡 | v1/v2 CRUD, drafts, versions, trash, permission-filtered trees, 54 folder/Smart Link/database/whiteboard ops, inline and footer comments, 12 restriction ops, 12 watch ops ([CONFLUENCE_SITE_SURFACES.md](../../docs/CONFLUENCE_SITE_SURFACES.md)) |
| Page moves, copies, archiving (7 ops) | 🟡 | Cross-space moves, copying permissions and restoring archived pages remain ([PAGE_MOVES.md](../../docs/PAGE_MOVES.md)) |
| Content states (8 ops) | 🟡 | Blog post and custom content states remain ([CONTENT_STATES.md](../../docs/CONTENT_STATES.md)) |
| Content history, macros, body conversion (9 ops) | 🟡 | Macros are stored, not executed ([CONTENT_HISTORY.md](../../docs/CONTENT_HISTORY.md)) |
| Site surfaces: comment properties, Forge app properties, admin key, id conversion, access checks, data policy (16 ops) | 🟡 | [CONFLUENCE_SITE_SURFACES.md](../../docs/CONFLUENCE_SITE_SURFACES.md) |
| Content analytics (2 ops) | 🟡 | View counts ([CONTENT_ANALYTICS.md](../../docs/CONTENT_ANALYTICS.md)) |
| CQL search (2 ops) | 🟡 | 18 fields. No relevance ranking or stemming ([CQL_SEARCH.md](../../docs/CQL_SEARCH.md)) |
| Content relations (5 ops) | 🟡 | Relation notifications remain ([CONTENT_RELATIONS.md](../../docs/CONTENT_RELATIONS.md)) |
| Audit log (6 ops) | 🟡 | Records are added through the API, not raised automatically ([WIKI_AUDIT.md](../../docs/WIKI_AUDIT.md)) |
| Templates and blueprints (8 ops) | 🟡 | App-provided blueprints remain ([CONTENT_TEMPLATES.md](../../docs/CONTENT_TEMPLATES.md)) |
| Site settings, look and feel, themes (8 ops) | 🟡 | Values stored without field validation ([SITE_SETTINGS.md](../../docs/SITE_SETTINGS.md)) |
| Space lifecycle (11 ops) | 🟡 | Space icons, alias routing and restore remain ([SPACE_LIFECYCLE.md](../../docs/SPACE_LIFECYCLE.md)) |
| Space permissions (6 ops) | 🟡 | [SPACE_PERMISSIONS.md](../../docs/SPACE_PERMISSIONS.md) |
| Space permission transition (5 ops) | 🟡 | Cursor paging and other principal types remain ([SPACE_PERMISSION_TRANSITION.md](../../docs/SPACE_PERMISSION_TRANSITION.md)) |
| Groups (8 ops) | 🟡 | `accessType`, `expand` and cursors remain ([WIKI_GROUPS.md](../../docs/WIKI_GROUPS.md)) |
| Users (14 ops) | 🟡 | `expand`, cursors, external collaborators and invites remain ([WIKI_USERS.md](../../docs/WIKI_USERS.md)) |
| Custom content (19 ops) | 🟡 | Attachments, footer comments and `atlas_doc_format` remain ([CUSTOM_CONTENT.md](../../docs/CUSTOM_CONTENT.md)) |

## Organization administration and apps

| Surface | Status | Notes |
|---|---|---|
| Organizations, directories, users, groups, memberships, workspaces, roles, events, domains, policies (47 ops) | 🟡 | Runtime policy enforcement and central rate limits remain ([ADMIN.md](../../docs/ADMIN.md)) |
| App runtime: Connect and native descriptors, modules, JWT, lifecycle, storage, webhooks, schedules, dynamic modules | 🟡 | Forge compute, workflow modules and Confluence macros remain ([APPS.md](../../docs/APPS.md)) |

## Browser tests

Playwright (Chromium) specs are in [`e2e/`](../../e2e). They cover:
- API smoke and the WASM worker.
- Offline reload and two-browser convergence.
- Create, triage, backlog, boards and timeline.
- Dashboards, reports and filters.
- Notifications and every scheme and admin page.
- Work types, priorities, resolutions and the work type hierarchy.
- Releases, plans, people and identity providers.
- Automation, apps, service management and Assets.
- Wiki pages, content tree and databases.
- WCAG 2.2 A/AA axe sweeps, target sizes, keyboard use and 320 px reflow.

## Load

See [loadtest.md](../../docs/loadtest.md).
- **Sync p95:** 6.8 ms at 10,000 work items and 89.6 ms at 100,000.
- **Index threshold:** a posting-list index is added above 300 ms. Current p95
  is about 3.3× under it.
