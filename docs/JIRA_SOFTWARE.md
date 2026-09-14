# Jira Software: epics, ranking, boards, estimation, DevOps modules and Jira expressions

## Epics

An epic is an issue whose work type sits at hierarchy level 1 (Jira's Epic
type is seeded for every site). Standard issues take an epic as their parent
on create, on edit and through the Agile moves; the epic may belong to any
project, as in company-managed Jira. Sub-tasks keep their parent in their own
project, and an epic takes no parent.

Epics carry Jira Software's extra state beside the issue: a name (the summary
until one is set), one of the fourteen colors `color_1` to `color_14`, and the
done flag. Deleting an epic keeps its issues; they lose their parent and the
change is recorded for every replica.

| Operation | Behavior |
| --- | --- |
| `GET/POST /rest/agile/1.0/epic/{epicIdOrKey}` | The epic bean; the partial update changes name, summary, color and done. Non-epics are 404. |
| `GET/POST /rest/agile/1.0/epic/{epicIdOrKey}/issue` | The epic's standard issues (JQL filter, rank order); moves up to 50 standard issues in. |
| `GET/POST /rest/agile/1.0/epic/none/issue` | Standard issues without an epic; removes epic parents. |
| `PUT /rest/agile/1.0/epic/{epicIdOrKey}/rank` | Ranks an epic before or after another epic. |
| `GET /rest/agile/1.0/board/{boardId}/epic` and `/epic/{epicId or none}/issue` | The board project's epics (optionally by done) and their issues. |
| `GET /rest/software/1.0/epic/{epicIdOrKey or none}/issue` | The same reads paged with `nextPageToken`. |

## Ranking and moves

Jira has one site-wide rank order. `PUT /rest/agile/1.0/issue/rank` places up to
50 issues before or after a reference issue, keeping their order; when an issue
cannot be ranked the response is 207 with an entry per issue. The board
configuration reports Jira's Rank field id `10019`, the only `rankCustomFieldId`
the rank operations accept. `POST /issue/rank` remains an alias.

`POST /rest/agile/1.0/backlog/{boardId}/issue` moves issues on the board to its
backlog and `POST /rest/agile/1.0/board/{boardId}/issue` moves backlog issues
onto the board: into the active sprint of a scrum board (an entry reports a
board without one), while every kanban board issue is already on the board.
Both rank the moved issues when a reference is given.

## Boards

`POST /rest/agile/1.0/board` creates a scrum or kanban board from a filter the
caller can view — a saved filter or another board's filter. A project location
names the project; a user location takes the project the filter is limited to.
`GET /rest/agile/1.0/board/filter/{filterId}` lists the boards created from a
filter together with the board that owns it, and administrators delete boards
with `DELETE /rest/agile/1.0/board/{boardId}`, which removes the board's
sprints and keeps the issues.

## Estimation

Every site has Jira's **Story point estimate** number field. Scrum boards
estimate with it, and their configuration reports
`{"type": "field", "field": {"fieldId": …, "displayName": "Story point estimate"}}`.
`GET/PUT /rest/agile/1.0/issue/{issueIdOrKey}/estimation?boardId=…` reads and
writes the board's field for an issue on the board, whatever the screens show.

`GET /rest/agile/1.0/issue/{issueIdOrKey}` and every Agile issue list carry
Jira Software's fields: `sprint` (the open sprint), `closedSprints`, `flagged`
and `epic`.

## DevOps provider modules

| Module | Base path | Entities |
| --- | --- | --- |
| Operations | `/rest/operations/1.0` | incidents, post-incident reviews, linked workspaces |
| Security | `/rest/security/1.0` | vulnerabilities, linked workspaces |
| DevOps components | `/rest/devopscomponents/1.0` | components |
| Feature flags | `/rest/featureflags/0.1` | flags |
| Remote links | `/rest/remotelinks/1.0` | remote links |

Each `POST …/bulk` validates every entity against Jira's schema and answers 202
with the accepted ids, the failed ids and their messages, and the unknown issue
associations. An entity only associated with unknown issues is rejected. Stored
data is replaced only by an update with a higher update sequence; the stored
document is what the provider submitted. Entities read and delete by id, and
`DELETE …/bulkByProperties` removes a module's data carrying every given
property from the submission that tagged it. Errors use the modules' list of
messages, `[{"message": …}]`.

## Jira expressions

`internal/jexpr` implements the expression language: literals, template
literals, arithmetic and comparison, `&&`, `||`, `??`, optional chaining,
conditionals, arrow functions, lists and maps with spread, `new Date`, `new Map`,
`new Issue`, `new User`, `new Project`, and the list, string, map, date, `Math`
and `JSON` functions.

| Operation | Behavior |
| --- | --- |
| `POST /rest/api/3/expression/analyse` | `check=syntax` reports parse errors with line and column; `type` infers Jira types such as `List<String>` and reports unknown properties; `complexity` gives the expensive-operation formula, e.g. `N` for `issues.map(i => i.comments)`. |
| `POST /rest/api/3/expression/eval` | Evaluates with `user`, `issue`, `project`, `sprint`, `board`, `serviceDesk`, `customerRequest`, JQL-loaded `issues` (paged by `startAt`, with `strict`, `warn` or `none` validation) and custom `json`, `user`, `issue` and `list` variables. |
| `POST /rest/api/3/expression/evaluate` | The same, paging JQL issues with `nextPageToken`. |

Evaluation enforces Jira's limits — 10,000 steps, 10 expensive operations,
1,000 beans and 10,000 primitive values — and `expand=meta.complexity` reports
what was used. Loading an issue's comments, attachments, worklogs, changelogs,
links, sub-tasks, stories, parent, epic, properties, sprints, votes or watches
is an expensive operation. Entities come back as their Jira REST
representations, and everything an expression reads follows the caller's
issue, comment and project permissions; anonymous evaluation has a null user
and sees no issues.

## Evidence

- `migrations/167_jira_software_agile.sql`
- `internal/agile/software.go`, `internal/store/jira_software.go`
- `internal/api3/software_providers.go`, `internal/api3/expressions.go`, `internal/jexpr`
- `internal/agile/software_test.go`, `internal/api3/software_providers_test.go`,
  `internal/api3/expressions_test.go`, `internal/jexpr/jexpr_test.go`
- `e2e/software.spec.ts`
