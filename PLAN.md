# ZZIRA delivery plan

ZZIRA is a self-hosted Jira Cloud, Jira Software, Jira Service Management and
Confluence Cloud. The UI and API behave as Atlassian's do; the architecture is
Linear's: one command layer writing an immutable action log, and permission-shaped
browser replicas synced from it. This plan lists every piece of Atlassian behavior
still missing and the order to build it in.

Status and evidence: [CLOUD_PARITY.md](docs/CLOUD_PARITY.md) (product status),
[UI_PARITY.md](docs/UI_PARITY.md) (persona journeys),
[cloud-coverage.json](api/conformance/cloud-coverage.json) (per-operation
assessment), [docs index](docs/README.md).

## Target

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

All 1,207 are served and assessed as partial; none is missing. Surfaces without an
OpenAPI document (Connect and Forge modules, Assets, automation components,
workflow rules, product UI) follow Atlassian's documented and observed behavior.
A missing specification is not a reason to skip a surface.

Out of scope, reported as explicit errors: Atlassian billing, Atlassian's AI
models, and Atlassian-hosted Forge compute. Clients that hardcode
`api.atlassian.com` or `auth.atlassian.com` need an endpoint override.

## Done means

- API behavior matches Atlassian's: paths, bodies, status codes, errors, paging,
  expansions, ids, permissions, concurrency.
- The browser journey reaches a persisted outcome and has a Playwright spec.
- REST, UI, automation, apps, search, reports, exports and replicas go through
  the same command and permission checks.
- State and its action/audit record commit in one transaction.
- Background work is durable, leased, retryable, idempotent and restart-safe.
- Search, sync, reports, exports and notifications filter by access before
  anything leaves the server.
- Migrations run on a clean database and on every prior schema.
- Go suite, `go vet`, lint, wasm build, e2e, security scans and conformance
  checks pass; docs and ledgers change in the same PR.

## Order

1. [Work item model](#1-work-item-model) — admin surfaces and the remaining field families.
2. In parallel after 1: [Plans](#2-plans), [cross-project releases](#3-cross-project-releases),
   [boards, reports and DORA](#4-boards-reports-and-dora), [JQL and filters](#5-jql-and-filters).
3. In parallel, independent of 1: [enterprise identity](#6-enterprise-identity),
   [automation](#7-automation), [Assets](#8-assets), [Service Management](#9-service-management),
   [Confluence](#10-confluence), [diagrams](#11-whiteboards-and-diagrams),
   [live collaboration](#12-live-collaboration), [apps](#13-apps).
4. [Local-first closure](#14-local-first-closure), then [certification](#15-certification).

Each numbered item is one or more substantial PRs.

## 1. Work item model

**Hierarchy rollups.** Levels above epic exist ([ISSUE_METADATA.md](docs/ISSUE_METADATA.md#work-type-hierarchy)). What remains:
- Plans, roadmaps and reports roll up through every level.
- Boards and backlogs still treat the epic level as the top.
- `hierarchyLevel` in JQL (see [JQL and filters](#5-jql-and-filters)).

**Metadata and configuration UI.**
- Per-language field translations.
- The screen catalog includes Reporter, Environment, Attachment and Linked
  work items; `projectKey` honored on tab-field reads.
- Issue security scheme form sends the level mapping for projects with secured
  work.
- Custom field contexts: a global context coexists with project contexts that
  override it.
- Custom field options: delete, replace on work items, reorder anywhere.
- App field options: `projects2` scope, `defaultValue`, property indexes for JQL.
- Store `renderer` / `rendererType` on field configuration items.
- `alternatives` for work types narrows by shared workflow, field configuration
  and screen schemes; team-managed `scope` and `entityId`; priority scheme update
  returns its `task`.
- Classification level on work items: field, REST value, UI control, JQL,
  project default, organization default; org admins only.

**Platform odds and ends.**
- Navigator bulk edit reaches only the six fields it offers; the REST endpoint
  takes any field on the shared edit metadata, including custom fields.
- Worklogs: `started`, real `updated`/`updateAuthor`, `issueId`, `self` with the
  work item; list paging and `startedAfter`/`startedBefore`/`expand=properties`;
  worklog visibility.
- Bounce handling for the project sender address (the address itself is the
  From header of the mail a project's work queues, once its domain is
  verified).
- Anonymous browsing in the UI (`/browse/{key}`), not only REST.
- `atlassian-addons-project-access` role for installed apps.
- Project templates: inline `REF` objects and team-managed projects.
- Development triggers beyond branch created: commits, pull requests, reviews,
  deployments.
- Compass components.

## 2. Plans

Jira Plans (Advanced Roadmaps): cross-project plans over boards, projects and
filters, with teams, capacity, scenarios and an auto-scheduler. ZZIRA has plan
REST CRUD, sources, exclusions, scenarios with review changes, teams, capacity
and dependency checks.

- Auto-scheduler: schedule by rank, dependencies, team capacity, sprints and
  releases; preview and apply to a scenario.
- Plan setup in the browser: a date custom field as a plan's date, and plan
  custom fields (create, scheduling, sources, exclusions, permissions, teams
  and cross-project releases are there).
- Restore archived and trashed plans; duplicate copies scenarios.
- Scenario edits: parent, rank, release, status, create work items.
- Views: saved views, grouping, filters, rollups across the hierarchy.
- Capacity from velocity; `inferredDates` and plan `customFields` take effect.
- Atlassian Teams REST API.

## 3. Cross-project releases

Jira Plans create a cross-project release that groups same-named versions across
projects. ZZIRA stores `crossProjectReleases` on a plan and returns it over REST,
but nothing shows or uses it.

- Create the member versions from the plan (today they are linked, not created).
- Cross-project releases as auto-scheduler input, and on the release hub.
- Release hub: deployments grouped across projects, release gates, environment
  promotion, drag reordering, dates in the viewer's locale and site time zone.

## 4. Boards, reports and DORA

- Reports: deployment frequency and cycle time as pages of their own.
- DORA: which work items count as incidents (environments and pipelines are
  configured per project), and excluded periods.
- Dashboards: layout and favourites over REST, inline gadget item properties,
  Jira system gadgets.
- Load test at 1M actions with mixed reads and writes.

## 5. JQL and filters

- Fields: JSM's `Organizations`, which needs a request to be shared with one.
- `fields.lastViewed` on a work item read, which needs the reader threaded
  through the bean.
- Historical `versionedRepresentations`.
- Unknown values (`status = Nope`) return Jira's error instead of matching nothing.
- Filters: subscriptions by any viewer with permission, group subscriptions,
  `MANAGE_GROUP_FILTER_SUBSCRIPTIONS`, opt-in for empty results,
  `CREATE_SHARED_OBJECTS` on sharing.

## 6. Enterprise identity

Atlassian Guard and organization administration. ZZIRA has OIDC sign-in (Google,
Entra, Atlassian, custom), DNS domain verification, IP allowlists, and managed
profile edit, suspend, restore and remove.

- SAML single sign-on with multiple identity providers.
- SCIM 2.0 user and group provisioning (`scimManaged` reflects it).
- Authentication policies: enforced SSO, two-step verification enrolment and
  enforcement, password rules, session duration, per-policy membership.
- Managed accounts claimed through verified domains; domain ownership exclusive
  across organizations; `claimStatus` derived, not constant.
- User management API (`/users/{id}/manage/...`).
- Organization API keys for the admin API.
- Data security policies; data residency placement (or an explicit boundary).
- Several sites per organization on one server.
- Organization and site lifecycle, product access per site.

## 7. Automation

ZZIRA runs the event, scheduled, manual and incoming-webhook triggers, the
condition catalog, and 14 action types including web requests and page creation.

- Connections: execute actions through stored connections (auth, secrets,
  rotation).
- Usage limits: per-site monthly execution quota, usage page, global vs
  single-project rule accounting, over-limit behavior.
- Confluence triggers: page created, updated, published, commented, labelled;
  Confluence actions beyond create page.
- Branches: JQL, for-each on lists, related work items at any level.
- Variables (create variable action, `{{variable}}`), web request custom bodies
  and headers, manual trigger in the rule editor.
- Remaining Jira action catalog, compared action by action with Atlassian's list.

## 8. Assets

Jira Service Management Assets: schemas, object types with a hierarchy, typed
attributes, objects, references, AQL, imports and a REST API. ZZIRA has per-desk
schemas with inline attributes, objects, directed relationships, request impact
links, the Assets portal field, and the workspace discovery endpoints.

- Object schemas, object types with inheritance, typed attributes (including
  reference, user, group, status, date, URL), object keys and labels.
- Assets REST API: schemas, object types, attributes, objects, AQL search,
  history, attachments, comments, icons.
- AQL parser and evaluator; `aqlFunction()` in JQL; AQL-filtered Assets fields.
- Imports (CSV, JSON, object schema) with mapping, reconciliation and schedules.
- Agent browser UI for schemas and objects; per-schema roles.

## 9. Service Management

- Request type restrictions (`RESTRICTED` returns the permitted people).
- Email channel: incoming mail creates and comments on requests.
- Customer notification email templates.
- Knowledge base ranking and analytics.
- Post-incident review templates.

## 10. Confluence

**Content.**
- Blog posts: version restore and delete, historical macro reads, content states,
  relations.
- Macro execution (`info`, `toc`, `code`, …); conversion context parameters.
- Content states: configurable suggested states, set and shown in the page view
  and editor; `state/available` honors space settings.
- Templates: modify and revert blueprints, app blueprints, `editor`/`view`/
  `export_view` bodies, `expand`, site template management UI.
- Custom content: app-registered types, attachments, `atlas_doc_format`, UI.
- Tasks: task lists, dates and macros in the rich editor; task report; personal
  task list; due-date reminders.

**Spaces and search.**
- Import (Confluence XML, HTML, Markdown, Word) and export (XML full/custom, site,
  PDF, Word, CSV, per page) including hierarchy, comments, whiteboards, databases
  and custom content.
- Space icons, browser rename and description edit; emptying the space trash 60
  days after a space lands there; `routeOverrideEnabled`, `contentMode` and
  themes take effect.
- Permissions: `export/space`, `restrict_content/space`, `archive/page`,
  anonymous and guest grants, content permission checks beyond pages, browser UI
  for grants and custom roles; GUEST, ANONYMOUS and APP transition principals;
  transition UI.
- Look and feel: site-wide theme, admin UI, app themes.
- CQL: relevance and stemming; whiteboards, databases, folders, Smart Links and
  custom content; `space.title`, `space.category`, content properties; a search
  page.
- Notifications: per-user email settings and digests, likes, Share, watcher
  notifications for move, delete, archive and new attachments.
- Users and groups: `expand` on user reads, `user/bulk` paging, full CQL user
  search; `invite-by-email` sends mail.
- Audit: site operations write records; `sysAdmin`/`superAdmin` derived; audit
  UI. Organization admin key; data security policies.

## 11. Whiteboards and diagrams

ZZIRA has a form-driven canvas with stickies, text and shapes, labelled
connectors, a fixed 1400×800 SVG render, and whiteboard lifecycle in v2.

- Direct manipulation: drag, resize, pan, zoom, multi-select, keyboard editing.
- Shapes library, freehand, images, frames and sections, grouping, alignment and
  snapping.
- Connectors with anchors, routing and arrowheads; automatic graph layout.
- Templates that add content (53 keys are accepted today and add nothing).
- Voting, timer, sticky-to-work-item conversion, Smart Links.
- Comments, reactions, version history, live collaboration (see 12).
- Image and PDF export; accessible text equivalents.
- Databases: remaining views, formulas, relations, imports and exports.

## 12. Live collaboration

ZZIRA polls presence (15 s) and merges page bodies (1 s), with named carets and
offline typing.

- Server push (WebSocket or SSE) replacing polling.
- Structural ADF step merging; live titles; live never-published drafts.
- Live whiteboards and databases; live comments and reactions.
- Multi-user convergence tests under latency and disconnects.

## 13. Apps

- Jira Connect modules: `configurePage`, `dialogs`, `webSections`,
  `keyboardShortcuts`, workflow conditions, validators and post functions (stored
  today, not executed), `jiraSearchRequestViews`, `jiraBackgroundScripts`,
  `profilePages`, `jiraProjectTabPanels`.
- Confluence Connect modules: dynamic and static content macros, `customContent`,
  `blueprints`, `spaceToolsTabs`, `confluenceContentProperties`.
- Admin pages: custom locations, `cacheable`, `fullPage`, icons.
- Remaining conditions, content-presence conditions, issue context change events,
  read-only issue fields, dynamic webhook `conditions`/`propertyKeys`/
  `excludeBody`, descriptor-driven upgrade migrations.
- Forge: manifest, UI Kit and Custom UI rendering, Forge modules, with hosted
  compute as an explicit boundary.
- DevOps: asynchronous provider processing; issue view panels for operations,
  security, feature flags and remote links.

## 14. Local-first closure

- Permission-shaped replicas and safe queued writes for service, knowledge,
  dashboards, releases, reports and administration.
- Conflict handling, outbox ordering, cross-tab ownership, reconnect, compaction,
  quota, account and site switching, schema upgrades, full resnapshot,
  revocation purge — tested for each replica.

## 15. Certification

- Move all 1,207 operations from partial to delivered with golden
  request/response evidence.
- An executable conformance harness that runs every operation against a server.
- Go, TypeScript and Python client fixtures with only the base URL changed;
  named third-party clients and apps.
- Fresh install, upgrade and rollback, backup and restore, worker failure,
  tenant isolation, accessibility, browsers, responsive layouts, localization,
  time zones, scale and performance budgets.
- Release evidence, supported-version matrix and operator runbook.

## Working rules

- Verify a gap in the code before building; docs have been wrong in both
  directions.
- Substantial PRs; keep the PR title and body current.
- Push once it builds; run the Go suite and e2e locally while CI runs.
- Never expose internal ids to clients ([WIRE_IDS.md](docs/WIRE_IDS.md)).
- Update the owning doc, [CLOUD_PARITY.md](docs/CLOUD_PARITY.md) and this plan in
  the same PR; delete plan items when they ship.
