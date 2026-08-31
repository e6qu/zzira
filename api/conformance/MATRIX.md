# ZZIRA × Atlassian Jira Cloud REST API — Compat Matrix

Generated from the delivered surface (V0–V5). Legend: ✅ conformant · 🟡 subset
(shape-conformant, reduced semantics) · ⛔ not implemented. Public compatibility
is deliberately confined to `/rest/api/3` and `/rest/agile/1.0`; ZZIRA-owned
control-plane endpoints use `/rest/zzira/1` and are never presented as Jira APIs.

## Tier A — Core issue tracking

| Endpoint | Status | Notes |
|---|---|---|
| GET /rest/api/3/serverInfo | ✅ | |
| GET /rest/api/3/myself | ✅ | |
| GET /rest/api/3/user · /user/search | ✅ | workspace members |
| GET/POST /rest/api/3/project · /project/search · /project/{keyOrId} | ✅ | |
| POST /rest/api/3/issue | ✅ | ADF description, custom fields; project, summary, and issue type are explicit |
| GET/PUT/DELETE /rest/api/3/issue/{idOrKey} | ✅ | expand=renderedFields |
| GET/PUT /rest/api/3/issue/{idOrKey}/assignee | ✅ | PUT fields.assignee + dedicated assignee endpoint |
| GET /rest/api/3/issue/{idOrKey}/editmeta | 🟡 | system + custom fields |
| GET /rest/api/3/issue/createmeta (+ per-type) | 🟡 | project/issuetype/fields |
| GET/POST /rest/api/3/issue/{idOrKey}/transitions | ✅ | project workflow enforced |
| /comment CRUD | ✅ | ADF bodies, author-only delete |
| GET /rest/api/3/issue/{idOrKey}/changelog | ✅ | derived from the action log |
| /worklog CRUD | ✅ | author-only delete |
| POST /issue/{idOrKey}/attachments · /attachment/{id} · /attachment/content/{id} | ✅ | X-Atlassian-Token semantics |
| GET /rest/api/3/search · POST /search · /search/jql · /search/approximate-count | ✅ | JQL v1 subset; explicit pagination is validated rather than rewritten |
| GET /rest/api/3/mypermissions · POST /permissions/check | ✅ | evaluated from workspace role |
| /issueLinkType · POST /issueLink · DELETE /issueLink/{id} | ✅ | links sync to replicas |
| GET /rest/api/3/label | ✅ | distinct labels + query |
| GET /rest/api/3/issuetype · /priority · /status · /statuscategory · /resolution | ✅ | registry lists |

## Tier B — Agile

| Endpoint | Status | Notes |
|---|---|---|
| GET /rest/agile/1.0/board · /board/{id} | ✅ | seeded board |
| GET /board/{id}/issue · /board/{id}/backlog | ✅ | rank-ordered per column |
| GET /board/{id}/sprint | ✅ | |
| POST /rest/agile/1.0/sprint · GET /sprint/{id} · GET /sprint/{id}/issue | ✅ | |
| POST /sprint/{id}/issue | ✅ | moves issues into sprint (ranked) |
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
| Notifications (custom) GET /rest/zzira/1/notifications | ✅ | synced per-user entity |

## Tier D — long tail

| Endpoint | Status | Notes |
|---|---|---|
| Dashboards (minimal) | ✅ custom | /dashboard: status counts, my-open, recent activity — Jira gadget API not implemented (⛔, justified: widget framework is proprietary surface) |
| dev-status (SCM links) | ⛔ | justified: requires real SCM integrations |
| Service management surfaces | ⛔ | separate Atlassian product |

## E2E (browser-proven, Playwright/Chromium)

| Spec | Status |
|---|---|
| API contract smoke (serverInfo + create) | ✅ |
| UI login → create → issue view | ✅ |
| WASM worker boots + syncs | ✅ |
| Offline reload renders from local SQLite | ✅ |
| Two-browser convergence via the action log | ✅ |

## Load measurement

See `docs/loadtest.md` — sync p95 ≈ 6.8ms at 10k issues (flat across 100×
history growth); v2 index trigger (>300ms) is ~44× away.

| E2E: dashboard renders counts + activity | ✅ |
