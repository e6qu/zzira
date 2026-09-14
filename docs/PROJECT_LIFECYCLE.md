# Jira project lifecycle

Updated: 2026-09-10

This checkpoint gives project administrators one durable lifecycle across the
Jira v3 API, browser administration, search, boards, service management, local
replicas, asynchronous tasks, and attachment storage.

## Delivered behavior

- Opening a project through the browser or `GET /rest/api/3/project/{idOrKey}`
  records a workspace- and user-scoped view. `GET /rest/api/3/project/recent`
  returns up to 20 browsable active projects in recency order with selected
  properties and every documented expansion. Project reads share one bean
  builder: `GET /rest/api/3/project/{idOrKey}` returns components, versions,
  role URLs and the `issueTypeHierarchy` expansion, and project search filters
  by `action`, `status` (live, archived, deleted), `propertyQuery` and every
  documented ordering, reporting archive and trash dates, actors and the
  60-day retention date.
- Administrators can archive an active project. Its database state remains
  intact while project reads, search, work-item access, boards, reports, and
  service-desk discovery stop exposing it. Archive actions remove its project,
  work, board, sprint, and sprint-membership records from local replicas.
- Administrators can restore archived or trashed projects. Restore republishes
  the project and dependent work-planning state to local replicas and makes the
  normal Jira and product surfaces available again.
- `DELETE /rest/api/3/project/{idOrKey}` moves an active project to trash by
  default. `enableUndo=false` permanently deletes an active or trashed project.
  Archived projects must be restored before deletion.
- `POST /rest/api/3/project/{idOrKey}/delete` creates a durable Jira task,
  returns `303 See Other` with its task URL in `Location`, and completes the
  delete transaction through the shared recoverable task runner.
- The project directory has Active, Archived, and Trash views. It shows
  retained work counts, the lifecycle actor, and the automatic deletion date.
  Settings provide archive and trash actions; the Trash view provides restore
  and explicit permanent-delete confirmation.
- Trashed projects are automatically deleted after 60 days. The persisted
  `trashed_at` timestamp is the schedule source, and the hourly runner resumes
  after process restarts. Multiple replicas tolerate a competing deletion.
- Permanent deletion cascades through issues, boards, sprints, service desks,
  releases, components, properties, workflow data, and other project children.
  Attachment metadata is deleted in the same transaction while blob references
  enter the existing leased, retryable cleanup queue.

## Authorization and transactional guarantees

Lifecycle writes currently require the shared site-administrator role. Reads
remain workspace scoped and issue-security filtering continues to apply before
serialization. State changes, replica actions, audit evidence, attachment
cleanup registration, dependent deletion, and asynchronous task completion are
transactional.

## Jira v3 resources

| Resource | Behavior |
|---|---|
| `GET /rest/api/3/project/recent` | Recent active projects, expansions, selected properties |
| `POST /rest/api/3/project/{idOrKey}/archive` | Archive an active project |
| `POST /rest/api/3/project/{idOrKey}/restore` | Restore archived or trashed project |
| `DELETE /rest/api/3/project/{idOrKey}` | Trash by default or permanently delete with `enableUndo=false` |
| `POST /rest/api/3/project/{idOrKey}/delete` | Durable asynchronous permanent deletion |

## Compatibility limits

- Recent projects are remembered per account, so anonymous callers receive an
  empty list; Jira's session-scoped anonymous history has no account to attach
  to.
- Lifecycle administration remains site-admin scoped; the shared permission
  evaluator is available, but lifecycle mutations have not yet adopted a
  project-level permission key.
- Automatic trash deletion uses ZZIRA's hourly worker cadence rather than
  Atlassian's internal scheduling interval.
