# Reports

Project reports chart delivery, sprint, flow and issue-analysis data. They count only work items the viewer can browse. Every report has a CSV download and can be emailed on a schedule, and most can be compared with the previous period. This page is part of [Jira Software](JIRA_SOFTWARE.md); see [CLOUD_PARITY.md](CLOUD_PARITY.md) for status. The service desk report belongs to [Jira Service Management](SERVICE_MANAGEMENT.md) and is summarized below.

## Pages

| Report | Path | Projects | Scope |
|---|---|---|---|
| Report list | `/projects/{key}/reports` | all | The built-in reports, with the agile ones offered on software projects only, then a card for each `jira:report` app module labelled with its category ([APPS.md](APPS.md)) |
| DORA metrics | `/projects/{key}/reports/dora` | all | `window` of 7, 30 (default) or 90 days |
| Sprint report | `/projects/{key}/reports/sprint` | software | A scrum board and one started sprint |
| Velocity chart | `/projects/{key}/reports/velocity` | software | A scrum board |
| Cumulative flow diagram | `/projects/{key}/reports/cumulative-flow` | software | Any board; 14, 30 (default) or 90 days |
| Control chart | `/projects/{key}/reports/control-chart` | software | Any board; 14, 30 (default) or 90 days |
| Epic report | `/projects/{key}/reports/epic` | software | An epic and a board |
| Version report | `/projects/{key}/reports/version` | software | An unarchived version and a board |
| Epic burndown | `/projects/{key}/reports/epic-burndown` | software | An epic and a board with started sprints |
| Release burndown | `/projects/{key}/reports/release-burndown` | software | An unarchived version and a board with started sprints |
| Created vs. resolved | `/projects/{key}/reports/created-vs-resolved` | all | 7, 30 (default) or 90 days; `cumulative=true` shows running totals |
| Resolution time | `/projects/{key}/reports/resolution-time` | all | 7, 30 (default) or 90 days |
| User workload | `/projects/{key}/reports/user-workload` | all | Unresolved work in the project |
| Version workload | `/projects/{key}/reports/version-workload` | all | An unarchived version |
| Time tracking | `/projects/{key}/reports/time-tracking` | all | Unresolved work; `version` narrows it to one version |
| Work by field | `/projects/{key}/reports/group-by` | all | `field` of assignee (default), issuetype, status, priority, resolution or reporter |
| Service desk report | `/service/agent/{desk}/reports` | service | 7, 30 or 90 days |

- A window value outside the allowed set returns 400.
- The Projects column is where the report list offers the report. The agile pages answer for any project; on one without a board they show their empty state.
- If a software project turns off its Reports feature (`jsw.classic.reports`), every report page under it is 404. Other project types have no features to turn off. See [PROJECT_GOVERNANCE.md](PROJECT_GOVERNANCE.md).
- The board-based reports use the board's estimation field, which is Story point estimate on a scrum board. On a board without one, they count work items instead.
- A page with no board, sprint, epic or version to read says so instead of drawing an empty chart.
- Every chart is an SVG with a title and description, and has a data table you can reach with the keyboard.
- Every report is tested in light and dark themes and at 320 px width.

## What each report needs

A report draws only from data that already exists, so a site being filled for a demo needs each row below before its chart says anything.

| Report | Needs |
|---|---|
| DORA metrics | Deployments submitted to `/rest/deployments/0.1/bulk` whose environment `type` and pipeline the project counts (`production` and every pipeline by default), whose `issueKeys` name work items of this project the viewer can browse, and whose `lastUpdated` falls in the window. Lead time also needs commits submitted to `/rest/devinfo/0.10/bulk` with `issueKeys` that a production deployment shares. Time to restore also needs service desk incident requests ([SERVICE_MANAGEMENT.md](SERVICE_MANAGEMENT.md#operations-incidents-problems-changes)) that were given a resolution inside the window. |
| Sprint report | A scrum board, and a sprint that has started. Future sprints are not offered, so the board needs an active or a completed sprint holding work items. |
| Velocity chart | A scrum board with at least one completed sprint. Each bar needs work that was in the sprint at its start (commitment) and work done before it closed. |
| Cumulative flow diagram | A board whose columns carry statuses, and work its filter shows. Work with no recorded status change counts in its current status from the day it was created. |
| Control chart | Work that moved into an in-progress status and then into a done status, with the done move inside the window. Work created straight into a done status never has a cycle time. |
| Epic report | A board, plus an epic with child work items. Sub-tasks are not counted. |
| Epic burndown | A board with sprints that have started, and an epic with child work items. Each bar is one sprint: what it finished, and what arrived in the epic while it ran. |
| Release burndown | The same, of the work whose fix version is the one chosen. |
| User workload | Work in the project that nobody has resolved. The time each person holds is the sum of the remaining estimates, so work without one counts as a work item and adds no time; the summary says how many carry an estimate. |
| Version workload | The same, of the work whose fix version is the one chosen. It is grouped twice: by the person holding it and by what kind of work it is. |
| Time tracking | Unresolved work with an original estimate, a remaining estimate or logged time. Accuracy is the original estimate less what the work has cost and what it has left, so it is negative when the work has run over. |
| Work by field | Any work in the project, resolved or not: the report is about how the work divides rather than what is left. |
| Version report | A board, plus an unarchived version that work items name in `fixVersions`. |
| Created vs. resolved | Work items created, or given a resolution, on days inside the window. |
| Resolution time | Work items whose current resolution was set on a day inside the window. |
| Service desk report | Requests created on the desk inside the window. CSAT also needs satisfaction ratings, and the breach count needs SLA cycles that have breached. |

Each report also counts only work the viewer can browse, so a demo account needs Browse projects on the project (and, for the service report, agent access to the desk).

## DORA metrics

Builds, deployments and commits arrive through the [Jira Software DevOps APIs](JIRA_SOFTWARE.md#development-and-devops-data); nothing else feeds this report.

| Measure | Calculation |
|---|---|
| Deployment frequency | Number of successful production deployments, plus a weekly rate (`count × 7 ÷ window days`) |
| Lead time for changes | Median, over the commits linked to those deployments, of the time from the commit to the earliest successful production deployment at or after it that shares one of its work items |
| Change failure rate | Failed and rolled-back production deployments divided by all successful, failed and rolled-back production deployments |
| Time to restore service | Median time from an incident's creation to the first change that gave it a resolution |

**Which deployments count**
- **Production:** the deployment's environment `type`, as sent by the provider, must be one the project counts as production. A project counts `production` until its administrators choose otherwise on the report itself (`POST /projects/{key}/reports/dora/mapping`), where they pick from `production`, `staging`, `testing`, `development` and `unmapped`, and may name the pipelines that count -- none named counts every pipeline. The environment name is ignored. The report says what it counted.
- **Identity:** a deployment is its pipeline, environment and `deploymentSequenceNumber`.
- **Latest update:** every accepted build and deployment update is stored and never changed. For each deployment, the report reads the update with the highest `updateSequenceNumber`, and dates it by that update's `lastUpdated`.
- **Window:** that date must fall in the window, which ends at the moment the page is drawn and runs back 7, 30 or 90 days.
- **Visibility:** the deployment's `issueKeys` must name a work item in this project that the viewer can browse.
- **Counted states:** `successful` counts as a deployment; `failed` and `rolled_back` count as failures. `pending`, `in_progress`, `cancelled` and `unknown` count toward neither, though they still appear in the recent list.
- **Time zone:** all timestamps are UTC.

**How lead time is measured**
- Commits come from the development information API, and need an `issueKeys` list and a commit timestamp.
- A commit pairs with a successful production deployment when they share a work item key and the deployment is not earlier than the commit. Each commit contributes one sample, its earliest such deployment.
- At least one shared key must be visible to the viewer in this project.

**What counts as an incident**
- An incident is a Jira Service Management request raised as an incident, which is the operations profile the desk stores. Incident teams, escalations and incident updates use the same definition.
- Labels do not matter. A work item labeled `incident` is not counted unless it was raised as an incident, and an incident still counts if the label is removed.
- The incident counts when the change that gave it a resolution falls in the window; the incident itself may be older.

**The page shows**
- a daily chart of production deployments and failures, with its data table;
- the ten most recent production events, whatever their state.

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

Service project agents and managers see, for the requests the desk received in the window:
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

**Filters.** The page narrows every figure by request type, by channel, and by open or resolved. A request type or channel the desk does not have is 400. See [SERVICE_MANAGEMENT.md](SERVICE_MANAGEMENT.md).

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
- **Recipients:** up to 50 active members of the site, besides the subscriber.
- **Access check at subscription:** the subscriber and every recipient must be able to open the report.
- **Each run:** the report is drawn for each recipient with that recipient's own access, and emailed with a link back to the page.
- **Failures:** if a recipient has lost access or left the site, the run fails.
  - Failed runs are retried with backoff.
  - Recipients who were already sent the email are not sent it again.
  - The failure reason appears under the schedule on the report page.

## Gaps

- DORA counts the incidents Jira Service Management raises; which work items count as incidents cannot be configured.
- DORA has no excluded-period calendars.
- Deployment frequency and cycle time have no page of their own; the DORA report carries deployment frequency as a number and the control chart carries cycle time as a distribution.

Remaining work is tracked in [PLAN.md](../PLAN.md).

## See also

- [AGILE_BOARDS.md](AGILE_BOARDS.md): the boards and sprints the agile reports read.
- [DASHBOARDS.md](DASHBOARDS.md): report gadgets.
- [RELEASES.md](RELEASES.md): delivery evidence for each version.
- [SERVICE_MANAGEMENT.md](SERVICE_MANAGEMENT.md): the service desk report and incidents.
- Code:
  - Store: `internal/store/reports.go` (DORA), `internal/store/agile_reports.go`, `internal/store/board_reports.go`, `internal/store/progress_reports.go`, `internal/store/issue_analysis_reports.go`, `internal/store/service_reports.go`.
  - Web: `internal/web/reports.go`, `internal/web/agile_reports.go`, `internal/web/report_csv.go`, `internal/web/report_compare.go`, `internal/web/report_email.go`.
- Browser tests: `e2e/reports_flow.spec.ts`, `e2e/reports_progress.spec.ts`, `e2e/report_subscriptions.spec.ts`, `e2e/dashboard_reports.spec.ts`.

