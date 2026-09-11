# Jira Software boards

Updated: 2026-09-11

ZZIRA serves a project's board, its columns and quick filters, the work on it,
its backlog and sprints, the board's project and versions, its properties, and
the reads Jira also publishes under `/rest/software/1.0`. Contributors use the
board at `/board/{id}`.

## Jira Cloud REST surface

Twenty-nine of the 33 pinned board operations are implemented. An audit against
a running server found eight already working and the rest returning 404,
including the entire `/rest/software/1.0` base path, which was not mounted.

| Method and path | Behavior |
|---|---|
| `GET /rest/agile/1.0/board` | Lists the workspace's boards. |
| `GET /rest/agile/1.0/board/{boardId}` | Reads one board with its project location. |
| `GET /rest/agile/1.0/board/{boardId}/configuration` | Reports the board's columns, their statuses and constraint. |
| `GET /rest/agile/1.0/board/{boardId}/issue` | Pages the work on the board in column order. |
| `GET /rest/agile/1.0/board/{boardId}/backlog` | Pages the board's backlog. |
| `GET /rest/agile/1.0/board/{boardId}/sprint` | Lists the board's sprints. |
| `GET /rest/agile/1.0/board/{boardId}/sprint/{sprintId}/issue` | Pages one sprint's work; a sprint on another board is a 404. |
| `GET /rest/agile/1.0/board/{boardId}/quickfilter` and `/quickfilter/{id}` | Reads the board's quick filters. |
| `GET /rest/agile/1.0/board/{boardId}/project` and `/project/full` | Reports the board's project, the full form adding its type and style. |
| `GET /rest/agile/1.0/board/{boardId}/version` | Lists the project's versions with Jira's `released` filter. |
| `GET /rest/agile/1.0/board/{boardId}/epic` and `/epic/none/issue` | Reports the board's epics and the work with none. |
| `GET/PUT /rest/agile/1.0/board/{boardId}/features` | Reports the board's features. |
| `GET /rest/agile/1.0/board/{boardId}/reports` | Lists the reports the board type offers. |
| `GET /rest/agile/1.0/board/{boardId}/properties` | Lists the board's property keys. |
| `GET/PUT/DELETE /rest/agile/1.0/board/{boardId}/properties/{propertyKey}` | Reads, stores, or removes one property; a new key answers 201 and a replacement 200. |
| `GET /rest/software/1.0/board/{boardId}/…` | Serves the backlog, issue, epic-none and sprint reads above, plus `backlog/approximate-count` and `issue/approximate-count`. |

Both base paths reach one implementation, so a client cannot get different
answers depending on which it calls.

## Where the model differs from Jira

**There is no epic work type.** ZZIRA's hierarchy is one parent level with
sub-tasks. The epic list is therefore empty, every board issue correctly counts
as having no epic, and a specific epic's issues is a 404. That is an accurate
report of this hierarchy rather than a stub.

**Board features follow the board's own configuration.** Sprints track the board
type, swimlanes track the swimlane strategy, and the backlog is always present.
`PUT /features` reports that they cannot be toggled here rather than silently
accepting a change it would not make.

## What is not implemented

Four operations remain, and they are recorded as missing rather than served by
something that would mislead a client:

- `POST /rest/agile/1.0/board` and `DELETE /rest/agile/1.0/board/{boardId}`.
  A board is created with its project and removed with it, so there is no
  standalone board lifecycle to expose yet.
- `GET /rest/agile/1.0/board/filter/{filterId}`. Boards are project-scoped with
  a JQL string and are not built on a saved filter, so answering "no board uses
  this filter" would misreport a board that cannot exist.
- `POST /rest/agile/1.0/board/{boardId}/issue`. Moving work onto a board happens
  through the sprint and backlog endpoints, which the browser journey uses.

## Evidence and current boundary

- `internal/agile/board_test.go` covers the project, version, epic, feature,
  report, sprint-issue and property operations, both base paths, the
  approximate counts, and the 404s for an unknown board, sprint, epic and
  property.
- `e2e/backlog.spec.ts` and `e2e/v4.spec.ts` cover the browser board and backlog
  journeys.
- `migrations/139_board_properties.sql` is exercised from a clean PostgreSQL
  schema.

Jira's `expand`, JQL and field filters on the board issue reads, board estimation
configuration, and exact Jira error wording remain.
