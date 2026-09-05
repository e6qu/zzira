# Jira Cloud and wiki compatibility

Scope updated 2026-09-05 at the product owner's request. This supersedes the
earlier daily-core boundary and historical non-goals in PLAN.md and UI_PARITY.md.

**ZZIRA is not yet a full Jira Cloud or Confluence Cloud replacement.** Changing
only a base URL works for some tested REST operations, not for every Jira client
or app. A route returning success is not proof of matching semantics.

## Reproducible API scope

The published OpenAPI documents are vendored in [api/specs](../api/specs), with
retrieval URLs, versions and SHA-256 checksums in [pins.json](../api/specs/pins.json).
The [generated inventory](../api/conformance/cloud-operations.json) contains:

| Published API | Operations |
|---|---:|
| Jira Cloud platform REST v3 | 617 |
| Jira Software Cloud, including Agile and development integrations | 105 |
| Confluence Cloud REST v2 | 218 |
| Total in these three pins | 940 |

These counts include deprecated operations. They describe the pinned contracts,
not implementation coverage. Jira REST v2, Confluence REST v1, Automation,
Service Management and app runtimes require additional contracts; 940 is not the
entire Atlassian ecosystem. Regenerate with `python3 api/conformance/inventory.py`;
CI verifies pins and generated output with `--check`.

## Review findings and delivered changes

1. The historical API matrix claimed POST /project support, but no handler
   existed. Project create and details update now share a command path with the
   browser. Creation includes a usable Scrum or Kanban board in the same
   transaction. New project IDs are numeric, including the integer ID required
   by the create response; older identifiers remain valid.
2. Project search returned every project with a hardcoded first page. It now
   supports startAt/maxResults, key/ID/type/text filtering, key/name ordering,
   total/isLast and navigable nextPage links. Unsupported parameters are errors.
3. Board JQL used a join whose `pr` alias meant priority, while the JQL compiler
   expected project. Newly created project-filtered boards exposed this bug.
   Board filtering now uses the search query's matching joins.
4. Wiki routes, models and storage were absent. A first wiki journey now covers
   spaces, private spaces, storage-format pages, a rich-text editor, drafts,
   published pages, parent selection and a nested page tree, title search, version history, optimistic
   version checks, and trash/restore. Private wiki content is filtered from API
   reads and the workspace action log.
5. Issue creation flattened ADF into plain text. It now preserves incoming
   document structure and formatting; a round-trip test covers headings/lists
   and explicit unassignment. Rendering and complete ADF validation remain subsets.
6. `serverInfo.deploymentType` was an object rather than the Cloud string used
   by Jira clients. It now returns `"Cloud"`. Enhanced search now supports GET
   as well as POST, bounded JQL, field projections, IDs-only defaults, `isLast`,
   next-page tokens and limits up to 5000. Expansions/reconciliation options and
   stable cursor semantics remain gaps.
7. `make conformance` selected `TestGolden`, which matched no tests in api3.
   It now runs the API test packages and verifies the pinned inventory.

## Whole-surface delivery ledger

“Partial” means useful behavior exists and further work remains. Nothing in this
ledger certifies full parity. The older API matrix describes individual delivered
slices and must be read with the limitations below.

| Surface | State | Remaining work needed for full fidelity |
|---|---|---|
| Identity and authentication | Partial | Jira OAuth 2.0/3LO, scopes, app principals, organization/site administration, product access, groups, account lifecycle and token administration |
| Projects and administration | Partial | Project roles and permissions; permission/notification/security/workflow/screen/field schemes; work types; categories; avatars; key renames; archive/delete/restore; project templates beyond the delivered software pair |
| Work items | Partial | Full ADF fidelity, hierarchy/epics/subtasks, components, fix/affected versions, dates, estimates, votes, issue properties, bulk operations, complete metadata/expansion and permission semantics |
| Search and filters | Partial | Full JQL grammar/functions/history, enhanced-search expansions and stable cursors, v2 clients, sharing permissions, filter administration and subscriptions |
| Agile planning | Partial | Board CRUD and filter ownership, epic planning, estimates/capacity, parallel sprints, complete sprint reporting, dependencies, roadmap/timeline, cross-project plans |
| Releases and versions | Missing | Version lifecycle, release dates, issue membership, release notes, release hub, release progress and cross-project releases |
| Reports, charts and metrics | Missing beyond basic counts | Burndown/burnup, velocity, cumulative flow, control chart, cycle/lead time, throughput, created/resolved, time tracking, historical calculation rules, accessible chart tables and exports |
| Custom dashboards | Missing beyond fixed dashboard | Dashboard CRUD, ownership/sharing/favourites, layouts, gadget catalog/configuration/reordering, refresh, filters and metric visualizations |
| Automation and scheduled tasks | Missing | Rule editor, schedule/cron/time zones, triggers/conditions/actions, durable execution, leases/retries, actor permissions, idempotency, audit history and automation API compatibility |
| Notifications and collaboration | Partial | Full notification schemes, email/subscriptions, mentions with identity, watching permissions, collaboration and preferences |
| Wiki and knowledge | Partial | Confluence v1/CQL, space roles/restrictions, complete ADF/storage conversion and macros, comments, attachments, labels, watchers, complete tree/move APIs, revision viewing/restoring, templates, exports/imports, blogs, live documents and collaborative drafts |
| Diagrams and graphs | Missing beyond workflow diagram | Whiteboards, diagram authoring, embed/export, graph links/dependencies, diagram app modules, historical charts and graph queries |
| App/plugin system | Missing | Installation/lifecycle, Connect descriptors/JWT/qsh/scopes, Forge-compatible runtime and bridge, extension modules, custom fields, workflow rules, webhooks, storage, scheduled functions, isolation, upgrades/uninstall and app administration |
| Development integrations | Missing | SCM/development info, builds, deployments, feature flags, remote links, security info, operations and component APIs |
| Service/enterprise surfaces | Missing | Service Management and portal journeys, assets, approvals/SLAs, organization policies, auditing, import/export, data residency/retention and administration |
| Local-first behavior | Partial | Existing issue replica/outbox remains; wiki and administrative pages currently require online navigation/editing; new entity bootstrap/materialization and permission revocation need full offline/two-client journeys |
| Accessibility and interaction fidelity | Partial | Extend browser/a11y/responsive/dark-mode/keyboard coverage to each newly delivered surface; visual comparisons against current Cloud journeys |

## Exact boundary of the new project slice

- Create: name, key, description, URL, leadAccountId, assigneeType, software
  projectTypeKey, and company-managed Scrum/Kanban template keys.
- Update: name, description, URL, leadAccountId and assigneeType. Omitting a
  field preserves it; unsupported request fields are rejected.
- Workspace administrators create/update projects. This is a subset of Jira's
  global and project permission model. Members can read workspace projects.
- Default project-lead assignment applies when the issue API omits assignee;
  explicit null remains unassigned. UI issue forms make an explicit selection.
- Configuration mutations append actions atomically; live shell refresh and
  offline project administration are not implemented.

## Exact boundary of the new wiki slice

The delivered `/wiki/api/v2` routes are GET/POST spaces, GET space by ID,
GET pages for a space, GET/POST pages, GET/PUT/DELETE page by ID, and GET page
versions. Create-space returns 201; create/update-page returns 200; trash returns
204. List responses provide results, `_links.next`, and the Link header.

- Space creation requires a workspace administrator. Public spaces are readable
  and editable by workspace members; private spaces are accessible only to their
  creator. Space-specific roles and mutable restrictions are not implemented.
- Page bodies use a validated XHTML/storage subset: headings, paragraphs,
  emphasis, lists, links, code, blockquotes and basic tables. Unsupported markup,
  macros and alternate body representations are rejected before persistence.
- Drafts are visible only to their creator. ZZIRA uses monotonic revisions for
  draft edits as well as published edits; Confluence's separate draft-version and
  merge semantics remain a gap. Published-to-draft conversion is rejected.
- Parent pages must be published and belong to the same space. Cycles are
  rejected. Child pages must be moved or trashed before their parent is trashed.
- Trash preserves history. Restoring a trashed page retains its stored content.
  Permanent deletion, historical content retrieval and revision restore are gaps.
- Pagination currently uses opaque offsets, not Confluence's stable cursor
  semantics under concurrent mutations. Search supports exact API titles and
  title substring filtering in the UI; CQL and full-text search are gaps.
- Wiki content is logged transactionally and private actions are filtered.
  Wiki pages do not yet have local SQLite materialization or offline editing.

## Validation for this delivery

- The full Go suite passed with PostgreSQL integration tests enabled; changed
  packages were checked again after final API fixes.
- All 41 Playwright tests passed, including wiki/project journeys, accessibility
  in light/dark themes, 320px reflow, existing offline issue edits and two-browser
  convergence. Existing create fixtures now select their intended project.
- Server and full WASM builds, `go vet`, pinned inventory checks and
  `git diff --check` passed.
- The test database and attachment data were temporary. The original seed command
  regenerated the ignored `data/seed-tokens.json` test artifact; seeding now honors
  `DATA_DIR` so future isolated runs can keep token artifacts outside the checkout.

## Completion gates

Each capability needs request/response/schema tests against pinned specifications,
permission and failure-path tests, a complete browser journey, and appropriate
concurrency, offline and accessibility checks. Client compatibility additionally
requires real SDK and integration fixtures with only the base URL changed.

Forge apps execute on Atlassian's platform and depend on its runtime/services;
Connect apps depend on installation, descriptors, modules and authenticated
lifecycle events. Supporting a REST route does not supply either runtime.
Compatibility must name and test supported app modules and runtime services.

## Primary references

- [Jira Cloud projects](https://developer.atlassian.com/cloud/jira/platform/rest/v3/api-group-projects/)
- [Jira Software boards](https://developer.atlassian.com/cloud/jira/software/rest/api-group-board/)
- [Confluence Cloud spaces](https://developer.atlassian.com/cloud/confluence/rest/v2/api-group-space/)
- [Confluence Cloud pages](https://developer.atlassian.com/cloud/confluence/rest/v2/api-group-page/)
- [Forge platform](https://developer.atlassian.com/platform/forge/introduction/the-forge-platform/)
- [Connect app descriptor](https://developer.atlassian.com/cloud/jira/platform/connect-app-descriptor/)
