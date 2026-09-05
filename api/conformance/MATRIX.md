# ZZIRA × Atlassian Jira Cloud REST API — Compat Matrix

This is a historical delivered-slice ledger, not a certification of full Jira
conformance. Legend: ✅ a tested delivered slice · 🟡 a known subset · ⛔ missing.
A ✅ does not establish that every request option, wire type, permission rule or
client behavior matches Jira. The broader review and [940-operation pinned
inventory](cloud-operations.json) are described in [CLOUD_PARITY.md](../../docs/CLOUD_PARITY.md).
The public API targets `/rest/api/3`, `/rest/agile/1.0` and `/wiki/api/v2`.
ZZIRA-owned control-plane endpoints use `/rest/zzira/1`.

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
| GET/POST /rest/api/3/issue/{idOrKey}/transitions | ✅ | project workflow enforced |
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

## Tier C — Platform & admin

| Endpoint | Status | Notes |
|---|---|---|
| POST/GET /rest/api/3/field · GET /field/{id} | ✅ | text/number/datetime |
| /issue/createmeta + /editmeta include custom fields | ✅ | context-aware |
| Custom fields in issue beans + JQL | ✅ | incl. numeric compare |
| POST/GET /rest/api/3/webhook · DELETE /webhook/{id} · GET /webhook/refresh | ✅ | log-driven dispatcher, watermark, exactly-once claims |
| /filter CRUD + /filter/{id}/favourite | ✅ | |
| GET /rest/api/3/workflow/search · POST /workflow · GET/PUT /workflow/project/{key} | ✅ | **enforced**: transitions come from the project workflow |
| GET /rest/api/3/role | 🟡 | registry list |
| Issue security: scheme admin APIs, assignment, enforcement | ✅ | tombstones + per-user sync filtering + visibility on search/board/navigator/bootstrap |
| Permissionscheme admin APIs | ⛔ | workspace-role enforcement live |
| Screens/schemes APIs | ⛔ | editmeta serves the form contract |
| Notifications (custom) GET/PUT /rest/zzira/1/notifications · POST /notifications/read-all | ✅ | Private per-user entities with synchronized read state, unread count, and idempotent mutations |

## Tier D — expanded product surfaces

| Endpoint | Status | Notes |
|---|---|---|
| Dashboards | 🟡 16/17 pinned operations + custom UI | Fixed `/dashboard` plus `/dashboards`: CRUD, ownership/sharing, copy, gadget catalog/lifecycle/properties, favourites, layouts, refresh, JQL/filter lists and permission-filtered charts. Bulk edit and external gadget runtimes remain; see `docs/DASHBOARDS.md` |
| dev-status (SCM links) | ⛔ | requires SCM integration APIs and client tests |
| Service management surfaces | ⛔ | separate product surface included in the full-surface ledger |
| Automation and schedules | ⛔ | durable scheduler, rule editor, execution/audit and API contracts remain |
| Project versions and releases | 🟡 | Ten version operations, release hub/lifecycle, fix/affected membership, visible progress and notes; exact limits in [RELEASES.md](../../docs/RELEASES.md) |
| Metrics and reports | ⛔ | Historical calculations and chart/report journeys remain |
| Apps/plugins, diagrams and graphs | ⛔ | installation/runtime modules, diagram authoring and graph/report surfaces remain |
| Confluence Cloud /wiki/api/v2 spaces and pages | 🟡 | Initial space/page CRUD, parent validation, storage subset, drafts, versions, trash/restore and permission-filtered action log; exact limits in CLOUD_PARITY.md |

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

## Load measurement

See `docs/loadtest.md` — sync p95 ≈ 6.8ms at 10k issues (flat across 100×
history growth); v2 index trigger (>300ms) is ~44× away.

| E2E: dashboard renders counts + activity | ✅ |
