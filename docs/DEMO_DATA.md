# Demo data

A demo site is built from a scenario: one JSON document that declares a
company's people, projects, work, releases, deliveries, service requests and
knowledge. `demo/company.json` is the company the repository ships; applying it
gives a site with three months of history behind it.

```bash
make demo                                          # apply demo/company.json
go run ./cmd/server -mode=demo -scenario=my.json   # apply your own
```

The people, their passwords and API tokens land in `data/demo-credentials.json`.

## Why it is declarative

The same document builds the site, is checked before anything is written, and is
the fixture the tests read. Changing what a report shows means editing the
scenario, not a script: add a sprint, move a day offset, add an incident.

## Time

Every moment is a **day offset from the day the scenario is applied**, so a
demo site's history always ends today. `-84` is twelve weeks ago; `0` is this
morning; a positive offset is in the future, which is how an unreleased version
gets a release date.

Work is applied through the ordinary command layer — permissions, the action
log, notifications and search all behave as they do for a person — and the
history is then moved into the past. The action log is the record of what
happened, so it is retimed first and each row takes its time from the action
that made it: a work item was created when its creation was logged and resolved
when its resolution was. Reports read that history, so a sprint report, a
cumulative flow diagram and the delivery metrics all have something real to
show.

## What a scenario declares

| Section | What it builds |
| --- | --- |
| `site` | The workspace: its slug and name |
| `groups`, `people` | Accounts, their roles and group membership. A person marked `customer` has no seat on the site and reaches the portal only |
| `hierarchy` | Levels above Epic and the work types on them ([ISSUE_METADATA.md](ISSUE_METADATA.md#work-type-hierarchy)) |
| `customFields` | Fields the work uses, with options for select lists |
| `projects` | Projects with their type and template, components, versions, a board with its sprints, and a service desk with agents and request types |
| `workItems` | Work with its parent, sprint, versions, labels, estimates and field values, plus `events` — the transitions, comments, worklogs, assignments, links, watches and votes that happened to it |
| `deployments` | Deliveries to environments, which the delivery (DORA) report counts ([REPORTS.md](REPORTS.md)) |
| `service` | Customer organizations and the requests they raised, with their conversation and satisfaction rating |
| `wiki` | Spaces, page trees, blog posts and comments |
| `filters`, `dashboards` | The searches and dashboards people saved |

Every entity has an `id` that the rest of the document refers to: a work item
names its `parent` and `sprint`, a deployment names the `workItems` it carried,
a request names its `requestType`.

## Events

An event is one thing that happened on one day:

```json
{ "day": -60, "kind": "transition", "actor": "ravi", "status": "Done", "resolution": "Done" }
{ "day": -59, "kind": "comment", "actor": "ines", "body": "Reviewed and merged." }
{ "day": -58, "kind": "worklog", "actor": "ravi", "seconds": 7200 }
```

The kinds are `transition`, `comment`, `worklog`, `assign`, `link`, `watch` and
`vote`. A transition names the status it moves to; the applier runs the workflow
transition that leads there, so conditions, validators and post functions all
run. A resolution is taken from the transition screen when it asks for one, and
recorded as an edit when it does not.

## What is checked before anything is written

Applying a scenario that contradicts itself is refused rather than half-built:
unknown people, projects, sprints, versions, components or fields; duplicate
ids; events before the work existed or out of order; a released version with no
release date; a satisfaction rating outside 1 to 5; a sprint that ends before it
starts.

## Tests

`internal/demo` checks that a scenario survives being written and read again,
that each contradiction above is refused, and that the demo company applies and
then reads back through all four products: Jira sees the work and its
changelog, Jira Software sees the board and its closed sprints, Jira Service
Management sees the requests, and Confluence sees the pages. It also checks
that work above the epic level stays off the board and the backlog.

## See also

- [REPORTS.md](REPORTS.md) — what each report needs in order to show something
- [AGILE_BOARDS.md](AGILE_BOARDS.md) — boards, backlogs and sprints
- [SERVICE_MANAGEMENT.md](SERVICE_MANAGEMENT.md) — desks, requests and SLAs
- [CLOUD_PARITY.md](CLOUD_PARITY.md) — what is built
