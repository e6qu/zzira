# Jira project lifecycle

Updated: 2026-09-09

This checkpoint gives project administrators one durable lifecycle across the
Jira v3 API, browser administration, search, boards, service management, local
replicas, asynchronous tasks, and attachment storage.

## Delivered behavior

- Opening a project through the browser or `GET /rest/api/3/project/{idOrKey}`
  records a workspace- and user-scoped view. `GET /rest/api/3/project/recent`
  returns up to 20 active projects in recency order and supports selected
  properties plus `projectKeys`, `lead`, `issueTypes`, `permissions`, and
  `insight` expansions.
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

- ZZIRA requires authentication for recent-project reads; Jira can expose this
  operation anonymously when public project permissions allow it.
- Lifecycle administration is site-admin scoped until permission schemes grant
  the corresponding project-level operations.
- Recent-project expansions cover the useful Jira project bean fields listed
  above; complete permission-scheme-derived permissions and every optional
  project representation remain part of PR 1.
- Automatic trash deletion uses ZZIRA's hourly worker cadence rather than
  Atlassian's internal scheduling interval.
