# Project lifecycle

Projects are active, archived or in trash. Archiving hides a project while
keeping its data; trash holds a project for 60 days before permanent deletion;
restore brings either back. Recent-project tracking is covered here too. Part
of the [Jira platform](JIRA_PLATFORM.md); see
[CLOUD_PARITY.md](CLOUD_PARITY.md) for status.

## API

| Route | Behavior |
|---|---|
| `GET /rest/api/3/project/recent` | Up to 20 browsable active projects the caller viewed, most recent first, with `properties` and every documented expansion |
| `POST /rest/api/3/project/{idOrKey}/archive` | Archive an active project |
| `POST /rest/api/3/project/{idOrKey}/restore` | Restore an archived or trashed project |
| `DELETE /rest/api/3/project/{idOrKey}` | Move an active project to trash; `enableUndo=false` deletes an active or trashed project permanently |
| `POST /rest/api/3/project/{idOrKey}/delete` | Permanent deletion as a durable task: 303 with the task URL in `Location` |

Project reads share one bean builder. `GET /project/{idOrKey}` includes
components, versions, role URLs and the `issueTypeHierarchy` expansion.
`GET /project/search` filters by `action`, `status` (`live`, `archived`,
`deleted`) and `propertyQuery`, supports every documented ordering, and
reports archive and trash dates, the actor and the 60-day retention date.

## Behavior

- **Views.** Opening a project in the browser or through
  `GET /project/{idOrKey}` records a per-user view for the recent list.
- **Archive.** Data stays intact, but project reads, search, work item access,
  boards, reports and service desk discovery stop exposing the project.
  Archive actions remove its project, work item, board, sprint and sprint
  membership records from local replicas.
- **Restore** republishes the project and its planning state to replicas.
- **Trash.** Trashed projects are deleted automatically 60 days after
  `trashed_at`, by an hourly runner that resumes after restarts; competing
  replicas are tolerated.
- **Archived projects** must be restored before they can be deleted.
- **Permanent deletion** cascades through work items, boards, sprints,
  service desks, versions, components, properties, workflow data and other
  project children. Attachment metadata is deleted in the same transaction and
  blobs enter the leased cleanup queue (see [ATTACHMENTS.md](ATTACHMENTS.md)).
- State changes, replica actions, audit records, cleanup registration and task
  completion are transactional. Reads stay workspace-scoped with issue
  security applied.

## Permissions

Archive, restore, trash and delete need a site or organization administrator.
Reads need Browse projects.

## UI

- `/projects` has Active, Archived (`?status=archived`) and Trash
  (`?status=trash`) views showing retained work counts, the lifecycle actor
  and the automatic deletion date.
- Project settings offer archive and trash (`POST /projects/{key}/lifecycle`);
  the Trash view offers restore and a confirmed permanent delete.

## Gaps

See [PLAN.md](../PLAN.md).

- Enhanced search takes `includeArchivedProjects=true` and returns the work
  of archived projects with it, checking Browse projects as it would for a
  live one. The choice is kept with the search, so its later pages answer
  the question its first page was asked; a work item archived in its own
  right stays out either way.
- Anonymous callers get an empty recent list; Jira keeps a session-scoped
  history for them.

## Tests

`internal/api3/project_lifecycle_test.go`, `e2e/projects.spec.ts`.

## See also

[PROJECT_GOVERNANCE.md](PROJECT_GOVERNANCE.md) · [JQL.md](JQL.md) ·
[ISSUE_SURFACE.md](ISSUE_SURFACE.md) (work item archiving)
