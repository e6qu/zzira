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

**Hierarchy rollups.** Levels above epic exist ([ISSUE_METADATA.md](docs/ISSUE_METADATA.md#work-type-hierarchy)), and plans, roadmaps and `hierarchyLevel` in JQL read through all of them. What remains:
- The epic report and epic burndown follow a level above the epic as well, summarising the work under everything beneath it ([REPORTS.md](docs/REPORTS.md)).
- Boards and backlogs treat the epic level as the top, as Jira's do; a board
  above the epic level would be ours, not Jira's.

**Metadata and configuration UI.**
- Per-language translations reach the metadata resources and the settings
  pages ([ISSUE_METADATA.md](docs/ISSUE_METADATA.md#translating-the-words-on-a-work-item)); the work item view, the board
  and search results still read the site's own names, as JQL does.
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
- Views: a saved zoom or column set. Work is grouped by team, sprint, project,
  status or assignee, filtered by key or summary, rolled up onto its parents,
  and kept under a name ([JIRA_SOFTWARE](docs/JIRA_SOFTWARE.md)).
- `inferredDates` and plan `customFields` take effect. (Capacity is read from velocity when nobody has typed one: [JIRA_SOFTWARE.md](docs/JIRA_SOFTWARE.md).)
- Atlassian Teams REST API.

## 3. Cross-project releases

Jira Plans create a cross-project release that groups same-named versions across
projects. ZZIRA stores `crossProjectReleases` on a plan and returns it over REST,
and the release hub reads it back; nothing plans with it yet.

- Create the member versions from the plan (today they are linked, not created).
- Cross-project releases as auto-scheduler input. The release hub reads them
  back: a version says what ships with it, and the version page lists the other
  projects' versions with their dates, progress and release state
  ([RELEASES.md](docs/RELEASES.md)).
- Release hub: deployments grouped across projects, release gates, environment
  promotion, drag reordering, dates in the viewer's locale and site time zone.

## 4. Boards, reports and DORA

- Dashboards: layout and favourites over REST, inline gadget item properties,
  Jira system gadgets.
- Load test at 1M actions with mixed reads and writes.

## 5. JQL and filters

- Fields: JSM's `Organizations`, which needs a request to be shared with one.
- `fields.lastViewed` on a work item read, which needs the reader threaded
  through the bean.
- Historical `versionedRepresentations`.
- Filters: a richer subscription schedule builder than two presets and a cron
  expression.

## 6. Enterprise identity

Atlassian Guard and organization administration. ZZIRA has OIDC sign-in (Google,
Entra, Atlassian, custom), DNS domain verification, IP allowlists, SCIM user and
group provisioning, authentication policies, and managed profile edit, suspend,
restore and remove.

- SAML single sign-on with multiple identity providers.
- SCIM provisioning of product access, and a directory-scoped provisioning key
  (users and groups are provisioned, and `scimManaged` reflects it).
- Authentication policies enforce single sign-on, two-step verification,
  session duration, the shortest password and per-policy membership; password
  expiry and the rest of Atlassian's strength rules are still missing, and a
  policy covers named people rather than a group.
- Two-step verification is an authenticator app alone: no WebAuthn, no
  passkeys. The setup link is drawn as a QR code and shown as a key.
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

- Work deleted, work moved, versions, sprints, work entering or leaving a
  sprint, and wiki content are covered.
- Connections: execute actions through stored connections (auth, secrets,
  rotation).
- Usage limits: per-site monthly execution quota, usage page, global vs
  single-project rule accounting, over-limit behavior.
- Confluence actions beyond creating, commenting on and labelling a page.
  Triggers for pages created, updated, commented and labelled, and for blog
  posts, are done.
- Branches: related work items at any level, and branches inside branches.
  JQL branches, branching over each item in a list, over the work a rule
  created, and several branches in one rule are done.
- Web request custom bodies and headers, the manual trigger with its questions
  in the rule editor and on the work item, and variables (create variable
  action, `{{variable}}`) are done.
- Running a manual rule over a selection of work items from the navigator.
- Remaining Jira action catalog, compared action by action with Atlassian's
  list: watchers, cloning, deleting comments and links. Looking work items up
  and keeping what was found is done.

## 8. Assets

Jira Service Management Assets: schemas, object types with a hierarchy, typed
attributes, objects, references, AQL, imports and a REST API. ZZIRA has per-desk
schemas with inline attributes, objects, directed relationships, request impact
links, the Assets portal field, the workspace discovery endpoints, an import of
objects from a file, and an Assets REST API under the Assets workspace
([SERVICE_MANAGEMENT](docs/SERVICE_MANAGEMENT.md#assets-api)).

- Object schemas, object types with inheritance, typed attributes (including
  reference, user, group, status, date, URL), object keys and labels.
- Assets REST API: attachments, comments and icons on an object. Schemas,
  object types, attributes, objects, AQL search, imports and an object's
  history are served, and a schema is created and deleted through it.
- AQL parser and evaluator; `aqlFunction()` in JQL; AQL-filtered Assets fields.
- Imports: JSON and object schema files, column mapping and schedules. A comma
  separated file of objects for one schema is imported from the page and over
  REST, and reconciled against the schema when it is the whole inventory.
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
  and custom content. A space this site exported reads back in with its page
  tree and blog posts ([CONFLUENCE_SPACES](docs/CONFLUENCE_SPACES.md#gaps)).
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
