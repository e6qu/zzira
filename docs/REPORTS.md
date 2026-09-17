# Reports and delivery metrics

The first project report is available at `/projects/{key}/reports/dora`. It
provides 7, 30 and 90 day views of the four DORA measures, a daily production
delivery chart, its exact tabular data, and recent production events. The
project navigation links directly to the report.

The calculations are permission filtered. An event contributes only when it is
associated with a work item the current user can view in the selected project.
The report uses UTC event timestamps and keeps every accepted build and
deployment update as an immutable fact, while choosing the highest update
sequence for each deployment key.

| Measure | Current calculation |
|---|---|
| Deployment frequency | Count of successful production deployment keys, plus a weekly rate |
| Lead time for changes | Median time from a linked commit to its first later successful production deployment |
| Change failure rate | Failed and rolled-back production events divided by successful, failed and rolled-back terminal events |
| Time to restore service | Median time from creation to first Done transition for Jira Service Management incidents |

The chart uses accessible SVG with a title and description. A keyboard
reachable table exposes every daily value, and the complete report is covered
in light and dark themes and at 320 px width.

Recovery time counts Jira Service Management incidents — requests raised as
incidents — which is how the site's incident teams, escalations and incident
updates already know one. The `incident` label an incident request carries is
not what makes it one, so a work item merely labeled `incident` is not counted,
and an incident whose label is removed still is. Deployment approvals, configurable
environment mapping, excluded-period calendars, and team filters and targets remain.
Every Jira and Agile report named in the plan is now available.

## Previous-period comparisons

DORA metrics, the control chart, created vs. resolved, resolution time and the
service desk report offer Compare with previous period. Each summary figure
then says how it changed from the same length of time just before the window,
such as "Up 2 from 3 in the previous 7 days" or "Shorter by 4h than 1d 2h in
the previous 30 days", computed with the same access, board and filters. A
window with nothing to measure says so rather than showing a change. The CSV
download of a compared report adds a Period column and lists the previous
period's rows before the current ones, and a report email keeps the
comparison. Service requests are compared by when they were created; open and
resolved are their statuses now.

## CSV downloads

Every report page, and the service desk report, has a Download CSV link. It
downloads the data behind the chart for the board, sprint, epic, version,
window and filters on screen, named after the project key, the report and the
day, such as `ZZ-velocity-chart-2026-09-15.csv`. Sprint, epic and version
reports list each work item with its section, estimates and whether it was
added after the start; the control chart lists each work item's cycle time in
hours; the others list one row per day or sprint. The file is sent with
`Cache-Control: no-store`, and a cell that starts with `=`, `+`, `-`, `@`, a
tab or a carriage return is prefixed with an apostrophe so a spreadsheet does
not run it as a formula.

## Report emails

Email this report, beside Download CSV, schedules the report's data every day,
every Monday, or on a cron expression of your own, read in a time zone you
choose, with the board, sprint, epic, version, window and filters on the page. The same report with other choices is a separate
email. The subscriber and every recipient must be able to open the report
when it is scheduled. At each run the report is drawn for each recipient,
through the same route as the download and with their own access, and
emailed with a link back to it. A recipient who can no longer open it, or
who left the site, fails the run; the run is retried with backoff, recipients
already sent are not sent twice, and the reason shows under the schedule on
the report.

## Sprint report and velocity chart

Software projects list two Agile reports beside DORA metrics. Both read a
scrum board of the project, chosen on the page, and use the board's
estimation field; a board without one counts work items. Only work the
viewer can browse contributes, and a project that turns Reports off has no
report pages at all.

`/projects/{key}/reports/sprint` opens the active sprint, or the most
recently completed one, and any started sprint can be chosen. The report
replays the action log from the moment the sprint started to the moment it
completed (or now, while it runs):

| Section | Work |
|---|---|
| Completed work items | In the sprint at the end and done then, but not when it joined |
| Work items not completed | In the sprint at the end and not done |
| Work items completed outside of this sprint | Already done when it joined the sprint |
| Work items removed from sprint | Joined during the sprint and left before the end |

Work added after the start is marked, and an estimate that moved shows its
starting and ending values. The burndown steps remaining estimate down as
work completes and up as work is added, reopened or re-estimated, beside a
guideline that falls to zero at the planned end. A table lists every change.

The report also draws a burnup from the same replay: the sprint's scope as
one step line and the work completed within it as another, so the gap between
them is the work left.

`/projects/{key}/reports/velocity` compares commitment (the estimate of the
work in each sprint when it started) with completed work for the board's
seven most recently completed sprints, oldest first, with the average
completed per sprint and the values as a table.

Sprints record when they actually started and completed, apart from their
planned dates; the Agile sprint bean returns `createdDate` and
`completeDate`. Sprints completed before this was recorded take the times
from their lifecycle actions.


Service managers and agents can open `/service/agent/{desk}/reports` for a
permission-scoped 7, 30, or 90 day service overview. It reports request volume,
current open and resolved counts, requests with any breached SLA cycle, CSAT
average and response count, and an exact daily intake series. The daily chart
has an accessible text alternative and keyboard-reachable data table. SLA
breaches are calculated with the same desk calendar and holidays used on the
customer request and attention queue. The Download CSV link exports the daily
intake series for the filters on screen, and Email this report sends it on a
schedule, with or without a previous-period comparison. The same requests are
broken down by request type, channel, priority and organization. A request
belongs to the desk's organizations its customer is in, which is who Jira
Service Management shares it with, so a customer in several organizations
counts in each; requests with no priority show as None, and requests whose
customer is in none of the desk's organizations show as No organization.

## Cumulative flow diagram and control chart

Both read any board of a software project, scrum or kanban, over the last 14,
30 or 90 days (30 by default; any other window is 400). The work is what the
board's filter shows the viewer, and each item's status history comes from
the action log.

`/projects/{key}/reports/cumulative-flow` counts the board's work in each
column at the end of each day, today included, and stacks the columns with
the first column on top, as Jira draws the diagram. A table lists every
day's counts.

`/projects/{key}/reports/control-chart` places each work item completed in
the window by when it was completed and how long it took: from its first move
into an in-progress status to the move into done that completed it. Reopened
work counts only once it is done again, and work that went straight to done
without starting has no cycle time. The summary gives the number of items and
the average and median cycle time, the chart marks the average, and a table
lists each item.

## Epic report and version report

`/projects/{key}/reports/epic` follows the work in an epic (its child work,
not sub-tasks) and `/projects/{key}/reports/version` the work fixed in an
unarchived version, both through a chosen board's estimation field or by
counting work items. The report replays the action log day by day from the
epic's creation, or from the version's start date or its earliest work, to
today:

- the chart draws the estimate of all the work that existed each day against
  the estimate of the work done, so scope growth and progress show together,
  and a table lists the daily values;
- the summary gives the share of work items done, the total and remaining
  estimate and how many work items have no estimate;
- tables list the completed and incomplete work with their estimates.

Only work the viewer can browse is included, and an epic the viewer cannot see
answers 404.

## Created vs. resolved and resolution time

Every project, whatever its type, has two issue analysis reports over the
last 7, 30 or 90 days (30 by default; any other window is 400), counting only
work the viewer can browse.

`/projects/{key}/reports/created-vs-resolved` counts the work created and the
work resolved each day. As in Jira, resolution uses the date of the current
resolution, so work that was reopened no longer counts as resolved. The chart
draws both counts per day, or as running totals with `cumulative=true`, and a
table lists the daily and running values.

`/projects/{key}/reports/resolution-time` averages, for the work resolved each
day, the time from creation to resolution, with the overall average for the
window. Days without resolved work have no bar, and a table lists each day.

