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
| Time to restore service | Median time from creation to first Done transition for work items labeled `incident` |

The chart uses accessible SVG with a title and description. A keyboard
reachable table exposes every daily value, and the complete report is covered
in light and dark themes and at 320 px width.

Jira Service Management incident request types currently add the `incident`
label that feeds recovery time; dedicated incident relationships and service
configuration remain. Deployment approvals, configurable
environment mapping, excluded-period calendars, team filters, comparison
periods, targets, exports, subscriptions and scheduled delivery remain.
Burnup, cumulative flow, control chart, created versus resolved and
resolution time reports remain on the active plan.

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
customer request and attention queue. Request-type, organization, channel and
priority segments, comparison periods, exports and scheduled delivery remain.
