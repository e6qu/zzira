# Boards and sprints

Boards show a project's work in columns. They also carry the board's backlog, sprints, quick filters, properties and administrators. The Agile REST API serves boards under `/rest/agile/1.0`, and the `/rest/software/1.0` reads are served by the same code. This page is part of [Jira Software](JIRA_SOFTWARE.md); see [CLOUD_PARITY.md](CLOUD_PARITY.md) for status.

## UI

| Page | Purpose |
|---|---|
| `/board/{id}` | Columns with cards in rank order. Supports drag and keyboard moves, quick filters, assignee filters, WIP limit feedback, swimlanes and a work item preview. |
| `/board/{id}/backlog` | Backlog and sprints: create, edit, start and complete sprints, and move and rank work items. |
| `/board/{id}/settings` | For board administrators. Swimlanes (`none` or `assignee`), card fields (priority, assignee, labels), column WIP limits, up to 20 quick filters, and the board's administrators (users and groups). |
| `/projects/{key}/settings` | Create and delete the project's boards (`POST /projects/{key}/boards`). |

## Board REST API

| Method and path | Behavior |
|---|---|
| `GET /rest/agile/1.0/board` | Lists the boards the caller can browse. **Filters:** `type`, `name`, `projectKeyOrId`/`projectLocation`, `accountIdLocation`, `negateLocationFiltering`, `projectTypeLocation` (software by default, or `service_desk`), `filterId`. **Ordering:** `orderBy=name`. **Expansions:** `admins` and `permissions`. At most 50 per page. |
| `POST /rest/agile/1.0/board` | Creates a scrum or kanban board from a filter the caller can view: a saved filter or another board's filter. A project location names the project. A user location uses the project the filter is limited to. |
| `GET /rest/agile/1.0/board/filter/{filterId}` | Lists the boards built on a filter, including the board that owns the filter. |
| `GET/DELETE /rest/agile/1.0/board/{boardId}` | Reads a board, with its project location and type. Delete is for administrators: it removes the board's sprints and keeps the work items. |
| `GET …/configuration` | Returns columns and their statuses, the constraint type (`issueCount` when a WIP limit is set), the filter, the location, `ranking.rankCustomFieldId` (`10019`), and estimation (scrum boards) or `subQuery` (kanban boards). |
| `GET …/issue` | Pages the board's work: work items in a status mapped to a column, in rank order. Scrum boards leave out epics. |
| `POST …/issue` | Moves backlog work items onto the board. On a scrum board they go into the active sprint (an entry reports a board with none). Every kanban work item is already on the board. The moved items are ranked if a reference work item is given. |
| `GET …/backlog` | Pages the project's work items that are in no active or future sprint. |
| `POST /rest/agile/1.0/backlog/{boardId}/issue` (also `/backlog/issue`) | Moves work items to the backlog, ranking them if a reference is given. |
| `GET …/sprint` | Lists the board's sprints: closed, then active, then future, each in board order. Filter by `state`; an unknown state is 400. At most 50 per page. |
| `GET …/sprint/{sprintId}/issue` | Pages one sprint's work items in sprint order. A sprint on another board is 404. |
| `GET …/quickfilter`, `…/quickfilter/{id}` | Reads the board's quick filters. |
| `GET …/project`, `…/project/full` | Returns the board's project. The full form adds the project type and style. |
| `GET …/version` | Lists the project's versions, with Jira's `released` filter. |
| `GET …/epic`, `…/epic/{epicId or none}/issue` | Returns the project's epics and their work items. See [Jira Software](JIRA_SOFTWARE.md#epics). |
| `GET …/features` | Reports SPRINTS (scrum boards only), BACKLOG (always on) and SWIMLANES (on when a swimlane strategy is set). Each has `toggleLocked: true`. |
| `PUT …/features` | 400: features follow the board's configuration and cannot be toggled. |
| `GET …/reports` | Lists the reports available for the board type. |
| `GET …/properties`, `GET/PUT/DELETE …/properties/{key}` | Board properties. A PUT that creates a key returns 201; one that replaces a value returns 200. |
| `GET /rest/software/1.0/board/{boardId}/…` | The backlog, issue, epic-none and sprint-issue reads, plus `backlog/approximate-count` and `issue/approximate-count`. |

### Filtering issue reads

The board, backlog and sprint issue reads take Jira's parameters:
- **`jql`** narrows the results within the board's scope. An `ORDER BY` in it replaces rank or sprint order.
- **Invalid JQL** returns 400. With `validateQuery=false`, the response is instead an empty page with the error in `warningMessages`.
- **`fields`** keeps the named fields. `*all` and `*navigable` keep every field, and `-field` drops one. Without `fields`, every navigable field is returned, plus the Agile fields `sprint`, `closedSprints`, `flagged` and `epic`.
- **`expand`** is accepted.

## Sprint REST API

| Method and path | Behavior |
|---|---|
| `POST /rest/agile/1.0/sprint` | Creates a sprint. New sprints go to the end of the board's sprint order. |
| `GET/PUT/POST /rest/agile/1.0/sprint/{sprintId}` | Reads a sprint. PUT replaces it and POST partially updates it; both leave absent fields unchanged. The sprint includes `createdDate` and `completeDate`: when it actually started and completed, kept separate from its planned dates. |
| `DELETE …/{sprintId}` | Deletes a sprint and returns its work items to the backlog. An active sprint cannot be deleted until it is completed. |
| `GET …/issue` | The sprint's work items, with the same filtering parameters as the board reads. |
| `POST …/issue` | Moves up to 50 work items from the sprint's project into a future or active sprint. With `rankBeforeIssue` or `rankAfterIssue`, they are ranked around that work item in request order. **Errors:** a closed sprint, a work item from another project, or both rank references is 400; an unknown work item is 404. |
| `POST …/swap` | Swaps two sprints' positions on the same board. |
| `GET …/properties`, `GET/PUT/DELETE …/properties/{key}` | Sprint properties, with the same rules as board properties. |
| `GET /rest/software/1.0/sprint/{sprintId}/issue` | The same read as the Agile sprint issue read. |

## Behavior

**Sprint states.** A sprint moves from future to active to closed. A board runs one active sprint at a time unless the site turns on **parallel sprints** in Jira settings (`parallelSprintsEnabled`). Starting a second sprint without that setting is refused.

**Moving a card between columns.** A drag or keyboard move that changes column is a real workflow transition, not a status write. The board tries each transition out of the card's current status whose destination is the target column's status, in workflow order, and keeps the first that runs. Conditions, validators, post-functions and history therefore apply exactly as on the work item page.
- A target status that is not one of the board's columns is 400.
- A column the current workflow offers no transition into is refused, and the card stays where it was.
- A missing permission stops the move at once, without trying the next transition.
- The refusal is 403 for a missing permission and 400 otherwise.

## Permissions

Boards and sprints are served only to people who can browse the board's project. Everyone else gets 404, as in Jira. A board is configured by its board administrators and by the administrators of its project.

Every write goes through the command layer, which asks for the project permission Jira asks for ([PERMISSION_SCHEMES.md](PERMISSION_SCHEMES.md)):

| Action | Needs |
|---|---|
| Rank a card, on the board or through the rank APIs | Schedule issues |
| Move a card to another column | Schedule issues, plus Transition issues and whatever the transition itself needs |
| Move work between the backlog and a sprint, on the board or through the Agile APIs | Edit issues and Schedule issues |
| Create, edit, start, complete, delete or reorder a sprint, and write sprint properties | Manage sprints |
| Edit an epic's name, color or done flag | Edit issues |
| Configure a board: swimlanes, card fields, WIP limits, quick filters and its administrators | Board administrator, or Administer projects on the board's project |

The default permission scheme grants Schedule issues, Edit issues, Transition issues and Manage sprints to the project's Members role, which every workspace member joins. A project on a scheme that withholds them serves the board read-only. See [PROJECT_ROLES.md](PROJECT_ROLES.md).

## Gaps

- Columns map one status each. You cannot configure columns (add, rename, or group several statuses in one column).
- Swimlanes support only `none` and `assignee`; Jira also offers epics, stories, queries and projects.
- Board features cannot be toggled (`PUT …/features`).
- A board's filter and estimation field cannot be changed after the board is created.

Remaining work is tracked in [PLAN.md](../PLAN.md).

## See also

- [REPORTS.md](REPORTS.md): sprint, velocity, cumulative flow and control chart reports.
- [FILTERS.md](FILTERS.md): the saved filters boards are built on.
- Code: `internal/agile`, `internal/store/agile.go`, `internal/store/board_*.go`, `internal/web/board.go`, `internal/web/backlog.go`.
- Tests: `internal/agile/board_test.go`, `e2e/backlog.spec.ts`, `e2e/v4.spec.ts`, `e2e/projects.spec.ts`.
