# Time tracking

When time tracking is on, each work item carries an original estimate, a
remaining estimate and the time logged against it, measured in the site's
working time (hours per day, days per week). Logged work itself is described
in [WORKLOGS.md](WORKLOGS.md). Part of the [Jira platform](JIRA_PLATFORM.md);
see [CLOUD_PARITY.md](CLOUD_PARITY.md) for status.

## Estimates

- Durations use Jira's format (`1w 2d 3h 30m`) or a bare number in the site's
  default unit.
- Set on create or edit with the `timetracking` field, or with the
  `update.timetracking` operation (`set`, `edit`). An empty value removes an
  estimate.
- An original estimate given without a remaining one also sets the remaining
  estimate.
- Issue reads return `timetracking` (formatted and seconds),
  `timeoriginalestimate`, `timeestimate`, `timespent`, their sub-task
  aggregates, `progress`, `aggregateprogress` and `workratio`.
- Estimate changes appear in the changelog as `timeoriginalestimate` and
  `timeestimate`. Both estimates are stored in seconds on `issues`.

## Adjusting the remaining estimate

Logging, changing and deleting work move the remaining estimate according to
the `adjustEstimate` query parameter:

| Mode | Log work | Change work | Delete work |
|---|---|---|---|
| `auto` (default) | reduce by the time logged | move by the change | increase by the time removed |
| `new` | set to `newEstimate` (required) | set to `newEstimate` | set to `newEstimate` |
| `manual` | reduce by `reduceBy` | — | increase by `increaseBy` |
| `leave` | keep | keep | keep |

The estimate never goes below zero, and `auto` leaves an unestimated work item
unestimated. Each change records `timeestimate` and `timespent` changelog
items.

## Notifications

Logging, changing and deleting work raise the Work logged, Worklog updated and
Worklog deleted events for the project's
[notification scheme](NOTIFICATION_SCHEMES.md), unless `notifyUsers=false`.

## JQL

`originalEstimate`, `remainingEstimate` and `timeSpent` compare with durations
(`timeSpent > 4h`); `workRatio` compares percentages. All four support
`IS EMPTY` and `ORDER BY`. See [JQL.md](JQL.md).

## UI

- The work item page shows a time tracking bar (logged, remaining, original),
  an estimate editor (`POST /issues/{key}/timetracking`) and a log work form
  that takes a duration and how to adjust the remaining estimate.
- While time tracking is on, create and edit metadata offer `timetracking`,
  which the default screen carries; the create form takes an original
  estimate.
- Administrators set the provider, working hours, default unit and format
  through the site configuration (see
  [JIRA_SITE_CONFIGURATION.md](JIRA_SITE_CONFIGURATION.md)).

## Tests

`internal/api3/time_tracking_test.go`, `internal/models/durations_test.go`,
`e2e/triage.spec.ts`, `e2e/v3.spec.ts`.

## See also

[WORKLOGS.md](WORKLOGS.md) · [BULK_ISSUES.md](BULK_ISSUES.md) (bulk estimate
edits) · [ISSUE_FIELDS.md](ISSUE_FIELDS.md)
