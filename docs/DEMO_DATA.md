# Demo data

A demo site is built from a scenario: one JSON document that declares a
company's people, projects, work, releases, deliveries, service requests and
knowledge. `demo/company.json` is the company the repository ships: Northwind, a business
that has been running for three years. Applying it gives a site with that
history behind it -- 37 people in eight teams, nine projects, hundreds of
sprints and releases, thousands of pieces of work with the worklogs, comments
and deployments that went with them, a support desk with years of requests,
and four dashboards a site like it would keep: delivery health (the DORA
metrics and the deployments behind them), this sprint, support and operations,
and each person's own work. Building it takes a few minutes, because every
piece of it is written through the same commands a person's clicks would run.

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
| `projects` | Projects with their type and template, components, versions, a board with its sprints, and a service desk with agents, request types and the queues its agents work from |
| `workItems` | Work with its parent, sprint, versions, labels, `estimate`, `due` day and field values, plus `events` — the transitions, comments, worklogs, assignments, links, watches and votes that happened to it |
| `deployments`, `commits` | Deliveries to environments and the changes they carried, which the delivery (DORA) report counts: deployments give the frequency and the failure rate, and commits paired with them give the lead time ([REPORTS.md](REPORTS.md)) |
| `plans` | Cross-project plans, the teams that work in them, and the cross-project `releases` that group versions from several projects into one delivery |
| `translations` | What the site calls a work type, a priority, a resolution or a status in the other languages its people read ([ISSUE_METADATA](ISSUE_METADATA.md#translating-the-words-on-a-work-item)) |
| `automation` | The rules the company runs: what starts them, what they are scoped to, and what they do |
| `service` | Customer organizations with their members and the `desks` they are customers of, the requests they raised with their conversation and satisfaction rating, and `assets` — the desk's inventory: schemas, the objects in them, and what each object needs from the others |
| `wiki` | Spaces, page trees, blog posts and comments, and the `content` a space holds beside them: whiteboards with their objects and the lines between them, databases with their columns, records and views, folders, and embedded pages |
| `filters`, `dashboards` | The searches people saved, and the dashboards they keep, with the gadgets on them. A gadget names its `type` (the catalog key without `com.zzira:`) and whatever that kind reads: a `project` and `days` window, a scrum `board`, a saved `filter` or `jql`, a `groupBy` and `yGroupBy`, `dateField` or `cumulative` |

## Generated history

Three years of a company is not something anybody writes out by hand, so the
scenario declares the *shape* of its history and the generator writes it:

```json
"generate": {
  "seed": 20260922, "days": 1092, "sprintDays": 14,
  "teams": [{ "name": "Payments", "members": ["ravi", "ines"], "projects": ["pay"] }],
  "projects": [{ "project": "pay", "board": "pay-board", "workPerSprint": 9,
                 "releaseEvery": 3, "deploymentsPerWeek": 5, "failureRate": 0.07,
                 "bugShare": 0.3, "points": "points", "impact": "impact",
                 "linkShare": 0.18, "watchShare": 0.25,
                 "themes": ["card payments", "refunds"] }],
  "service": { "project": "help", "requestsPerWeek": 6, "incidentShare": 0.18 },
  "knowledge": { "space": "ENG", "pagesPerMonth": 2.5, "postsPerQuarter": 2 }
}
```

- The same `seed` gives the same company every time, so a test can be written
  against it.
- Generated history fills the years *before* whatever the scenario declares by
  hand: a curated sprint or release is the recent, readable part, and the
  generator never runs a second sprint alongside it.
- Generated releases are numbered above the highest version declared by hand,
  so the two never collide.
- Expanding consumes the plan: the scenario that reaches the applier has every
  sprint, release, work item, deployment, commit, request and page declared.
- `points` names the field the work is estimated in. Without it a board has
  nothing to draw a velocity, a burndown or a sprint health from, which is most
  of what a board is for.
- `impact` names a select field the work carries, so the site has custom field
  values to group, filter and report on. A scenario names an option by its
  value -- "Several customers" -- and the applier writes it the way an option
  field reads it.
- `linkShare` and `watchShare` are how much of a sprint's work is linked to the
  work beside it and watched by somebody. A link points backwards, at work
  already raised, because the timeline is replayed in order.
- `service` is one desk and `services` are the rest: a company with an external
  desk and an internal one has two queues filling over the same years, each
  with its own agents, request types and customers. An internal desk's
  customers are the people who work here, and raising a request enrols them.
- `approvalType` is the request type somebody has to approve, `approvers` the
  people who answer, and `assets` the objects an incident is about. Most access
  requests are approved, a few refused and closed, and a few left waiting.
- A share of the generated bugs are the outages the team had: labelled
  `incident`, raised at the highest priority and always resolved. Each project
  has one in the last month, because a delivery report whose time to restore
  says "No data" reads as a report that does not work. A project says what it
  counts as an incident with `incidentJql`.
- Generated work carries an `estimate` like curated work does, and about a
  third of it is `due` on a day, so the time tracking, user workload, version
  workload and calendar surfaces have something to show for every year of the
  history rather than only for the curated weeks.

Every entity has an `id` that the rest of the document refers to: a work item
names its `parent` and `sprint`, a deployment names the `workItems` it carried,
a request names its `requestType`.

## Estimates and due dates

`estimate` is the original estimate, written the way Jira writes one -- `2d`,
`4h`, `1w 2d` -- in the site's working time of eight hours a day and five days
a week. The applier raises the work carrying it as both the original and the
remaining estimate, so a `worklog` event afterwards moves the remaining
estimate down exactly as logging work in the site does, and work that logs more
than it estimated reads as over-run in the time tracking report.

`due` is a day offset, as a version's `releaseDay` is: `"due": -2` is work that
was due the day before yesterday and is still open. It cannot fall before the
day the work was raised.

## Events

An event is one thing that happened on one day:

```json
{ "day": -60, "kind": "transition", "actor": "ravi", "status": "Done", "resolution": "Done" }
{ "day": -59, "kind": "comment", "actor": "ines", "body": "Reviewed and merged." }
{ "day": -58, "kind": "worklog", "actor": "ravi", "seconds": 7200 }
{ "day": -57, "minutes": 48, "kind": "transition", "status": "Done", "resolution": "Done" }
{ "day": -57, "kind": "approval", "actor": "sara", "body": "Manager approval", "approvers": ["nora"] }
{ "day": -56, "kind": "approve", "actor": "nora" }
```

The kinds are `transition`, `comment`, `worklog`, `assign`, `link`, `watch`,
`vote`, and, on a service request only, `approval`, `approve` and `decline`. A
transition names the status it moves to; the applier runs the workflow
transition that leads there, so conditions, validators and post functions all
run. A resolution is taken from the transition screen when it asks for one, and
recorded as an edit when it does not.

`minutes` is how far into the working day an event happened, for the times a
day is too coarse to say what happened: an outage found at nine and over by ten
is fifty minutes of recovery, which is what the delivery report's time to
restore reads. Events inside a day are still replayed in the order they are
written.

`asset` connects the request to something in the desk's inventory, as
`"affected"` unless the event says `"role": "depends_on"`. What depends on that
object is then reached through the relationships, so an incident on a service
shows what else it takes down.

A comment on a service request is a reply through the desk: public unless the
event says `"internal": true`, which is an agent's note the customer never sees.
A public reply stops the desk's time to first response; a note leaves it
running, as it does in the product. `approval` asks the people it names to
approve, and `approve` or `decline` is one of them answering, so the actor has
to be one of the approvers.

## What is checked before anything is written

Applying a scenario that contradicts itself is refused rather than half-built:
unknown people, projects, sprints, versions, components or fields; duplicate
ids; events before the work existed or out of order; a released version with no
release date; a satisfaction rating outside 1 to 5; a sprint that ends before it
starts; an estimate that is not a duration; work due before it was raised; a
queue whose query does not parse; a request answered by somebody who is not an
agent of that desk; an approval answered before anybody asked for one.

## Tests

Every gadget in the catalog is on one of the company's dashboards, and a test
holds it there: a gadget nobody has put on a dashboard is one nobody has
looked at.

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
