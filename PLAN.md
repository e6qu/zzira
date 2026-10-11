# ZZIRA delivery plan

ZZIRA reimplements Jira Cloud, Jira Software, Jira Service Management and
Confluence Cloud. Compatibility is partial. This plan tracks remaining delivery
work; shipped behavior and its evidence belong in the ledgers and feature docs.

Status and evidence: [CLOUD_PARITY.md](docs/CLOUD_PARITY.md) (product status),
[UI_PARITY.md](docs/UI_PARITY.md) (persona journeys),
[cloud-coverage.json](api/conformance/cloud-coverage.json) (per-operation
assessment), [docs index](docs/README.md).

## Target

The [compatibility ledger](docs/CLOUD_PARITY.md#contracts) owns the pinned
contract counts and current assessment. The operation inventory and coverage
files under `api/conformance/` are generated evidence; this plan owns remaining
work and its delivery order.

Surfaces without an OpenAPI document (Connect and Forge modules, Assets,
automation components, workflow rules, product UI) follow Atlassian's documented
and observed behavior. A missing specification is not a reason to skip a surface.

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

## Delivery order

1. Fix a broken persisted journey, data loss, authorization error or failing
   CI before adding scope. Keep recovery regressions in the owning browser spec.
2. Finish remaining configuration and authoring controls. Each enabled control
   must perform its action and retain refused input.
3. Finish the work item model and its dependent planning, release, dashboard
   and search behavior (sections 1–5).
4. Close identity, automation, Assets, service, knowledge and app gaps
   (sections 6–13), one complete command/API/browser slice at a time.
5. Extend local-first guarantees (section 14) and certify the complete contract
   and release matrix (section 15).

The sections below retain the compatibility scope. Closing a browser defect or
removing a stale claim does not certify an API operation. For each PR, name the
remaining item, verify it in code, ship its persisted outcome and evidence,
then remove that item after all CI checks pass and the PR is merged.

## 1. Work item model

**Hierarchy rollups.** Extend the epic report and epic burndown to summarise
work beneath a level above Epic ([REPORTS.md](docs/REPORTS.md)). Boards and
backlogs remain epic-level, as Jira's do.

**Metadata and configuration UI.**
- Team-managed work type scoping, asynchronous priority-scheme updates and a
  larger avatar catalogue remain ([ISSUE_METADATA.md](docs/ISSUE_METADATA.md#gaps)).
- Reproduce view screen field order and edit/transition screen tabs ([SCREEN_SCHEMES.md](docs/SCREEN_SCHEMES.md#gaps)).
- Add Attachment and Linked work items to the screen catalog.
- App field options: `projects2` scope, `defaultValue`, property indexes for JQL.
- Classification level on work items: field, REST value, UI control, JQL,
  project default, organization default; org admins only.

**Platform odds and ends.**
- Worklog visibility restrictions to a group or project role (requests for
  unsupported restrictions are currently refused).
- Bounce handling for the project sender address (the address itself is the
  From header of the mail a project's work queues, once its domain is
  verified).
- Anonymous browser filters and custom-field, worklog and history panels
  beyond public project/work item lists ([anonymous access](docs/ANONYMOUS_ACCESS.md)).
- `atlassian-addons-project-access` role for installed apps.
- Project templates: inline `REF` objects and team-managed projects.
- Development triggers beyond branch created: commits, pull requests, reviews,
  deployments.
- Compass components.

## 2. Plans

Owning reference: [JIRA_SOFTWARE.md](docs/JIRA_SOFTWARE.md#plans).

- Auto-scheduler: apply release constraints to estimated work and model flexible
  release dates. Unestimated work already falls back to live fixed release dates.
- Scenario edits: parent, rank, release, status, custom fields, create work items.
- Extend custom columns beyond text, numbers, dates, URLs, selects and labels.
- Atlassian Teams REST API.

## 3. Cross-project releases

Owning reference: [RELEASES.md](docs/RELEASES.md).

- Create the member versions from the plan (today they are linked, not created).
- Cross-project releases as auto-scheduler input.
- Release hub: deployments grouped across projects, release gates, environment
  promotion, drag reordering, dates in the viewer's locale and site time zone.

## 4. Boards, reports and DORA

- Dashboards: layout and favourites over REST, inline gadget item properties,
  Jira system gadgets.
- Load test at 1M actions with mixed reads and writes.

## 5. JQL and filters

- Additional documented field formats in `versionedRepresentations`; current
  values use format version `1`. Change history belongs to the changelog.
- Extend JQL value validation to projects, components, versions, users and custom
  field options ([JQL.md](docs/JQL.md#gaps)).
- Filters: a richer subscription schedule builder than two presets and a cron
  expression.

## 6. Enterprise identity

Owning references: [ADMIN.md](docs/ADMIN.md), [SCIM.md](docs/SCIM.md).

- SAML: single logout, encrypted assertions, signed authentication requests,
  and provider-initiated sign-in, which is refused today.
- SCIM provisioning of product access, and a directory-scoped provisioning key
  (users and groups are provisioned, and `scimManaged` reflects it).
- Authentication policies enforce single sign-on, two-step verification,
  session duration, the shortest password and per-policy membership; password
  expiry and the rest of Atlassian's strength rules are still missing, and a
  policy covers named people rather than a group.
- Two-step verification is an authenticator app or a security key
  ([ADMIN.md](docs/ADMIN.md)); a key is a second step rather than a way to
  sign in without a password, and there is no SMS.
- Managed accounts claimed through verified domains; domain ownership exclusive
  across organizations; `claimStatus` derived, not constant.
- User management API (`/users/{id}/manage/...`).
- Organization API keys for the admin API.
- Data security policies; data residency placement (or an explicit boundary).
- Several sites per organization on one server.
- Organization and site lifecycle, product access per site.

## 7. Automation

Owning reference: [AUTOMATION.md](docs/AUTOMATION.md#gaps).

- Connections: execute actions through stored connections (auth, secrets,
  rotation).
- Usage limits: per-site monthly execution quota, usage page, global vs
  single-project rule accounting, over-limit behavior.
- Confluence actions beyond creating, commenting on, labelling, appending to
  and archiving a page; triggers for spaces, attachments and custom content.
- Branches: remaining related-work hierarchy coverage. Nested branches are
  refused, matching Jira; they are not a missing feature.
- Remaining Jira action catalog, compared action by action with Atlassian's
  list: deleting comments and links, editing components, fix versions, work
  type and custom fields.

## 8. Assets

Owning reference: [SERVICE_MANAGEMENT.md](docs/SERVICE_MANAGEMENT.md#assets-api).

- Separate object types within a schema, inherited attributes, and reference,
  user, group, status and URL attribute types. Each schema is currently its
  own object type, with a parent type and object keys and labels.
- Assets REST API: icons on an object. Schemas, object types, attributes,
  objects, AQL search, imports, and an object's history, comments and files are
  served, and a schema is created and deleted through it.
- AQL in JQL (`aqlFunction()`). Assets search and portal fields already use
  AQL, including relationship traversal and `objectTypeAndChildren()`.
- Imports: JSON and object schema files, column mapping and schedules. A comma
  separated file of objects for one schema is imported from the page and over
  REST, and reconciled against the schema when it is the whole inventory.
- Per-schema roles. Agents already manage schemas, objects and relationships
  in the browser.

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
- Macro execution beyond the ones the site draws (panels, `status`, `code`,
  `toc` and page layouts): `expand`, `include`, `excerpt`, `children`,
  `jira`, charts and the rest; conversion context parameters.
- Content states: configurable suggested states, set and shown in the page view
  and editor; `state/available` honors space settings.
- Templates: modify and revert blueprints, app blueprints, `editor`/`view`/
  `export_view` bodies, `expand`, site template management UI.
- Custom content: app-registered types, attachments, `atlas_doc_format`, UI.
- Tasks: task lists, dates and the macros the editor does not draw (it draws
  panels, status, code and a table of contents) in the rich editor; task
  report; personal task list; due-date reminders.

**Spaces and search.**
- Import (Confluence XML, HTML, Markdown, Word) and export (XML full/custom, site,
  PDF, Word, CSV, per page) including hierarchy, comments, whiteboards, databases
  and custom content. A space this site exported reads back in with its page
  tree and blog posts ([CONFLUENCE_SPACES](docs/CONFLUENCE_SPACES.md#gaps)).
- Space icons; emptying the space trash 60
  days after a space lands there; `routeOverrideEnabled`, `contentMode` and
  themes take effect.
- Permissions: `export/space`, `restrict_content/space`, `archive/page`,
  anonymous and guest grants, content permission checks beyond pages;
  GUEST, ANONYMOUS and APP transition principals;
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

Owning reference: [CONFLUENCE_SITE_SURFACES.md](docs/CONFLUENCE_SITE_SURFACES.md#whiteboards).

- Direct manipulation beyond dragging and keyboard movement: resize, pan,
  zoom, multi-select and text editing on the canvas.
- Shapes library, freehand, images, frames and sections, grouping, alignment and
  snapping.
- Connectors with anchors, routing and arrowheads; automatic graph layout.
- Specialized template layout parity and translated labels beyond the native
  English starters.
- Voting, timer, sticky-to-work-item conversion, Smart Links.
- Comments, reactions, version history, live collaboration (see 12).
- Image and PDF export; accessible text equivalents.
- Databases: remaining views, formulas, relations, imports and exports.

## 12. Live collaboration

Owning reference: [CONFLUENCE_LIVE.md](docs/CONFLUENCE_LIVE.md).

- Server push (WebSocket or SSE) replacing polling.
- Structural ADF step merging; live titles; live never-published drafts.
- Live whiteboards and databases; live comments and reactions.
- Extend the page/blog convergence and disconnect tests to structural edits,
  never-published drafts, whiteboards and databases.

## 13. Apps

- Jira Connect modules: `dialogs`, `webSections`,
  `keyboardShortcuts`, workflow conditions, validators and post functions (stored
  today, not executed), `jiraSearchRequestViews`, `jiraBackgroundScripts`,
  `jiraProjectTabPanels`.
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

- Complete offline screen and field metadata for richer work-item edits.
- Reconcile refused commands’ optimistic field values with the authoritative
  work item, retaining the recoverable draft and other pending edits.
- Permission-shaped replicas and safe queued writes for service, knowledge,
  dashboards, releases, reports and administration.
- Conflict handling, idempotent replay, outbox ordering and cross-tab ownership when a replica ID is
  duplicated, reconnect, compaction,
  quota, account and site switching, schema upgrades, full resnapshot,
  revocation purge — tested for each replica.

## 15. Certification

- Move every pinned operation from partial to delivered with golden
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
- Run focused checks serially at low priority locally. Full builds, test gates
  and large evidence regeneration belong on GitHub runners; preserve local
  interactivity and stop if resource limits are reached.
- Wait for every head check to pass before merging. Squash with an explicit
  one-line subject of at most 80 characters and an empty body; no trailers.
- Adopt dependencies only after the 24-hour publication gate passes. PostgreSQL
  major upgrades need a separate data migration, not an image-only bump.
- Never expose internal ids to clients ([WIRE_IDS.md](docs/WIRE_IDS.md)).
- Update the owning doc, [CLOUD_PARITY.md](docs/CLOUD_PARITY.md) and this plan in
  the same PR; delete plan items when they ship.
