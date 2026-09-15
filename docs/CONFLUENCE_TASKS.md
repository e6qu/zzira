# Confluence tasks

Tasks live where Confluence keeps them: in the bodies of pages and blog posts,
as task lists.

```xml
<ac:task-list>
  <ac:task>
    <ac:task-id>1</ac:task-id>
    <ac:task-status>incomplete</ac:task-status>
    <ac:task-body>Ship the release <ac:link><ri:user ri:account-id="…" /></ac:link> <time datetime="2030-01-02" /></ac:task-body>
  </ac:task>
</ac:task-list>
```

## What a body says

- Each task has an id that is unique within its body. Its status is
  `complete` or `incomplete`, and `incomplete` when left out.
- The first person the task mentions is its assignee, provided they belong
  to the site. The first date in it is when it is due, at the start of that
  day in UTC.
- Task lists render as checklists: ☐ for open tasks, ☑ for done ones. Dates
  render as `<time>` elements.
- In `atlas_doc_format` a task list is a `taskList` of `taskItem`s, using
  `localId` and the `TODO`/`DONE` state. Mentions and dates are `mention` and
  `date` nodes. Converting between the formats keeps tasks, mentions and
  dates, so tasks written through either format are the same tasks.

## Keeping the task store in step

Every change to a body reconciles its tasks. That includes publishing, editing,
restoring a version and redaction, for pages and blog posts alike:

- tasks new to the body are created, by whoever saved it;
- changed tasks are updated. A task that becomes complete is completed by the
  person who saved the body; a reopened task loses its completer;
- tasks taken out of the body are deleted.

## The tasks API

- `GET /wiki/api/v2/tasks` and `GET /tasks/{id}` list tasks on published
  pages and blog posts the caller can see.
  - `page-id` and `blogpost-id` together select tasks on any of those pages
    or posts.
  - Beans carry `pageId` or `blogPostId`.
  - `body-format` is `storage` or `atlas_doc_format`.
- `PUT /tasks/{id}` changes a task's status. It needs permission to edit the
  page or blog post, and it ticks the task in the body in place, without
  making a new version.

## The web UI

- The page task panel adds a task by writing it into the page body, as a new
  version. The assignee is written as a mention, so they are notified, and
  the due date is written as a date.
- Pages and blog posts list their tasks with **Complete** and **Reopen**.
- The rich editor keeps text, formatting, links and mentions. A body that
  holds task lists, macros or dates opens in the storage editor so none of
  them is lost.

## Tasks from before

Tasks created before tasks moved into bodies were stored beside their pages.
Migration `179_wiki_body_tasks.sql` writes each of them into its page body,
with its assignee as a mention and its due date as a date.
