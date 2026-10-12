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
5. Extend local-first guarantees (section 14), close the CRUD, admin UI and
   RBAC gaps (section 15), then certify the complete contract and release
   matrix (section 16).

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
- Reproduce view screen field order ([SCREEN_SCHEMES.md](docs/SCREEN_SCHEMES.md#gaps)).
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

## 15. CRUD, admin UI and RBAC completeness

Every resource Jira, Confluence, Service Management and the admin site create,
change and delete must be reachable through both the API and the browser, with
the same permission checks on each. This section tracks what a probe of the
running server found; extend it rather than restate it.

Verified (site admin, full create/update/delete): projects, fields, screens,
screen schemes, priorities, work types, permission schemes, notification
schemes, issue security schemes, field configurations, workflows
(`POST /rest/api/3/workflows/create`, `DELETE /rest/api/3/workflow/{id}`) and
workflow schemes; Confluence spaces and v2 pages; Service Management
organizations, customers and request types. A plain member is refused on each,
in the browser and over REST. The workflow, status and workflow scheme
directories admit site administrators and project administrators, who read the
configuration their own projects follow, and refuse everyone else.

Probe method: resolve every write operation in
[cloud-operations.json](api/conformance/cloud-operations.json) against a running
server as a site admin and as a plain member, then check the browser's admin
pages for the same resources. All 608 write operations are routed, and none of
the 149 Confluence writes answer "not implemented"; `POST /wiki/rest/api/content`
does, and is meant to -- Atlassian's contract no longer carries the v1 content
writes, only `/wiki/api/v2/pages`.

Error statuses are done. Jira does not report every missing resource as 404:
each operation publishes the statuses it can answer, and where 404 is absent
while 400 is present, a resource it cannot find leaves the request invalid and
400 is the answer. Deleting is the clearest case -- `DELETE /dashboard/{id}`
and the permission scheme grant writes report 400 for ids whose reads report
404 -- and no priority scheme operation publishes 404 at all. A paginated
listing that publishes only success for the id it is given answers an empty
page instead, which `/field/{fieldId}/screens` already did and
`/field/{fieldId}/contexts` and `/field/{fieldId}/context/issuetypemapping` now
do. A path parameter outside its enumerated set addresses no resource, so the
application key answers 404 like the system avatar types. Fourteen operations
were corrected and
[error_status_test.go](internal/api3/error_status_test.go) pins each one
against the statuses the pinned specification publishes for it, so an
expectation cannot drift from the contract. Error bodies themselves are
faithful: the specification's own examples carry `errorMessages` alone and
`ErrorCollection` requires no property, so omitting an empty `errors` is
right. Two operations keep 404 deliberately, publishing neither 400 nor 404:
reading an issue security scheme, where every other operation on the same id
publishes 404.

Expansions are done. 64 operations publish an `expand` value; 61 of them
answer at all (two are Forge-only and one needs a bounded query), and every
value they publish was called against a site carrying the demo company, with
and without the expansion. 170 values were probed and 152 changed the
response. Each of the 15 that did not was then explained rather than guessed
at: three are faithful, because the contract says `GET /project/{id}` carries
the description, work types and lead in every response; twelve were artefacts
of a site with nothing to expand, retired by creating the data -- a permission
scheme holding user and group grants, a version with a driver, a filter shared
with everyone and subscribed to -- since an expansion that fills a list cannot
be told from one that is ignored until the list has rows.

Three defects came out of it. The bulk read by worklog id ignored
`expand=properties` outright; the change feeds carried an empty `properties`
on every row whether or not it was asked for, and never filled it; and a
published filter subscription carried this product's schedule rather than the
subscriber the contract publishes, on a schema that refuses any other
property.

The sweep also caught a search defect on the way to the filters it expands:
`filterName` is documented as a case-insensitive partial match, and this
product required the whole name exactly. It now matches the query's words in
any order, and `isSubstringMatch=true` asks for one contiguous run, which is
the distinction the two parameters describe.
[expansion_test.go](internal/api3/expansion_test.go) pins the worklog
expansions, the collapsed-then-filled filter lists, the subscription shape
against the properties the contract publishes for it, and the name search.

Conflicts are checked. 47 operations publish a 409, and they describe three
kinds of collision. The conflicts a caller can provoke on purpose all answer
correctly, each one built and checked in
[conflict_test.go](internal/api3/conflict_test.go): a work type, work type
scheme, project category or project role name already taken; a work type
renamed onto a taken name; a work type replaced with itself; a field
association scheme a project follows; and a project role somebody holds, which
goes once a replacement is named. No defect came out of this group.

The second kind is a background task still running, which this product cannot
reach the way Jira does because it finishes that work before it answers --
`DELETE /field/{id}` already reports a task it has completed. The guard itself
is real and is queried per request, so the test builds the collision the way a
real one would leave it, with a task of the same kind still enqueued against
the same item, and the deletion answers 409.

The third kind turns on sub-tasks being disabled for the site, and all four of
those operations were wrong. The site has had a sub-tasks switch all along, on
`/admin` and in `GET /configuration`, and work item creation honours it -- but
`POST /field/{fieldId}/context`,
`PUT /field/{fieldId}/context/{contextId}/issuetype`,
`POST /issuetypescreenscheme` and `PUT /issuetypescreenscheme/{id}/mapping`
all accepted a sub-task work type on a site with sub-tasks off. They answer
409 now, from one store reader the four share, and go through unchanged when
sub-tasks are on. Finding this needed a context scoped to a project: a global
context already exists for a new field, so the one-global-context conflict
answers first and reads as if the sub-task rule were working.

Concurrent work item writes are refused rather than silently won.
`PUT /rest/api/3/issue/{issueIdOrKey}` and
`POST /rest/api/3/issue/{issueIdOrKey}/transitions` publish 409 for an update
that conflicts with another, and nothing in the write path detected a lost
update: two concurrent edits both succeeded and the last one won. The store
already held the precondition the transitions used -- an expected
`updated_seq` compared under the row lock -- so the edit path carries it too,
from the work item the request read, and the refusal is a sentinel both
operations report as 409 through one mapping.

The case that actually happens is two people in the edit dialog minutes
apart, each about to write every field back. The dialog carries the work item
as it was opened, a stale save is refused with the same conflict, and
[concurrent_edit.spec.ts](e2e/concurrent_edit.spec.ts) walks it: their change
stands, the refused save writes nothing, and reopening the work item lets the
edit through. Writes this product makes for itself -- automation, bulk
operations, migrations -- name no expectation and apply to the work item as it
stands, so none of them became conflict-prone.

Response shapes are swept. A schema that sets `additionalProperties` to false
makes a validating client reject an answer carrying anything else, so every
answer whose schema refuses extras was compared against the properties that
operation publishes. Six were wrong and are fixed: creating a screen scheme
answered the whole scheme where the operation publishes its id; creating a
field association scheme added the field count and default flag that belong
to reading one; `GET /configuration` advertised the site's own parallel
sprints switch among Jira's features; the field search carried the clause
names and searcher flags that belong to a read of the field; the filter
search carried a `sharedUsers` list it does not offer and omitted the
documented `expand`; and the work type screen scheme assignments answered one
project per row instead of each scheme with the projects it governs.

Two more answers look identical to those and are correct: `GET
/fieldconfiguration` and `POST /field` carry more than their linked schema
lists, but the specification's own examples show those properties, so the
schema reference is narrower than the published payload. Examples settle what
schemas alone cannot, and trusting the schema would have broken both.

Write persistence is swept, and found a paging defect instead. Every field a
create or an update accepts came back on 15 resources plus comments,
worklogs, remote links, the banner, the time tracking options, screens,
screen schemes, both work type schemes, field configurations, the project and
a person's own preferences -- nothing was dropped. Two apparent misses were
not: `Comment.jsdPublic` is `readOnly` in the contract, which says to use the
Service Management API to raise an internal comment, and a version's `driver`
comes back only when expanded.

What the sweep found instead is that 28 paged collections refused a page
larger than they serve, with 400. The contract documents no failure for
asking too much, and `GET /project/search` says outright that "if a value
greater than 100 is provided, the maxResults parameter will default to 100".
They all cap now and report the size they served, which is what a handful of
collections already did; a negative or unparseable page size is still
refused. [page_size_test.go](internal/api3/page_size_test.go) reads the paged
collections out of the pinned specification and asks each for ten thousand
rows, so a collection added later is swept without anybody remembering to
add it.

Remaining:
- Jira's two remaining settings surfaces are features, not pages: languages
  needs the interface translated, and issue collectors need a public
  embeddable widget. Neither is a form over data this product already keeps.
- Whether a write persists every field it accepted, beyond the resources
  already sampled.

Checked and found already present, so that nothing re-opens them: the
organization admin journeys are on `/admin` and work in the browser -- a
domain is added and a policy created there, each landing in the database --
even though their REST counterparts need a bearer organization API key.
`/admin` also carries the advanced settings list, one form per application
property, beside the curated `/settings/look-and-feel` page, which is the pair
Jira has. Link types, look and feel and shared items landed at
`/settings/issue-linking`, `/settings/look-and-feel` and
`/settings/shared-items`.

Measured so far: every one of the 1207 pinned operations is routed (599 reads,
608 writes, none answering "not implemented"); the paged collections checked
honour `maxResults`, echo `startAt`, report `total` and `isLast`, and advance
their rows; and a create followed by a read returns every field it was given
for components, versions, filters, dashboards, project categories and link
types. Dashboard create requires `editPermissions`, as the contract says.

Two sweeps are done; neither is worth repeating as-is. Diffing live responses
against the pinned schemas found the status `iconUrl` and the project's
`simplified`/`style` pair, and four false alarms: `versionedRepresentations`
suppresses `fields` by contract, `lead` and `projectCategory` are absent only
on a project that has neither, and `Resolution` carries no `iconUrl`. Grepping
the source for documented property names found nothing further -- it cannot see
a key a bean builds, such as the version `userReleaseDate`. Paging, expansions
and the shapes an answer carries have since been swept per operation, above;
what remains of that frontier is whether a write persists every field it
accepted.

## 16. Certification

- Validate recently merged editor, transition, offline recovery and image-pin
  changes through the full CI gates before certifying these journeys.
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
