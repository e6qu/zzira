# Confluence tasks

Tasks are stored where Confluence stores them: as task lists inside page and
blog post bodies. A task store kept in sync with those bodies serves the tasks
API. Part of [Confluence](CONFLUENCE_SITE_SURFACES.md). Code:
`internal/confluence/tasks.go`, `internal/store/wiki_tasks.go`.

```xml
<ac:task-list>
  <ac:task>
    <ac:task-id>1</ac:task-id>
    <ac:task-status>incomplete</ac:task-status>
    <ac:task-body>Ship the release <ac:link><ri:user ri:account-id="…" /></ac:link> <time datetime="2030-01-02" /></ac:task-body>
  </ac:task>
</ac:task-list>
```

## Body semantics

- **Id and status.** Each task has an id that is unique within its body. Its
  status is `complete` or `incomplete`, and defaults to `incomplete`.
- **Assignee.** The first person mentioned in the task, if they belong to the
  site.
- **Due date.** The first date in the task, taken as the start of that day in
  UTC.
- **Rendering.** Tasks render as checklists (☐ open, ☑ done). Dates render as
  `<time>`.
- **`atlas_doc_format`.** A task list is a `taskList` of `taskItem`s with a
  `localId` and a `TODO` or `DONE` state. Mentions and dates are `mention` and
  `date` nodes. Converting between formats keeps tasks, mentions and dates.

## Keeping the store in sync

Any change to a body re-syncs its tasks. That includes publishing, editing,
restoring a version and [redaction](CONFLUENCE_REDACTION.md), on pages and
blog posts alike.

- **New tasks** are created, and whoever saved the body becomes their
  creator.
- **Changed tasks** are updated. A task that becomes complete is marked
  completed by whoever saved the body. A reopened task loses its completer.
- **Removed tasks** are deleted.

## API

| Method and path | Behavior |
| --- | --- |
| `GET /wiki/api/v2/tasks` | Tasks on published pages and blog posts the caller can see. |
| `GET /wiki/api/v2/tasks/{id}` | One task. |
| `PUT /wiki/api/v2/tasks/{id}` | Changes a task's status. |

- **Filters.** The list takes `status`, `task-id`, `space-id`, `page-id`,
  `blogpost-id`, `created-by`, `assigned-to`, `completed-by`,
  `created-at-from`/`-to`, `due-at-from`/`-to`, `completed-at-from`/`-to`,
  `include-blank-tasks`, `cursor` and `limit`.
  - Passing both `page-id` and `blogpost-id` returns tasks on any of the
    listed pages or posts.
- **Beans.** Each task carries `pageId` or `blogPostId`.
- **`body-format`.** `storage` or `atlas_doc_format`.
- **`PUT` permissions.** Needs edit permission on the page or blog post.
- **`PUT` effect.** Ticks the task in the body in place, without creating a
  new version.

## UI

- **Adding tasks.** The page task panel (`POST /wiki/spaces/{space}/pages/{page}/tasks`)
  writes the task into the page body as a new version. The assignee is
  written as a mention, which notifies them (see
  [notifications](CONFLUENCE_NOTIFICATIONS.md)). The due date is written as a
  date.
- **Completing tasks.** Pages and blog posts list their tasks with
  **Complete** and **Reopen** buttons.
- **Editors.** The rich editor handles text, formatting, links, mentions, the
  macros it draws and page layouts. A body that contains task lists, dates or
  a macro it does not draw opens in the storage editor instead, so none of
  them are lost.

## Storage

Migration `179_wiki_body_tasks.sql` moved tasks that were stored alongside
their pages into the page bodies.

## Gaps

Tracked in [PLAN.md](../PLAN.md).

- **Rich editor.** It cannot insert or edit task lists or dates, nor the
  macros it does not draw; that needs the storage editor. The panels, status,
  code and table-of-contents macros it does draw are written from its toolbar
  ([PAGE_WRITING](PAGE_WRITING.md)).
- **Task reports.** No "Tasks" macro or report, and no personal task list
  page.
- **Reminders.** No due-date reminders.

## Tests

`internal/confluence/tasks_body_test.go`

## See also

[PAGE_WRITING.md](PAGE_WRITING.md) · [CLOUD_PARITY.md](CLOUD_PARITY.md)
