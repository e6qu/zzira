# Jira Software

Jira Software adds agile planning and delivery tracking to Jira projects: epics, one rank order for the whole site, boards and sprints, estimation, the project timeline, cross-project plans, development and DevOps data, and Jira expressions. For status against Jira Cloud, see [CLOUD_PARITY.md](CLOUD_PARITY.md). For the platform underneath, see [JIRA_PLATFORM.md](JIRA_PLATFORM.md).

| Surface | Doc |
|---|---|
| Boards, backlog, sprints | [AGILE_BOARDS.md](AGILE_BOARDS.md) |
| Release hub, release notes, delivery evidence | [RELEASES.md](RELEASES.md) |
| Version REST API | [PROJECT_VERSIONS.md](PROJECT_VERSIONS.md) |
| Agile, issue analysis and DORA reports | [REPORTS.md](REPORTS.md) |
| Dashboards: browser, gadgets, wallboards, emails | [DASHBOARDS.md](DASHBOARDS.md) |
| Dashboard REST API | [DASHBOARDS_API.md](DASHBOARDS_API.md) |
| Search (`sprint`, `fixVersion`, the epic and rank clauses) | [JQL.md](JQL.md) |

## Epics

An epic is a work item whose work type is at hierarchy level 1. Every site gets Jira's Epic work type. Standard work items can take an epic as their parent on create, on edit, and through the Agile move operations. The epic can be in any project, as in company-managed Jira. A sub-task's parent must be in the sub-task's own project.

A parent always sits exactly one level above its child in the site's [work type hierarchy](ISSUE_METADATA.md#work-type-hierarchy), and a parent from any other level is refused. So an epic takes its own parent from the level above Epic, once a site administrator adds one.

An epic has three extra fields:
- **Name.** Defaults to the summary.
- **Color.** One of `color_1` to `color_14`; any other value is 400.
- **Done flag.**

Deleting an epic keeps its work items. They lose their parent, and that change is recorded in the action log.

| Operation | Behavior |
|---|---|
| `GET/POST /rest/agile/1.0/epic/{epicIdOrKey}` | GET reads the epic. POST is a partial update of name, summary, color and done. A work item that is not an epic is 404. |
| `GET/POST /rest/agile/1.0/epic/{epicIdOrKey}/issue` | GET lists the epic's standard work items in rank order, with a JQL filter. POST moves up to 50 work items into the epic. |
| `GET/POST /rest/agile/1.0/epic/none/issue` | GET lists work items that have no epic. POST removes the epic parent. |
| `PUT /rest/agile/1.0/epic/{epicIdOrKey}/rank` | Ranks an epic before or after another epic. |
| `GET /rest/agile/1.0/board/{boardId}/epic`, `…/epic/{epicId or none}/issue` | Lists the epics in the board's project (optionally filtered by `done`) and their work items. |
| `GET /rest/software/1.0/epic/{epicIdOrKey or none}/issue` | The same reads, paged with `nextPageToken`. |

## Ranking

There is one rank order for the whole site. `PUT /rest/agile/1.0/issue/rank` places up to 50 work items before or after a reference work item and keeps their relative order. `POST` on the same path does the same thing.

Ranking a work item needs **Schedule issues** in its project, as in Jira. The response is 204 when every work item is ranked, and 207 with one entry per work item otherwise: 404 for one the caller cannot see, 403 for one whose project withholds Schedule issues, 400 for one ranked relative to itself.

The only accepted `rankCustomFieldId` is `10019`, Jira's Rank field. Board configuration reports that same id. Ranking on a board, and every other board write, is in [AGILE_BOARDS.md](AGILE_BOARDS.md#permissions).

## Estimation

Every site has Jira's **Story point estimate** number field.
- **Scrum boards** estimate with it. Their configuration reports `{"type": "field", "field": {"fieldId": …, "displayName": "Story point estimate"}}`. A scrum board with no estimation field reports `issueCount`.
- **Kanban boards** report no estimation.

`GET/PUT /rest/agile/1.0/issue/{issueIdOrKey}/estimation?boardId=…` reads and writes the board's estimation field for a work item on that board, whether or not the field is on its screens.

`GET /rest/agile/1.0/issue/{issueIdOrKey}` and every Agile issue list include these Jira Software fields: `sprint` (the open sprint), `closedSprints`, `flagged` and `epic`.

## Timeline

Software projects have a **Timeline** at `/projects/{key}/timeline`. It lists the project's epics in rank order, each followed by its child work items; sub-tasks appear under their parents. It shows only work items the viewer can browse.

Dates come from Jira's **Start date** field, which every site has, and the **Due date** system field.

**Month range**
- Months run from the earliest scheduled date to the latest, and always cover at least three months.
- If nothing is scheduled, the range starts at the current month.

**Bars**
- A bar runs from the start date to the due date.
- A work item with only one of the two dates gets a lighter, open-ended bar that runs to the edge of the timeline.
- Unscheduled work items have no bar.
- A line marks today.

**Scheduling**
- Each row's **Schedule** form (`POST /projects/{key}/timeline`) sets both dates through the normal edit path, so it needs **Edit issues** for the start date and **Schedule issues** for the due date.
- Workflow editability and field configuration apply: a status that sets `jira.issue.editable` to false refuses the form.
- A due date earlier than the start date is refused, as is a start date on a site with no Start date field.

If the project's Roadmap feature (`jsw.classic.roadmap`) is off, the Timeline is removed from navigation and its page returns 404.

## Plans

Plans (Jira's Advanced Roadmaps) collect work from several projects into one schedule.

**REST** (site administrators only), under `/rest/api/3/plans/plan`:

| Operation | Behavior |
|---|---|
| `GET` | Lists plans, with `includeTrashed`, `includeArchived` and cursor paging. |
| `POST` | Creates a plan. |
| `GET/PUT …/{planId}` | Reads a plan. `PUT` applies a JSON Patch, then validates the whole plan again. |
| `PUT …/{planId}/archive`, `…/trash` | Changes the status of an active plan. |
| `POST …/{planId}/duplicate` | Copies the plan's sources and teams. Scenarios are not copied. |
| `GET …/{planId}/team` | Lists the plan's teams. |
| `POST …/team/atlassian`, `…/team/planonly` | Adds a team. |
| `GET/PUT/DELETE …/team/{atlassian or planonly}/{teamId}` | Reads, updates or removes one team. |

**A plan document holds:**
- **Issue sources.** 1 to 100 boards, projects or saved filters. A board source uses the board's filter, limited to the board's project.
- **Exclusion rules.** By work item, work type, status, status category or release, plus how many days completed work stays visible. Sub-tasks are always excluded.
- **Scheduling.** The start and end fields: Due date, Target start, Target end, or any date custom field. Every site gets Target start and Target end fields. Also the estimation unit (StoryPoints, Days or Hours) and whether dependencies are Sequential or Concurrent.
- **Permissions.** View or Edit, granted to groups or accounts.

**Browser pages**
- `/plans` lists the active plans the viewer can open, and, for site
  administrators, creates one: a name and the boards, projects and filters it
  reads. The creator becomes the plan lead.
- `/plans/{id}/settings` is the plan's setup, for anyone who may edit it: its
  name and lead, its issue sources, its exclusion rules (how long completed
  work stays visible, and the work types and statuses to leave out), and who
  else may view or edit it.
- `/plans/{id}` shows the plan's work as a table (Work, Start, End, Team, Sprint, Estimate, Dependencies) beside a month timeline. Epics are nested with their child work items.
- Who can open a plan: site administrators, the plan lead, and people or groups the plan grants access to.

**Scenarios**
- Each plan has a Default scenario. You can add more scenarios, blank or copied from another, and rename, recolor or delete them. Switch with `?scenario=`.
- In a scenario you can edit a work item's summary, start date, end date, team, sprint and estimate. These edits stay in the scenario and are not saved to the work item.
- **Review changes** (`/plans/{id}/review`) saves the chosen edits to Jira through the normal edit path, or discards them.

**Teams**
- **Atlassian teams** are managed at `/teams` and `/teams/{id}`. **Plan-only teams** exist only inside one plan.
- Every site has Jira's **Team** field.
- Each team plans as Scrum or Kanban:
  - **Scrum:** capacity is compared with estimates per sprint of the team's board. The default capacity is 30 points per sprint.
  - **Kanban:** capacity is compared per week across 12 weeks, using time estimates.
- Capacity can be overridden per iteration and per scenario (`POST /plans/{id}/capacity`).

**Dependencies**
- Only links of the **Blocks** type count as dependencies.
- A dependency is off track when:
  - the blocker ends after the blocked work item starts, or
  - the blocker is in a later sprint, or
  - both are in the same sprint and dependencies are Sequential.

## Development and DevOps data

Development information, builds and deployments are attached to work items and roll up into [releases](RELEASES.md) and the [DORA report](REPORTS.md#dora-metrics):
- Development information: `/rest/devinfo/0.10`
- Builds: `/rest/builds/0.1`
- Deployments: `/rest/deployments/0.1`

Each of these also answers under its `/jira/…/cloud/` alias. Deployment gating by a service desk is described in [SERVICE_MANAGEMENT.md](SERVICE_MANAGEMENT.md).

| Module | Base path | Entities |
|---|---|---|
| Operations | `/rest/operations/1.0` | incidents, post-incident reviews, linked workspaces |
| Security | `/rest/security/1.0` | vulnerabilities, linked workspaces |
| DevOps components | `/rest/devopscomponents/1.0` | components |
| Feature flags | `/rest/featureflags/0.1` | flags |
| Remote links | `/rest/remotelinks/1.0` | remote links |

**Bulk submission (`POST …/bulk`)**
- Every entity is validated against Jira's schema.
- The response is 202 and lists the accepted ids, the failed ids with their messages, and the unknown issue associations.
- An entity whose only associations are unknown work items is rejected.
- A stored entity is replaced only by an update with a higher update sequence number.
- The stored document is exactly what the provider submitted.

**Reads and deletes**
- Entities are read and deleted by id.
- `DELETE …/bulkByProperties` removes the module's data that carries every given property.

Errors use the module format, `[{"message": …}]`. Provider rate limits are described in [APPS.md](APPS.md#devops-provider-rate-limits).

## Jira expressions

`internal/jexpr` implements Jira's expression language:
- Literals, template literals, arithmetic and comparison.
- `&&`, `||`, `??`, optional chaining and conditionals.
- Arrow functions, and lists and maps with spread.
- `new Date`, `new Map`, `new Issue`, `new User` and `new Project`.
- The list, string, map, date, `Math` and `JSON` functions.

| Operation | Behavior |
|---|---|
| `POST /rest/api/3/expression/analyse` | `check=syntax` reports parse errors with line and column. `type` infers Jira types such as `List<String>` and reports unknown properties. `complexity` returns the formula for expensive operations, for example `N` for `issues.map(i => i.comments)`. |
| `POST /rest/api/3/expression/eval` | Evaluates an expression. **Context:** `user`, `issue`, `project`, `sprint`, `board`, `serviceDesk` and `customerRequest`. **JQL-loaded `issues`:** paged by `startAt`, with `strict`, `warn` or `none` validation. **Custom variables:** `json`, `user`, `issue` and `list`. |
| `POST /rest/api/3/expression/evaluate` | The same, but JQL-loaded work items are paged with `nextPageToken`. |

**Limits.** Jira's limits are enforced: 10,000 steps, 10 expensive operations, 1,000 beans and 10,000 primitive values. `expand=meta.complexity` reports usage.

**Expensive operations.** Loading any of these for a work item is an expensive operation: comments, attachments, worklogs, changelogs, links, sub-tasks, stories, parent, epic, properties, sprints, votes or watches.

**Results and access**
- Entities come back as their Jira REST representations.
- Every read follows the caller's work item, comment and project permissions.
- Anonymous evaluation has a null user and sees no work items.

## Gaps

- Plans:
  - No auto-scheduler.
  - No creating or configuring a plan in the browser (sources, exclusions, permissions and teams are REST-only).
  - No restoring an archived or trashed plan.
  - `inferredDates` and plan `customFields` are stored but not used.
  - No capacity derived from velocity.
  - No saved views, grouping, filters or rollups.
- Scenarios cannot change parent, rank, release or status, and cannot create work items.
- Cross-project releases: the plan's `crossProjectReleases` list is validated and stored, but nothing shows it or plans against it. There is no multi-project release in the plan or the release hub.
- No REST teams API (Atlassian's public teams API).

Remaining work is tracked in [PLAN.md](../PLAN.md).

## See also

- Code:
  - `internal/agile/software.go`, `internal/store/jira_software.go`
  - `internal/api3/plans.go`, `internal/store/plans.go`, `internal/store/plan_*.go`, `internal/web/plan_planning.go`
  - `internal/api3/software_providers.go`, `internal/api3/expressions.go`, `internal/jexpr`
- Browser tests: `e2e/software.spec.ts`, `e2e/timeline.spec.ts`, `e2e/plans_view.spec.ts`, `e2e/plans_teams.spec.ts`, `e2e/plans_setup.spec.ts`
