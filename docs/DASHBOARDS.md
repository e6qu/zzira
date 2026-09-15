# Custom dashboards

The dashboard directory at `/dashboards` provides private and shared dashboards.
Owners can change dashboard details and sharing, while people named in edit
permissions can arrange gadgets and configure their queries. Every viewer's
gadget results are evaluated with that viewer's issue-security permissions.

## Delivered Jira Cloud operations

ZZIRA implements 16 of the 17 dashboard operations in the pinned Jira Cloud
platform REST v3 contract:

| Operation | Delivered behavior |
|---|---|
| GET/POST `/rest/api/3/dashboard` | Paginated visible dashboards with `my` and `favourite` filters; create a private or shared dashboard |
| GET `/rest/api/3/dashboard/search` | Name and owner filtering, active status, pagination, supported expansions and Jira orderings |
| GET/PUT/DELETE `/rest/api/3/dashboard/{id}` | Permission-scoped details, owner-only metadata/sharing updates and deletion |
| POST `/rest/api/3/dashboard/{id}/copy` | Copy every gadget and property into a private dashboard owned by the caller |
| GET `/rest/api/3/dashboard/gadgets` | Native ZZIRA gadget catalog |
| GET/POST `/rest/api/3/dashboard/{id}/gadget` | Filtered gadget listing and native gadget creation |
| PUT/DELETE `/rest/api/3/dashboard/{id}/gadget/{gadgetId}` | Title, color and position updates with stable row compaction; gadget removal |
| GET `/rest/api/3/dashboard/{id}/items/{itemId}/properties` | Sorted property key listing |
| GET/PUT/DELETE `/rest/api/3/dashboard/{id}/items/{itemId}/properties/{propertyKey}` | JSON property lifecycle with Jira key and value bounds |

`PUT /rest/api/3/dashboard/bulk/edit` changes permissions, changes owners and
deletes, with a per-dashboard error map. Dashboards share with everyone signed
in, people, groups, projects and project roles, and `extendAdminPermissions`
lets a site administrator update a dashboard they neither own nor were shared.
Searching archived or deleted dashboards answers 400, because dashboards here
are only ever active. Jira Cloud no longer shares dashboards publicly, so there
are no anonymous dashboards; REST extensions for this site's layout and
favourite settings remain compatibility gaps.

## Native gadgets and presentation

The built-in catalog contains:

- `com.zzira:filter-results`
- `com.zzira:issue-statistics`
- `com.zzira:pie-chart`
- `com.zzira:assigned-to-me`
- `com.zzira:created-vs-resolved`
- `com.zzira:resolution-time`
- `com.zzira:velocity`
- `com.zzira:sprint-burndown`
- `com.zzira:two-dimensional-statistics`
- `com.zzira:heat-map`
- `com.zzira:watched-issues`
- `com.zzira:voted-issues`
- `com.zzira:in-progress`
- `com.zzira:recently-created`
- `com.zzira:average-age`
- `com.zzira:time-since`
- `com.zzira:days-remaining`
- `com.zzira:sprint-health`

Gadgets accept direct JQL or a saved filter through the reserved
`zzira.config` item property. Lists return up to 50 results. Statistics, pie
charts and heat maps calculate their full permission-filtered total and group
by status, priority, work type, assignee, reporter, resolution, project or
label. Work with several labels counts once under each label, while the total
counts each work item once. Pie charts include an equivalent data table, and
heat maps size each value by its share. Two dimensional filter statistics count
the same work by one grouping across its columns and another down its rows,
with row and column totals, showing the largest rows up to the result limit.
Assigned-to-me adds `assignee = currentUser()` when each viewer loads it.
Watched work items, Voted work items and Work in progress likewise add
`issue in watchedIssues()`, `issue in votedIssues()` and
`assignee = currentUser() AND statusCategory = indeterminate`.
Report gadgets draw the matching report instead of a query. Created vs.
resolved and Resolution time keep a `projectKey`, a `days` window of 7, 30 or
90 and, for created vs. resolved, `cumulative` running totals; Velocity and
Sprint burndown keep a scrum board's `boardId`, and the burndown follows the
board's active sprint. Recently created, Average age and Time since also keep a
`projectKey` and `days` window. Recently created splits each day's new work by
whether it is resolved now. Average age averages the age of work unresolved at
the end of each day, or now for today. Time since counts work whose `dateField`
(`created`, `updated` or `resolved`) fell on each day. Like created vs.
resolved, these use each item's current resolution. Days remaining in sprint and
Sprint health keep a scrum board's `boardId` and follow its active sprint:
days remaining counts whole days to the planned end, or days overdue. Sprint
health shows the share of planned time elapsed, the share of the board's
estimation statistic complete (by work items when nothing is estimated), and
work added or removed after the start as a share of the work committed at the
start. Each viewer sees the report counted from their own
access to the work, with its values as a table. A gadget not yet configured,
a project that turned Reports off, or a board that is not a scrum board says
so instead. The configuration form offers only projects and scrum boards the
editor can browse. See [REPORTS.md](REPORTS.md) for how each report counts.
Active `jira:dashboardGadget` modules from installed apps also join the browser
catalog. Their escaped host-rendered body can be placed, titled, colored,
positioned, copied and removed like a built-in gadget. Stable module IDs keep
placements intact through upgrades; suspension shows an unavailable state,
while module removal or uninstall removes the corresponding placements.
Standard Connect `jiraDashboardItems` use the same lifecycle and retain their
descriptor description in the catalog. A remote item opens in a sandboxed,
signed iframe with `dashboard.id`, `dashboardItem.id`, `dashboardItem.key` and
`dashboardItem.viewType` context. The catalog renders its descriptor thumbnail
through an authenticated endpoint that signs the validated app-relative image
request. A `configurable` item shows Configure to people who can edit the
dashboard, which sends it Connect's `jira_dashboard_item_edit` event, and a
`refreshable` item shows Refresh, which reloads it with a newly signed frame.
Items may be offered and shown only under Connect conditions, inverted or
grouped with `AND` or `OR`; see [APPS.md](APPS.md#connect-conditions) for the
evaluated conditions. Items use Connect's JavaScript API to resize, rename
themselves and call product APIs within their app's scopes; see
[APPS.md](APPS.md).

## Wallboards

View as wallboard, at `/dashboards/{id}/wallboard`, shows a dashboard full
screen without navigation for a team display, in the viewer's theme. As in
Jira, consecutive gadgets of one colour in a column form a group that shows one
gadget at a time and moves to the next every 30 seconds, while gadgets of
different colours show together. Gadgets are read-only there, and the page
reloads when the dashboard's automatic refresh interval passes.

The site has one wallboard slide show at `/dashboards/slideshow`. Anyone who can
edit a dashboard configures it from that dashboard: between 1 and 50
dashboards they can view, 5 to 3600 seconds per dashboard (30 by default) and
an optional random order. Each viewer sees only the chosen dashboards they can
view, and the slide show reloads after each full pass so it draws the latest
work. Both pages offer a Pause rotation control and an exit link.

## Dashboard emails

Anyone who can view a dashboard can have it emailed every day or every Monday
at 08:00 UTC from **Email this dashboard**, to themselves or to members who can
view it too; a recipient who cannot is refused when the email is scheduled.
Each recipient receives the dashboard as they see it: its name and link, then
a line per gadget — a list gadget's count and first five work items, a chart
gadget's count by its grouping, a report gadget's created and resolved counts,
average resolution time, average velocity or the active sprint's remaining
work, and a pointer to the dashboard for app gadgets or gadgets still to be
configured. Deliveries are durable runs claimed one at a time and retried with
backoff; a recipient who can no longer view the dashboard fails the run with
that reason, which the panel shows, and people already sent to are not sent
again. The panel lists the viewer's own dashboard emails with their next
delivery and removes them.

The browser supports Jira-style one, two and three-column layouts (`A`, `AA`,
`AB`, `BA`, `AAA`), gadget reordering, eight accent colors, favourites, manual
refresh, and automatic refresh at one, five or fifteen minutes. The content
refresh endpoint is never placed in the service-worker page cache. Revoking a
share clears a viewer's rendered gadgets on their next refresh.

## Compatibility boundary

ZZIRA validates REST-created module keys against its built-in catalog and
browser-created app gadgets against active installed modules. Installed native
gadgets may use escaped host-rendered content or a declared HTTPS remote module
with signed Connect context. Standard Connect dashboard items must use a
relative URL beneath their descriptor `baseUrl`. ZZIRA does not execute unmanaged gadget URLs,
Forge modules, or unknown Jira system gadget module keys. Clients sending a
URI, an unknown module key, or `ignoreUriAndModuleKeyValidation=true` receive an
explicit validation error.

Dashboard writes add ID-only invalidation records to the workspace action log.
They never serialize dashboard configuration or gadget results. Dashboard
materialization in the offline SQLite replica is not delivered yet, so custom
dashboard pages intentionally require an online session.
