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

1. [Permission enforcement](#1-permission-enforcement) — correctness; blocks everything that trusts the command layer.
2. [Work item model](#2-work-item-model) — hierarchy above epic unblocks Plans and releases.
3. In parallel after 2: [Plans](#3-plans), [cross-project releases](#4-cross-project-releases),
   [boards, reports and DORA](#5-boards-reports-and-dora), [JQL and filters](#6-jql-and-filters).
4. In parallel, independent of 2: [enterprise identity](#7-enterprise-identity),
   [automation](#8-automation), [Assets](#9-assets), [Service Management](#10-service-management),
   [Confluence](#11-confluence), [diagrams](#12-whiteboards-and-diagrams),
   [live collaboration](#13-live-collaboration), [apps](#14-apps).
5. [Local-first closure](#15-local-first-closure), then [certification](#16-certification).

Each numbered item is one or more substantial PRs.

## 1. Permission enforcement

Jira checks the project's permission scheme on every work item action. ZZIRA
checks Browse projects, issue security, Delete, attachments, comments and
worklogs in the command layer; the rest are checked only on some API paths.

- Check in `internal/commands`, for every caller: Create issues, Edit issues,
  Transition issues, Assign issues, Assignable user (direct assignment), Resolve
  issues, Close issues, Schedule issues (due date), Modify reporter, Move issues,
  Set issue security, Link issues, Manage watchers, Manage sprints, Service
  desk agent.
- `system:check-permission-validator` evaluates the project's scheme, not the
  fixed role map in `internal/authz/jira_permissions.go`.
- Navigator bulk actions check Bulk change, not site admin (`internal/web/web.go`).
- Bulk issue property updates evaluate the Jira `expression` filter.
- Tests: a member without each permission is refused on REST, UI, bulk,
  automation (run-as) and offline replay.

## 2. Work item model

**Hierarchy above epic.** Jira Premium lets admins add levels above epic
(Initiative, Theme…) in Settings → Work type hierarchy; the Parent field links any
level to the one above.
- Drop the `hierarchy_level BETWEEN -1 AND 1` check; store configurable levels.
- Hierarchy settings page; `GET /project/{id}/hierarchy` returns real levels.
- Parent field, `parent` JQL, `hierarchyLevel` JQL, issue view and create form
  across levels; board and backlog epic handling stays at level 1.
- Plans, roadmaps and reports roll up through every level.

**Metadata and configuration UI.**
- Admin pages for work types, work type schemes, priorities, priority schemes,
  resolutions, and custom fields (create, rename, trash, restore, delete,
  translations).
- Resolution chosen on transition screens and editable; not always the default.
- Transition screens reference a Screen, not a field list; forms render screen
  tabs; the screen catalog includes Reporter, Environment, Attachment, Linked
  work items and Resolution; `projectKey` honored on tab-field reads.
- Copy for permission, notification, issue security, screen, screen and work type
  screen schemes.
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
- Bulk edit of `issueType` and `status`; bulk edit, watch and unwatch in the
  navigator.
- Worklogs: `started`, real `updated`/`updateAuthor`, `issueId`, `self` with the
  work item; list paging and `startedAfter`/`startedBefore`/`expand=properties`;
  worklog visibility.
- Project sender address used by the mailer, with bounce handling.
- Anonymous browsing in the UI (`/browse/{key}`), not only REST.
- `atlassian-addons-project-access` role for installed apps.
- Project templates: inline `REF` objects and team-managed projects.
- Development triggers beyond branch created: commits, pull requests, reviews,
  deployments.
- Compass components.

## 3. Plans

Jira Plans (Advanced Roadmaps): cross-project plans over boards, projects and
filters, with teams, capacity, scenarios and an auto-scheduler. ZZIRA has plan
REST CRUD, sources, exclusions, scenarios with review changes, teams, capacity
and dependency checks.

- Auto-scheduler: schedule by rank, dependencies, team capacity, sprints and
  releases; preview and apply to a scenario.
- Plan setup in the browser: create, sources, exclusions, permissions, teams.
- Restore archived and trashed plans; duplicate copies scenarios.
- Scenario edits: parent, rank, release, status, create work items.
- Views: saved views, grouping, filters, rollups across the hierarchy.
- Capacity from velocity; `inferredDates` and plan `customFields` take effect.
- Atlassian Teams REST API.
- Fix sprint length: days in `store/plans.go`, weeks in `store/plan_planning.go`.

## 4. Cross-project releases

Jira Plans create a cross-project release that groups same-named versions across
projects. ZZIRA stores `crossProjectReleases` on a plan and returns it over REST,
but nothing shows or uses it.

- Create, rename and delete cross-project releases in a plan; create or link the
  member versions.
- Release view across projects: progress, dates, warnings, auto-scheduler input.
- Release hub: deployments grouped across projects, release gates, environment
  promotion, drag reordering, dates in the viewer's locale and site time zone.

## 5. Boards, reports and DORA

- Boards: column configuration with several statuses per column, swimlanes by
  query, epic, project and stories; `PUT …/features`; changing a board's filter
  and estimation field.
- Reports: release burndown, epic burndown, user and version workload, time
  tracking, single-level group-by, deployment frequency, cycle time.
- DORA: configurable mappings (which environments, pipelines and incident types
  count) and excluded periods.
- Dashboards: layout and favourites over REST, inline gadget item properties,
  Jira system gadgets.
- Load test at 1M actions with mixed reads and writes.

## 6. JQL and filters

- Fields: `text`, `comment`, `watcher(s)`, `voter`/`votes`, `attachments`,
  `level`, `lastViewed`, `issueLinkType`, `category`, `filter`,
  `statusCategoryChangedDate`, `hierarchyLevel`, `Request participants`,
  `Organizations`, `request-channel-type`.
- Aliases: `issuekey`, `type`, `timeoriginalestimate`, `timeestimate`.
- Functions: `issueHistory()`, `issuesWithRemoteLinksByGlobalId()`.
- `WAS`/`CHANGED` on `resolution`; `includeArchivedProjects`; historical
  `versionedRepresentations`.
- Unknown values (`status = Nope`) return Jira's error instead of matching nothing.
- Filters: subscriptions by any viewer with permission, group subscriptions,
  `MANAGE_GROUP_FILTER_SUBSCRIPTIONS`, opt-in for empty results,
  `CREATE_SHARED_OBJECTS` on sharing.

## 7. Enterprise identity

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
- Self-service API tokens on the profile page, with expiry and revocation.
- Organization API keys for the admin API.
- Data security policies; data residency placement (or an explicit boundary).
- Several sites per organization on one server.
- Organization and site lifecycle, product access per site.

## 8. Automation

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

## 9. Assets

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

## 10. Service Management

- Request type restrictions (`RESTRICTED` returns the permitted people).
- Create, edit and delete request types in the agent UI.
- Email channel: incoming mail creates and comments on requests.
- Customer notification email templates.
- Knowledge base ranking and analytics.
- Post-incident review templates.

## 11. Confluence

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
- Space trash and restore, icons, browser rename, description edit and delete;
  `routeOverrideEnabled`, `contentMode` and themes take effect.
- Permissions: `export/space`, `restrict_content/space`, `archive/page`,
  anonymous and guest grants, content permission checks beyond pages, browser UI
  for grants and custom roles; GUEST, ANONYMOUS and APP transition principals;
  transition UI.
- Look and feel: site-wide theme, admin UI, app themes.
- CQL: relevance and stemming; whiteboards, databases, folders, Smart Links and
  custom content; `space.title`, `space.category`, content properties; a search
  page.
- Notifications: autowatch, per-user email settings and digests, likes, Share,
  watcher notifications for move, delete, archive and new attachments.
- Users and groups: `expand` on user reads, `user/bulk` paging, full CQL user
  search; `invite-by-email` sends mail.
- Audit: site operations write records; `sysAdmin`/`superAdmin` derived; audit
  UI. Organization admin key; data security policies.

## 12. Whiteboards and diagrams

ZZIRA has a form-driven canvas with stickies, text and shapes, labelled
connectors, a fixed 1400×800 SVG render, and whiteboard lifecycle in v2.

- Direct manipulation: drag, resize, pan, zoom, multi-select, keyboard editing.
- Shapes library, freehand, images, frames and sections, grouping, alignment and
  snapping.
- Connectors with anchors, routing and arrowheads; automatic graph layout.
- Templates that add content (53 keys are accepted today and add nothing).
- Voting, timer, sticky-to-work-item conversion, Smart Links.
- Comments, reactions, version history, live collaboration (see 13).
- Image and PDF export; accessible text equivalents.
- Databases: remaining views, formulas, relations, imports and exports.

## 13. Live collaboration

ZZIRA polls presence (15 s) and merges page bodies (1 s), with named carets and
offline typing.

- Server push (WebSocket or SSE) replacing polling.
- Structural ADF step merging; live titles; live never-published drafts.
- Live whiteboards and databases; live comments and reactions.
- Multi-user convergence tests under latency and disconnects.

## 14. Apps

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

## 15. Local-first closure

- Permission-shaped replicas and safe queued writes for service, knowledge,
  dashboards, releases, reports and administration.
- Conflict handling, outbox ordering, cross-tab ownership, reconnect, compaction,
  quota, account and site switching, schema upgrades, full resnapshot,
  revocation purge — tested for each replica.

## 16. Certification

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
