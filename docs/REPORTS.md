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
Additional Jira and Agile reports—sprint, velocity, burndown, burnup,
cumulative flow, control chart, created versus resolved and resolution
time—remain on the active plan.
