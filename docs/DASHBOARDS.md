# Dashboards

Dashboards collect gadgets that show work, charts and reports. Each viewer sees every gadget according to their own permissions. Dashboards can be private or shared. People who have edit permission arrange the gadgets and set their queries. A dashboard can also be shown as a wallboard or sent by email. The REST API is described in [DASHBOARDS_API.md](DASHBOARDS_API.md). This page belongs to [Jira Software](JIRA_SOFTWARE.md); see [CLOUD_PARITY.md](CLOUD_PARITY.md) for status.

## UI

| Page | Purpose |
|---|---|
| `/dashboard` | The workspace home dashboard: status counts and recent activity |
| `/dashboards` | Directory of private and shared dashboards; create a dashboard |
| `/dashboards/{id}` | One dashboard. `?edit=1` edits details, sharing and layout; `?add=1` opens the gadget catalog |
| `/dashboards/{id}/wallboard` | The dashboard as a wallboard |
| `/dashboards/slideshow` | The site's wallboard slide show |

**Owners** can change a dashboard's details and sharing, copy it and delete it.

**Editors** can:
- choose a layout: `A`, `AA`, `AB`, `BA` or `AAA`;
- reorder gadgets;
- give gadgets one of eight accent colors;
- configure gadget queries.

**Every viewer** can:
- add the dashboard to favourites;
- refresh it by hand;
- set it to refresh automatically every 1, 5 or 15 minutes.

When a share is revoked, the person's rendered gadgets are cleared on their next refresh. The service worker never caches the content refresh endpoint (`/dashboards/{id}/content`).

## Built-in gadgets

| Gadget | Module key | Shows |
|---|---|---|
| Filter results | `com.zzira:filter-results` | Work items matching JQL or a saved filter |
| Issue statistics | `com.zzira:issue-statistics` | Counts by one grouping |
| Pie chart | `com.zzira:pie-chart` | Counts by one grouping, with a data table |
| Heat map | `com.zzira:heat-map` | Counts by one grouping, each value sized by its share |
| Two dimensional filter statistics | `com.zzira:two-dimensional-statistics` | Counts by one grouping across and another down, with totals |
| Assigned to me | `com.zzira:assigned-to-me` | Adds `assignee = currentUser()` |
| Watched work items | `com.zzira:watched-issues` | Adds `issue in watchedIssues()` |
| Voted work items | `com.zzira:voted-issues` | Adds `issue in votedIssues()` |
| Work in progress | `com.zzira:in-progress` | Adds `assignee = currentUser() AND statusCategory = indeterminate` |
| Activity stream | `com.zzira:activity-stream` | Creation, field changes and comments on matching work, newest first |
| Calendar | `com.zzira:calendar` | This month's matching work by due date, plus release dates |
| Bubble chart | `com.zzira:bubble-chart` | Matching work by age and participants or votes |
| Created vs. resolved | `com.zzira:created-vs-resolved` | The report, for a project |
| Resolution time | `com.zzira:resolution-time` | The report, for a project |
| Recently created | `com.zzira:recently-created` | New work per day, split by whether it is resolved now |
| Average age | `com.zzira:average-age` | Average age of unresolved work at the end of each day |
| Time since | `com.zzira:time-since` | Work whose `dateField` (`created`, `updated`, `resolved`) fell on each day |
| Road map | `com.zzira:road-map` | Upcoming and overdue versions with progress |
| Velocity | `com.zzira:velocity` | The velocity chart, for a scrum board |
| Sprint burndown | `com.zzira:sprint-burndown` | The active sprint's burndown, for a scrum board |
| Days remaining in sprint | `com.zzira:days-remaining` | Whole days to the active sprint's planned end, or days overdue |
| Sprint health | `com.zzira:sprint-health` | Time elapsed, work complete, and scope change for the active sprint |

**Query gadgets**
- Configuration is stored in the reserved `zzira.config` item property. It holds either direct JQL or a saved filter.
- Lists show at most 50 results.
- Groupings can be status, priority, work type, assignee, reporter, resolution, project or label.
- Chart totals include all permitted matching work, not just the listed results.
- A work item with several labels is counted under each label, but only once in the total.
- Two dimensional statistics show the largest rows, up to the result limit.

**Activity stream**
- Lists only work the viewer can see.
- A comment restricted to a group or project role appears only to its members.

**Calendar**
- Shows the current month in weeks starting on Monday.
- Lists at most 200 matching work items, plus a count of the rest.
- Also shows the release dates of unarchived versions in those work items' projects.

**Bubble chart**
- Plots the most recently updated matching work, up to the result limit.
- The horizontal axis is days since last update.
- `bubbleAxis` chooses what goes on the vertical axis, participants or votes. The other value sets the bubble size.
- Participants are the reporter, the assignee and everyone who commented.
- Bubbles are darker the more recently the work changed.
- A table lists the same values.

**Report gadgets**
- **Project-based.** Created vs. resolved, Resolution time, Recently created, Average age and Time since store a `projectKey` and a `days` window of 7, 30 or 90. Created vs. resolved can also show running totals (`cumulative`).
- **Board-based.** Velocity, Sprint burndown, Days remaining and Sprint health store a scrum board's `boardId` and follow its active sprint.
- **Road map** stores a `projectKey` and a `days` window. It lists up to 20 of the project's unreleased, unarchived versions that are due in the window or already overdue, soonest first.
- **Sprint health** shows:
  - the share of planned time elapsed;
  - the share of the estimate complete (counted by work items when nothing is estimated);
  - work added or removed after the start, as a share of the work committed at the start.
- Each viewer sees the counts for the work they can access, with the values also shown as a table.
- A gadget shows a message instead when:
  - it is not configured yet;
  - its project has turned Reports off;
  - its board is not a scrum board.
- The configuration form offers only projects and scrum boards the editor can browse.
- See [REPORTS.md](REPORTS.md) for how each report counts.

## App gadgets

**Native app gadgets**
- Active `jira:dashboardGadget` modules from installed apps appear in the catalog.
- Their host-rendered body is escaped. You can place, title, color, move, copy and remove them like built-in gadgets.
- Module ids are stable, so placements survive app upgrades.
- A suspended app shows an unavailable state.
- Removing the module or uninstalling the app removes its placements.

**Connect dashboard items (`jiraDashboardItems`)**
- They follow the same lifecycle and keep their descriptor description in the catalog.
- A remote item opens in a sandboxed, signed iframe with this context: `dashboard.id`, `dashboardItem.id`, `dashboardItem.key` and `dashboardItem.viewType`.
- Its URL must be relative to the descriptor's `baseUrl`.
- The descriptor thumbnail is served through an authenticated endpoint that signs the image request.
- A `configurable` item shows **Configure** to editors. Configure sends Connect's `jira_dashboard_item_edit` event.
- A `refreshable` item shows **Refresh**. Refresh reloads the item with a newly signed frame.
- Items are offered and shown only when their [Connect conditions](APPS.md#connect-conditions) pass.
- Items can use the [Connect JavaScript API](APPS.md#connect-javascript-api) to resize, rename themselves, and call product APIs within their app's scopes.

**Not supported:** gadget URLs not declared by an installed app, Forge modules, and Jira system gadget module keys.

## Wallboards

**View as wallboard**
- Shows a dashboard full screen with no navigation, in the viewer's theme.
- Consecutive gadgets of the same color in a column form a group. The group shows one gadget at a time and switches every 30 seconds.
- Gadgets of different colors are shown together.
- Gadgets are read-only.
- The page reloads when the dashboard's automatic refresh interval passes.

**Slide show**
- The site has one slide show.
- Anyone who can edit a dashboard can configure it from that dashboard:
  - 1 to 50 dashboards they can view;
  - 5 to 3,600 seconds per dashboard (default 30);
  - optional random order.
- Each viewer sees only the chosen dashboards they can view.
- The slide show reloads after each full pass.

Both pages have a **Pause rotation** control and an exit link.

## Dashboard emails

**Scheduling.** Use **Email this dashboard**.
- Anyone who can view the dashboard can schedule an email.
- Timing: every day or every Monday at 08:00, or any cron expression, in a chosen time zone.
- Recipients: yourself, and members who can view the dashboard. A recipient who cannot view it is refused when you schedule.

**Content.** Each recipient gets the dashboard as they see it:
- the dashboard name and link;
- then one line per gadget:
  - list gadgets: the count and the first five work items;
  - chart gadgets: counts by grouping;
  - report gadgets: a summary figure;
  - app gadgets and gadgets not yet configured: a pointer to the dashboard.

**Delivery**
- Deliveries are durable runs, handled one at a time and retried with backoff.
- A recipient who can no longer view the dashboard fails the run. The reason appears in the panel.
- People who were already sent that email are not sent it again.

**Managing emails.** The panel lists your own dashboard emails with their next delivery, and lets you remove them.

## Gaps

- Dashboards are not stored in the offline replica, so dashboard pages need a connection.
- Forge dashboard gadgets and Jira's system gadget modules are not supported.

Remaining work is tracked in [PLAN.md](../PLAN.md).

## See also

- [FILTERS.md](FILTERS.md): saved filters.
- [APPS.md](APPS.md): app modules.
- Code: `internal/web/custom_dashboards.go`, `internal/web/dashboard_report_gadgets.go`, `internal/web/dashboard_stream_gadgets.go`, `internal/web/dashboard_wallboard.go`, `internal/store/dashboard_*.go`.
- Browser tests: `e2e/dashboards.spec.ts`, `e2e/dashboard_reports.spec.ts`, `e2e/dashboard_subscriptions.spec.ts`, `e2e/v6.spec.ts`.
