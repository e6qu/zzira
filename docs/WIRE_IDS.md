# Ids clients see

Clients of the Jira, Jira Software and Jira Service Management APIs see only the ids those products use. ZZIRA stores its own ids internally; each kind of item also carries its Jira id, and every API translates at its boundary. Requests accept the Jira id (the stored id still resolves), and no response contains a stored id. Part of the [Jira platform](JIRA_PLATFORM.md). For status, see [CLOUD_PARITY.md](CLOUD_PARITY.md).

| Item | Id clients see | Notes |
| --- | --- | --- |
| Work item | numeric, from 10000 | keys also resolve |
| Project | numeric, from 10000 | |
| Work type, priority, resolution | Jira's numbers | see [ISSUE_METADATA.md](ISSUE_METADATA.md) |
| Status | numeric | To Do 10000, In Progress 3, Done 10001; site statuses from 10002 |
| Workflow | UUID | the `id` in workflow search, preview, scheme mappings, capabilities and usage |
| Filter | numeric, from 10000 | the built-in "All issues" filter is Jira's system filter -4 |
| Board | numeric, from 1 | |
| Board filter | numeric | each board has its own; `GET /rest/api/3/filter/{id}` returns the board's query |
| Sprint | numeric, from 1 | `originBoardId` is the board's numeric id |
| Quick filter | numeric | kept when a board's quick filters are saved again |
| Comment, work item link, link type, remote link, attachment, worklog | numeric | see [ISSUE_SURFACE.md](ISSUE_SURFACE.md) |
| Workflow scheme | numeric, from 10000 | scheme beans, bulk reads, project associations, switch requests, task results |
| Service request comment | numeric | in the bean and its `_links.self` |
| Asynchronous task | numeric string, from 10000 | `/rest/api/3/task/{id}`, bulk `taskId`, `Location` headers, archived work item exports, Confluence long tasks |

## Where ids are translated

- **Status ids:** work item beans, transitions, changelogs, the status APIs, workflow search and preview (`id`, `statusReference`, `toStatusReference`, `fromStatusReference`), workflow validation and updates, workflow scheme status mappings, bulk transition discovery, board configuration, service request transitions and automation rules.
- **JQL:** numeric ids work wherever Jira accepts them. `status = 10000`, `project = 10000` and `sprint = 1` match like the name, key or sprint ([JQL.md](JQL.md)).
- **Metadata:** parent candidates in create and edit metadata are work item ids.
- **Service requests:** attachment links use attachment ids.
- **Task results** name workflow schemes and projects by their Jira ids.

## Code and tests

- `migrations/166_jira_wire_ids.sql`: the id columns and sequences.
- `internal/api3/wire_ids.go`, `internal/store/jql_wire_ids.go`: translation.
- `e2e/wire-ids.spec.ts`: crawls the read APIs of all three products on a seeded site and fails if any id, id list, status reference or URL holds a stored id. It covers projects, statuses, workflows, schemes, work items with transitions and changelogs, search, filters, dashboards, fields, screens, boards with configuration, sprints, backlog, quick filters, and service desks with queues and request types.
