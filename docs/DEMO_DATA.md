# Demo data

A demo site is built from a scenario: one JSON document that declares a
company's people, projects, work, releases, deliveries, service requests and
knowledge. `demo/company.json` is the company the repository ships; applying it
gives a site with three months of history behind it, and four dashboards a
site like it would keep: delivery health (the DORA metrics and the deployments
behind them), this sprint, support and operations, and each person's own work.

```bash
make demo                                          # apply demo/company.json
go run ./cmd/server -mode=demo -scenario=my.json   # apply your own
```

The people, their passwords and API tokens land in `data/demo-credentials.json`.

## Which workspace it builds into

An instance serves exactly one workspace, the one `WORKSPACE_SLUG` names, so
seeding a deployment means naming that one. `-mode=demo` builds into, in order:

1. `-workspace`, the operator's one-off override;
2. `WORKSPACE_SLUG`, the workspace this deployment serves;
3. the scenario's own `site.slug`, when neither is set.

Only the slug is taken; the site keeps the scenario's display name. A site that
is already there keeps its name and everything on it.

```bash
WORKSPACE_SLUG=acme go run ./cmd/server -mode=demo     # seed the served site
go run ./cmd/server -mode=demo -workspace=sandbox      # seed another one
```

`make demo` and the `docker compose` recipe in the [README](../README.md) both
pass the workspace their server serves, so the company appears where you then
look for it.

Applying a scenario to the wrong workspace is silent, not loud: the server
shows the one it serves and nothing else, so a company built next to it simply
never appears.

## Running it again

Editing the scenario and applying it again is how you build your own company,
so a second run adds what the scenario has gained and keeps what it already
built. Everything is found by its natural key: the site by its slug, a person
by their email, a group, a hierarchy level, a component, a version, a sprint,
a filter and a dashboard by name, a project by its key, a space by its key, a
page and a blog post by their title, and a work item or a service request by
its summary within its project.

So a second run:

- raises only work items the scenario has gained, each once;
- replays no history onto work that already has it, leaves comments,
  transitions and worklogs alone, and re-times nothing that already happened;
- leaves a closed sprint closed and a released version released;
- reports the credentials of everyone in the scenario, including the people
  whose accounts it kept.

It does not undo an edit: a work item removed from the scenario, or renamed,
stays on the site under its old summary. Start from an empty database when you
want exactly what the document says.

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
| `filters`, `dashboards` | The searches people saved, and the dashboards they keep, with the gadgets on them. A gadget names its `type` (the catalog key without `com.zzira:`) and whatever that kind reads: a `project` and `days` window, a scrum `board`, a saved `filter` or `jql`, a `groupBy` and `yGroupBy`, `dateField` or `cumulative` |

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
that each contradiction above is refused, that a scenario builds into the
workspace it is given rather than its own, that a second run raises only what
the scenario has gained, and that the demo company applies and then reads back
through all four products: Jira sees the work and its
changelog, Jira Software sees the board and its closed sprints, Jira Service
Management sees the requests, and Confluence sees the pages. It also checks
that work above the epic level stays off the board and the backlog.

## See also

- [REPORTS.md](REPORTS.md) — what each report needs in order to show something
- [AGILE_BOARDS.md](AGILE_BOARDS.md) — boards, backlogs and sprints
- [SERVICE_MANAGEMENT.md](SERVICE_MANAGEMENT.md) — desks, requests and SLAs
- [CLOUD_PARITY.md](CLOUD_PARITY.md) — what is built
