# Ids clients see

Clients of the Jira, Jira Software and Jira Service Management APIs only ever
see the ids those products use. The product stores its own ids internally; each
kind of item also carries the id Jira uses, and every API translates at its
boundary. Requests accept the Jira id, and the stored id still resolves for
compatibility, but no response contains a stored id.

| Item | Id clients see | Notes |
| --- | --- | --- |
| Issue | numeric, from 10000 | keys resolve too |
| Project | numeric, from 10000 | older non-numeric project ids were rewritten to numeric ones |
| Issue type, priority, resolution | Jira's numbers | see [Issue metadata](ISSUE_METADATA.md) |
| Status | numeric | To Do is 10000, In Progress 3, Done 10001; a site's own statuses count from 10002 |
| Workflow | UUID | the `id` in workflow search, preview, scheme mappings, capabilities and usage |
| Filter | numeric, from 10000 | the built-in "All issues" filter is Jira's system filter -4 |
| Board | numeric, from 1 | |
| Board filter | numeric | each board has its own filter id; `GET /rest/api/3/filter/{id}` returns the board's query over its project |
| Sprint | numeric, from 1 | `originBoardId` is the board's numeric id |
| Quick filter | numeric | kept when a board's quick filters are saved again |
| Comment, issue link, link type, remote link, attachment, worklog | numeric | see [Issue surface](ISSUE_SURFACE.md) |

## Where ids are translated

- Status ids appear in issue beans, transitions, changelogs, the status APIs,
  workflow search and preview (`id`, `statusReference`, `toStatusReference`,
  `fromStatusReference`), workflow validation and updates, workflow scheme
  status mappings, bulk transition discovery, board configuration, service
  request transitions and automation rules.
- JQL accepts numeric ids wherever Jira does: `status = 10000`,
  `project = 10000` and `sprint = 1` match exactly as the status name, project
  key or sprint would.
- Parent candidates in create and edit metadata are issue ids.
- Service request attachment links use attachment ids.

## Keeping it that way

`e2e/wire-ids.spec.ts` crawls the read APIs of all three products on a seeded
site — projects, statuses, workflows, schemes, issues with their transitions and
changelog, search, filters, dashboards, fields and screens, boards with their
configuration, sprints, backlog and quick filters, and service desks with their
queues and request types — and fails if any id, list of ids, status reference or
URL holds a stored id.

## Evidence

- `migrations/166_jira_wire_ids.sql`
- `internal/api3/wire_ids.go`, `internal/store/jql_wire_ids.go`
- `e2e/wire-ids.spec.ts`
