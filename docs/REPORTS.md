# Reports

Project reports chart delivery, sprint, flow and issue-analysis data. They count only work items the viewer can browse. Every report has a CSV download and can be emailed on a schedule, and most can be compared with the previous period. This page is part of [Jira Software](JIRA_SOFTWARE.md); see [CLOUD_PARITY.md](CLOUD_PARITY.md) for status. The service desk report belongs to [Jira Service Management](SERVICE_MANAGEMENT.md) and is summarized below.

## Pages

| Report | Path | Projects | Scope |
|---|---|---|---|
| Report list | `/projects/{key}/reports` | all | Built-in reports, plus `jira:report` app modules grouped by category ([APPS.md](APPS.md)) |
| DORA metrics | `/projects/{key}/reports/dora` | all | `window` of 7, 30 (default) or 90 days |
| Sprint report | `/projects/{key}/reports/sprint` | software | A scrum board and one started sprint |
| Velocity chart | `/projects/{key}/reports/velocity` | software | A scrum board |
| Cumulative flow diagram | `/projects/{key}/reports/cumulative-flow` | software | Any board; 14, 30 (default) or 90 days |
| Control chart | `/projects/{key}/reports/control-chart` | software | Any board; 14, 30 (default) or 90 days |
| Epic report | `/projects/{key}/reports/epic` | software | An epic and a board |
| Version report | `/projects/{key}/reports/version` | software | An unarchived version and a board |
| Created vs. resolved | `/projects/{key}/reports/created-vs-resolved` | all | 7, 30 (default) or 90 days; `cumulative=true` shows running totals |
| Resolution time | `/projects/{key}/reports/resolution-time` | all | 7, 30 (default) or 90 days |
| Service desk report | `/service/agent/{desk}/reports` | service | 7, 30 or 90 days |

- A window value outside the allowed set returns 400.
- If a project turns off its Reports feature, it has no report pages.
- The board-based reports use the board's estimation field. On a board without one, they count work items instead.
- Every chart is an SVG with a title and description, and has a data table you can reach with the keyboard.
- Every report is tested in light and dark themes and at 320 px width.

## DORA metrics

| Measure | Calculation |
|---|---|
| Deployment frequency | Number of successful production deployments, plus a weekly rate |
| Lead time for changes | Median time from a linked commit to the first successful production deployment after it |
| Change failure rate | Failed and rolled-back production deployments divided by all successful, failed and rolled-back production deployments |
| Time to restore service | Median time from an incident's creation to when it first got a resolution |

**Which deployments count**
- **Production:** the deployment's environment `type`, as sent by the provider, must be `production`. The environment name is ignored.
- **Visibility:** the deployment must be linked to a work item in this project that the viewer can see.
- **Latest update:** every accepted build and deployment update is stored and never changed. For each deployment, the report uses the update with the highest update sequence number.
- **Ignored states:** `pending`, `in_progress`, `cancelled` and `unknown`.
- **Time zone:** all timestamps are UTC.

**What counts as an incident**
- An incident is a Jira Service Management request raised as an incident. Incident teams, escalations and incident updates use the same definition.
- Labels do not matter. A work item labeled `incident` is not counted unless it was raised as an incident, and an incident still counts if the label is removed.

**The page shows**
- a daily chart of production deployments, with its data table;
- the ten most recent production events.

## Sprint report and velocity chart

**Sprint report**
- **Which sprint:** the active sprint, or if none, the most recently completed one. Any started sprint can be chosen.
- **How it is built:** the report replays the action log from the sprint's start to its completion (or to now while the sprint runs).

| Section | Work items |
|---|---|
| Completed work items | In the sprint at the end, and done at the end but not done when they joined |
| Work items not completed | In the sprint at the end and not done |
| Work items completed outside of this sprint | Already done when they joined the sprint |
| Work items removed from sprint | Joined during the sprint and left before the end |

**Markers**
- Work items added after the start are marked.
- A changed estimate shows its starting and ending values.

**Burndown**
- The remaining estimate steps down as work completes.
- It steps up as work is added, reopened or re-estimated.
- A guideline falls to zero at the planned end.
- A table lists every change.

**Burnup**
- Plots the sprint's scope and its completed work as two step lines.

**Sprint dates**
- Sprints record their actual start and completion times separately from their planned dates.
- For sprints completed before this was recorded, the times come from the sprint's start and complete actions in the action log.

**Velocity chart**
- Covers the board's seven most recently completed sprints, oldest first.
- For each sprint, compares commitment (the estimate of the sprint's work at its start) with completed work.
- Also shows the average completed per sprint.

## Cumulative flow diagram and control chart

Both reports use the work items the board's filter shows the viewer. Each work item's status history comes from the action log.

**Cumulative flow diagram**
- Counts the board's work items in each column at the end of each day, including today.
- Stacks the columns with the first column on top, as Jira does.

**Control chart**
- Plots each work item completed in the window by its completion date and its cycle time.
- **Cycle time** runs from the work item's first move into an in-progress status to the move into done that completed it.
  - Reopened work counts only once it is done again.
  - Work that went straight to done has no cycle time.
- The summary gives the number of work items and the average and median cycle time. The chart marks the average.

## Epic report and version report

The epic report follows an epic's child work items (not sub-tasks). The version report follows the work items fixed in a version.

**How they are built**
- Both replay the action log day by day, up to today.
- The epic report starts from the epic's creation.
- The version report starts from the version's start date, or from its earliest work item if there is no start date.

**What they show**
- **Chart:** each day's estimate for all existing work, against the estimate for completed work.
- **Summary:** the share of work items done, the total and remaining estimate, and how many work items have no estimate.
- **Tables:** completed work items and incomplete work items, with their estimates.

An epic the viewer cannot see returns 404.

## Created vs. resolved and resolution time

**Created vs. resolved**
- Counts work items created and resolved each day.
- Uses the date of each work item's current resolution, so a reopened work item no longer counts as resolved.

**Resolution time**
- For work items resolved each day, averages the time from creation to resolution.
- Also shows the overall average for the window.
- Days with no resolved work items have no bar.

## Service desk report

Service project agents and managers see:
- **Request volume.**
- **Open and resolved counts.** A request is open while it has no resolution and resolved once it has one.
- **SLA breaches.** Requests with a breached SLA cycle, calculated with the desk's calendars and holidays.
- **CSAT.** The average score and the number of responses.
- **Daily intake.** Requests received each day.

**Breakdowns**
- Requests are broken down by request type, channel, priority and organization.
- A request counts in every organization of the desk that its customer belongs to.
- Requests with no priority show as None.
- Requests whose customer belongs to none of the desk's organizations show as No organization.

## Common features

**Compare with previous period**
- Available on DORA metrics, the control chart, created vs. resolved, resolution time and the service desk report.
- Each summary figure shows its change from the window of the same length just before, calculated with the same access, board and filters. Example: "Up 2 from 3 in the previous 7 days".
- If there is nothing to measure in a window, the report says so instead of showing a change.
- Service requests in the previous window are picked by creation date; whether they are open or resolved is their state today.

**Download CSV**
- Exports the data behind the chart for the board, sprint, epic, version, window and filters on screen.
- The file name combines the project key, the report and the date, for example `ZZ-velocity-chart-2026-09-15.csv`.
- **Rows:**
  - Sprint, epic and version reports: one row per work item, with its section, its estimates and whether it was added after the start.
  - Control chart: one row per work item, with its cycle time in hours.
  - Other reports: one row per day or per sprint.
- A compared report adds a Period column, with the previous period's rows first.
- The file is sent with `Cache-Control: no-store`.
- A cell starting with `=`, `+`, `-`, `@`, a tab or a carriage return gets a leading apostrophe, so spreadsheets do not run it as a formula.

**Email this report** (`POST /reports/email`)
- **Schedule:** every day or every Monday at 08:00, or a cron expression you supply, in a time zone you choose.
- **What is sent:** the board, sprint, epic, version, window, filters and comparison that were on the page when you subscribed. The same report with different choices is a separate email.
- **Access check at subscription:** the subscriber and every recipient must be able to open the report.
- **Each run:** the report is drawn for each recipient with that recipient's own access, and emailed with a link back to the page.
- **Failures:** if a recipient has lost access or left the site, the run fails.
  - Failed runs are retried with backoff.
  - Recipients who were already sent the email are not sent it again.
  - The failure reason appears under the schedule on the report page.

## Gaps

- The DORA mapping cannot be configured. You cannot choose which environments, pipelines or incident types count; production is fixed as environment type `production`.
- DORA has no excluded-period calendars.
- Several Jira reports are not built: release burndown, epic burndown, user workload, version workload, time tracking, single-level group-by, deployment frequency and cycle time.

Remaining work is tracked in [PLAN.md](../PLAN.md).

## See also

- [DASHBOARDS.md](DASHBOARDS.md): report gadgets.
- [RELEASES.md](RELEASES.md): delivery evidence for each version.
- Code:
  - Store: `internal/store/reports.go` (DORA), `internal/store/agile_reports.go`, `internal/store/board_reports.go`, `internal/store/progress_reports.go`, `internal/store/issue_analysis_reports.go`, `internal/store/service_reports.go`.
  - Web: `internal/web/reports.go`, `internal/web/agile_reports.go`, `internal/web/report_csv.go`, `internal/web/report_compare.go`, `internal/web/report_email.go`.
- Browser tests: `e2e/reports_flow.spec.ts`, `e2e/reports_progress.spec.ts`, `e2e/report_subscriptions.spec.ts`, `e2e/dashboard_reports.spec.ts`.

