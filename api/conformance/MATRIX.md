# ZZIRA × Atlassian Jira Cloud REST API — Compat Matrix

This is a grouped delivered-slice ledger, not a certification of full Jira
conformance. Legend: ✅ a tested delivered slice · 🟡 a known subset · ⛔ missing.
A ✅ does not establish that every request option, wire type, permission rule or
client behavior matches Jira. The broader review and [1,207-operation pinned
inventory](cloud-operations.json) are described in [CLOUD_PARITY.md](../../docs/CLOUD_PARITY.md).
The pinned contracts cover `/rest/api/3`, `/rest/agile/1.0`,
`/rest/servicedeskapi`, `/wiki/rest/api`, `/wiki/api/v2`, the Automation site
gateway, and organization administration. ZZIRA-owned control-plane endpoints
use `/rest/zzira/1`.

## Tier A — Core issue tracking

| Endpoint | Status | Notes |
|---|---|---|
| GET /rest/api/3/serverInfo | ✅ | |
| GET /rest/api/3/myself | ✅ | |
| GET /rest/api/3/user · /user/search | ✅ | workspace members |
| GET/POST /rest/api/3/project · GET /project/search · GET/PUT /project/{keyOrId} | 🟡 | Shared create/details commands and browser journey; software Scrum/Kanban templates; pagination/filtering/order; schemes, project roles and lifecycle remain |
| POST /rest/api/3/issue | ✅ | Project key/id, ADF description, assignee, priority, labels, fix/affected versions, security and typed context-aware custom fields; unsupported fields are explicit errors |
| GET/PUT/DELETE /rest/api/3/issue/{idOrKey} | ✅ | expand=renderedFields |
| GET/PUT /rest/api/3/issue/{idOrKey}/assignee | ✅ | PUT fields.assignee + dedicated assignee endpoint |
| GET /rest/api/3/issue/{idOrKey}/editmeta | 🟡 | system + custom fields |
| GET /rest/api/3/issue/createmeta (+ paginated project/type routes) | ✅ | legacy filters/expanded fields plus current per-project issue-type and field metadata shapes |
| GET/POST /rest/api/3/issue/{idOrKey}/transitions | ✅ | Project workflow, nested actor conditions, required-field validators, development triggers, and atomic assignee, field-update, field-copy and registered-webhook post-functions enforced |
| /jira/forms/cloud/{cloudId}/issue/{idOrKey}/form lifecycle | 🟡 | Issue form index/attach/get/save/delete, visibility, submit/reopen, and executable attached/submitted workflow validators; project templates, exports, attachments, external data and copy remain |
| GET/POST/DELETE /rest/api/3/issue/{idOrKey}/watchers | 🟡 | complete self-subscription and watcher reads; managing other users is intentionally not exposed without a broader permission model |
| /comment CRUD | ✅ | ADF bodies, author-only delete |
| GET /rest/api/3/issue/{idOrKey}/changelog | ✅ | derived from the action log |
| /worklog CRUD | ✅ | author-only delete |
| POST /issue/{idOrKey}/attachments · /attachment/{id} · /attachment/content/{id} | ✅ | X-Atlassian-Token semantics |
| GET /rest/api/3/search · POST /search · GET/POST /search/jql · POST /search/approximate-count | 🟡 | JQL subset; enhanced search supports bounded queries, IDs-only defaults, field projections, isLast/tokens and 1–5000 result limits; expansions and stable cursor semantics remain |
| GET /rest/api/3/mypermissions · POST /permissions/check | ✅ | evaluated from workspace role |
| /issueLinkType · POST /issueLink · DELETE /issueLink/{id} | ✅ | links sync to replicas |
| GET /rest/api/3/label | ✅ | distinct labels + query |
| GET /rest/api/3/issuetype · /priority · /status · /statuscategory · /resolution | ✅ | registry lists |

## Tier B — Agile

| Endpoint | Status | Notes |
|---|---|---|
| GET /rest/agile/1.0/board · /board/{id} | ✅ | seeded board |
| GET /board/{id}/configuration | ✅ | ordered status mapping, constraints, location, estimation/subquery and ranking metadata |
| GET /board/{id}/quickfilter · /quickfilter/{id} | ✅ | position-ordered and paginated board quick filters |
| GET /board/{id}/issue · /board/{id}/backlog | ✅ | board columns and true unsprinted backlog are separately rank-ordered |
| GET /board/{id}/sprint | ✅ | |
| POST /rest/agile/1.0/sprint · GET/PUT /sprint/{id} · GET /sprint/{id}/issue | ✅ | metadata plus validated future → active → closed lifecycle |
| POST /sprint/{id}/issue | ✅ | moves issues into one open sprint (ranked), preserving closed-sprint history |
| POST /backlog/issue | ✅ | moves issues out of open sprints and retains closed-sprint history |
| POST /rest/agile/1.0/issue/rank | ✅ | LexoRank, column-scoped |
| /rest/devinfo/0.10 repositories, entities and property operations | 🟡 | All six pinned operations: ordered repository/commit/branch/pull-request ingestion, current reads, idempotent sequence-aware deletes, property existence/bulk delete, issue panel, branch-created workflow trigger, and cloudId alias; Connect JWT scopes, asynchronous deletes, complete validation and rate limits remain |
| /rest/builds/0.1 build operations | 🟡 | All four pinned operations: ordered bulk submission with per-item acceptance/rejection, issue-key/ID associations, keyed reads, sequence-aware keyed/property deletes, issue/release evidence, and cloudId alias; Connect JWT scopes, asynchronous deletes, complete optional validation and rate limits remain |
| /rest/deployments/0.1 deployment operations | 🟡 | All five pinned operations: ordered bulk submission, keyed reads/deletes, property cleanup, default allowed gating status, environment evidence on issues/releases, and cloudId alias; configurable gates, Connect JWT scopes, asynchronous deletes, complete optional validation and rate limits remain |

## Tier C — Platform & admin

| Endpoint | Status | Notes |
|---|---|---|
| POST/GET /rest/api/3/field · GET /field/{id} | ✅ | text/number/datetime |
| /issue/createmeta + /editmeta include custom fields | ✅ | context-aware |
| Custom fields in issue beans + JQL | ✅ | incl. numeric compare |
| POST/GET /rest/api/3/webhook · DELETE /webhook/{id} · GET /webhook/refresh | ✅ | log-driven dispatcher, watermark, exactly-once claims |
| /filter CRUD + /filter/{id}/favourite | ✅ | |
| GET /rest/api/3/workflow/search · modern workflow create/update/search/preview/capabilities · POST /workflow · GET/PUT /workflow/project/{key} | ✅ | **enforced**: project workflows, designer layouts, and executable transition rules round-trip through admin APIs and runtime |
| GET /rest/api/3/task/{taskId} · POST /task/{taskId}/cancel | ✅ | durable ENQUEUED/RUNNING/terminal progress, creator/admin visibility, safe cancellation, failure results and stale-claim recovery |
| GET /rest/api/3/role | 🟡 | registry list |
| Organizations orgs · directories · users · groups · memberships · workspaces · roles · events · domains · policies | 🟡 | All 47 operations reviewed: group and directory-user administration, product roles/activity, invitations/email, directory lifecycle, audit query/poll/detail/actions, DNS claims, and policy/resource CRUD/validation are useful tested subsets; runtime policy enforcement, central rate limits, and remaining edge semantics remain |
| Issue security: scheme admin APIs, assignment, enforcement | ✅ | tombstones + per-user sync filtering + visibility on search/board/navigator/bootstrap |
| Permissionscheme admin APIs | ⛔ | workspace-role enforcement live |
| Screens/schemes APIs | ⛔ | editmeta serves the form contract |
| Notifications (custom) GET/PUT /rest/zzira/1/notifications · POST /notifications/read-all | ✅ | Private per-user entities with synchronized read state, unread count, and idempotent mutations |

## Tier D — expanded product surfaces

| Endpoint | Status | Notes |
|---|---|---|
| Dashboards | 🟡 16/17 pinned operations + custom UI | Fixed `/dashboard` plus `/dashboards`: CRUD, ownership/sharing, copy, gadget catalog/lifecycle/properties, favourites, layouts, refresh, JQL/filter lists and permission-filtered charts. Bulk edit and external gadget runtimes remain; see `docs/DASHBOARDS.md` |
| Development information | 🟡 | All six devinfo, four build and five deployment operations plus issue/release UI and branch-created workflow trigger; feature flags, security, operations, components, configurable deployment gates and legacy dev-status summaries remain |
| Service management surfaces | 🟡 | All 75 pinned operations are reviewed: service projects, seeded help/incident/problem/change request types and groups, related-work links, permissions/properties, requests, durable request-type field metadata and typed custom answers, built-in and manager-defined JQL queues, default and ordered conditional SLA goals with stable cycle snapshots, holiday calendars, approvals, attachments, subscriptions, feedback, customer-only account lifecycle, desk customer admission, customer organizations, linked knowledge search, Assets workspace discovery, and filterable service volume/SLA/CSAT reports with request-type and channel breakdowns. Help-center, customer, agent, and manager journeys use the same command paths; conditional/advanced portal fields, complete JQL beyond label matching, SLA rule reordering and advanced criteria, complete Assets APIs, advanced incident/problem/change risk, approval, on-call and review configuration, comparisons, SLA goal distributions, exports and scheduled report delivery remain |
| Automation and schedules | 🟡 | all eight Jira Automation rule-management routes, fixed-rate editor, durable execution/retries, actor-scoped JQL, label/assign/transition actions and audit UI delivered; see `docs/AUTOMATION.md` for Cron, trigger, component and runtime gaps |
| Project versions and releases | 🟡 | Ten version operations, release hub/lifecycle, fix/affected membership, visible progress and notes; exact limits in [RELEASES.md](../../docs/RELEASES.md) |
| Metrics and reports | 🟡 | Permission-filtered 7/30/90 day DORA metrics, immutable delivery facts, accessible daily SVG/table and recent production evidence; Jira/Agile report catalog, comparisons, exports, subscriptions and scheduled delivery remain; see `docs/REPORTS.md` |
| Apps/plugins, diagrams and graphs | ⛔ | installation/runtime modules, diagram authoring and graph/report surfaces remain |
| Confluence Cloud spaces, pages, folders, Smart Links, hierarchy, labels, restrictions, attachments, comments, tasks and watches | 🟡 | Initial v2 space/page CRUD, drafts, versions, trash/restore, v1/v2 permission-filtered page children/ancestors/descendants, all 24 v2 folder and Smart Link create/read/delete/hierarchy/operation/property resources on a shared heterogeneous tree, page/attachment footer comments, page inline-comment threads with exact anchors and resolution, filtered page tasks with assignment/due/completion, labels, all 12 v1 page-restriction operations, 21 v1/v2 versioned page-attachment/property/label/thumbnail operations, and all 12 v1 content/space/label watch operations with deduplicated in-app delivery across 114 reviewed operations, plus permission-filtered action log; exact limits in CLOUD_PARITY.md |

## E2E (browser-proven, Playwright/Chromium)

| Spec | Status |
|---|---|
| API contract smoke (serverInfo + metadata-driven create) | ✅ |
| UI login → full-field create → validation recovery → create another → issue view | ✅ |
| WASM worker boots + syncs | ✅ |
| Offline reload renders from local SQLite | ✅ |
| Two-browser convergence via the action log | ✅ |
| Board controls/settings, issue preview, Agile configuration and quick-filter APIs | ✅ |
| Notifications inbox, private API mutations, unread filtering, and open-to-work flow | ✅ |
| WCAG 2.2 A/AA axe sweep, target sizes, keyboard movement and 320px reflow | ✅ |
| Service help center, request, queue, SLA, approval, file, notification, feedback, customer and organization management journey | ✅ |
| Wiki author/member page access, authoring, stale edits, history, threaded footer/inline comments, assigned page tasks, child pages and trash/restore | ✅ |

## Load measurement

See `docs/loadtest.md` — sync p95 ≈ 6.8ms at 10k issues (flat across 100×
history growth); v2 index trigger (>300ms) is ~44× away.

| E2E: dashboard renders counts + activity | ✅ |
