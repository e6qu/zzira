# Time tracking

When time tracking is on for the site, each work item carries an original
estimate, a remaining estimate and the time logged against it, measured in the
site's working time (hours per day and days per week).

## Estimates

- Estimates are written as Jira durations — weeks, days, hours and minutes such
  as `1w 2d 3h 30m` — or a bare number in the site's default unit. They are set
  with the `timetracking` field when creating or editing a work item, or with
  the `update.timetracking` edit operation; an empty value removes an estimate.
- An original estimate given without a remaining one also starts the remaining
  estimate, as in Jira.
- Issue reads return `timetracking` with formatted and second values,
  `timeoriginalestimate`, `timeestimate`, `timespent`, their sub-task
  aggregates, `progress`, `aggregateprogress` and `workratio`. Estimate changes
  appear in the changelog as `timeoriginalestimate` and `timeestimate`.
- `migrations/189_issue_time_estimates.sql` stores both estimates in seconds.

## Logging work

Logging, changing and deleting work move the remaining estimate as the request's
`adjustEstimate` says:

| Mode | Log work | Change work | Delete work |
|---|---|---|---|
| `auto` (default) | reduces it by the time logged | moves it by the change | increases it by the time removed |
| `new` | sets it to `newEstimate` | sets it to `newEstimate` | sets it to `newEstimate` |
| `manual` | reduces it by `reduceBy` | — | increases it by `increaseBy` |
| `leave` | keeps it | keeps it | keeps it |

The estimate never goes below zero, and a work item without a remaining estimate
keeps none under `auto`. Each change records `timeestimate` and `timespent`
changelog items. Time is given as `timeSpent` or `timeSpentSeconds`.

Logging work needs Work on issues; changing work needs Edit all worklogs, or Edit
own worklogs for the caller's own; deleting work needs Delete all worklogs or
Delete own worklogs.

## JQL

`originalEstimate`, `remainingEstimate` and `timeSpent` compare with durations
(`timeSpent > 4h`), `workRatio` with percentages, and all four support
`is EMPTY` and ordering.

## Browser

The issue page shows a time tracking bar with logged, remaining and original
time, an estimate editor, and a log work form that takes a duration and how to
move the remaining estimate.

## Evidence

- `internal/api3/time_tracking_test.go` covers estimates on create and edit,
  bean fields and aggregates, every adjustment mode, permissions, changelog items
  and JQL.
- `internal/models/durations_test.go` covers duration parsing and formatting.
- `e2e/triage.spec.ts` and `e2e/v3.spec.ts` log work through the issue page.
