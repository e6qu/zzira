# Issue comments, links, archiving and bulk operations

Jira's issue-level surface: comments and their properties, issue links and link
types, remote links, watchers and assignment, the issue projection, bulk reads
and writes, changelogs, the issue picker, notifications, limit reports,
archiving, redaction, bulk issue properties and issue panel pins.

## Ids

Clients see Jira's numeric ids for issues, comments, issue links, link types,
remote links, attachments and worklogs. Lookups accept those ids (and keys for
issues); stored ids are never sent. `self` URLs use the numeric ids, for example
`/rest/api/3/issue/10042/comment/10007`.

## Comments

| Method | Path | |
| --- | --- | --- |
| `GET` / `POST` | `/rest/api/3/issue/{issueIdOrKey}/comment` | paged (`startAt`, `maxResults` up to 5000), `orderBy` `created`, `+created` or `-created` (anything else is 400), `expand=renderedBody,properties` |
| `GET` / `PUT` / `DELETE` | `/rest/api/3/issue/{issueIdOrKey}/comment/{id}` | |
| `POST` | `/rest/api/3/comment/list` | 1 to 1000 ids; only comments the caller may read |
| `GET` | `/rest/api/3/comment/{commentId}/properties` | keys |
| `GET` / `PUT` / `DELETE` | `/rest/api/3/comment/{commentId}/properties/{key}` | `PUT` answers 201 new, 200 replaced |

A comment body is an Atlassian Document Format document. `visibility` restricts a
comment to a group (by `identifier` group id, or `value` name) or a project role;
people outside it do not see the comment in any list, lookup or issue field.
`PUT` with `visibility: null` removes the restriction; omitting it leaves it.

Permissions follow the project's permission scheme: *Add comments* to add, *Edit
all comments* or *Edit own comments* to edit (and to change properties), *Delete
all comments* or *Delete own comments* to delete. An issue holds at most 5000
comments; one more is 413. Edits record `updateAuthor` and `updated`, and send
the *Issue comment edited* and *Issue comment deleted* events.

## Issue links and link types

| Method | Path | |
| --- | --- | --- |
| `GET` / `POST` | `/rest/api/3/issueLinkType` | `POST` needs *Administer Jira* |
| `GET` / `PUT` / `DELETE` | `/rest/api/3/issueLinkType/{id}` | |
| `POST` | `/rest/api/3/issueLink` | 201 with no body |
| `GET` / `DELETE` | `/rest/api/3/issueLink/{linkId}` | |

Every site starts with Jira's link types: Blocks (10000), Cloners (10001),
Duplicate (10002) and Relates (10003). A site's new types take ids from 10004.
Name, inward and outward descriptions are required; a name already in use, or a
caller without permission, is 404 as Jira documents. A non-numeric id is 400.
Deleting a link type deletes every link of that type.

A link reads from the inward issue: with Blocks, `inwardIssue` "blocks"
`outwardIssue`. The inward issue's `issuelinks` field lists the other issue as
`outwardIssue`; the outward issue lists it as `inwardIssue`. Creating a link
needs *Link issues* on the inward issue's project; an optional `comment` goes on
the outward issue. Each issue holds at most 2000 links. Links are refused with
404 while issue linking is switched off for the site.

## Remote links

| Method | Path | |
| --- | --- | --- |
| `GET` / `POST` / `DELETE` | `/rest/api/3/issue/{issueIdOrKey}/remotelink` | `GET ?globalId=` returns one link; `DELETE` requires `globalId` |
| `GET` / `PUT` / `DELETE` | `/rest/api/3/issue/{issueIdOrKey}/remotelink/{linkId}` | |

`object.url` (absolute) and `object.title` are required. `POST` with a `globalId`
already on the issue replaces that link (200); otherwise it creates one (201).
Fields left out of `POST` or `PUT` become empty. A link id that belongs to another
issue is 400; an unknown one is 404. Changes need *Link issues* and *Edit issues*.

## Watchers and assignment

`GET /issue/{key}/watchers` lists watchers to people with *View voters and
watchers*; everyone sees `watchCount` and `isWatching`. `POST` with no body
watches for the caller; naming someone else needs *Manage watchers*, and that
person must be able to see the issue. `DELETE` requires `accountId`.
`POST /rest/api/3/issue/watching` reports the caller's watch status for a list of
issue ids, `false` for unknown ones.

`PUT /issue/{key}/assignee` takes exactly one of `accountId`, `name` or `key`:
`null` unassigns, `"-1"` assigns the project default (the lead when the project
assigns to its lead), and an unknown person is 400. Usernames and user keys do
not exist on Cloud and are refused.

## Reading issues

`GET /rest/api/3/issue/{issueIdOrKey}` supports `fields` (named fields, `*all`,
`*navigable`, `-field` exclusions), `fieldsByKeys`, `expand` (`renderedFields`,
`names`, `schema`, `transitions`, `operations`, `editmeta`, `changelog`,
`versionedRepresentations`), `properties` and `updateHistory`. Beyond the issue
row it returns `creator`, `created`, `comment`, `issuelinks`, `watches`, `votes`,
`subtasks`, `attachment` and `worklog`, loading each only when it is asked for.

`POST /rest/api/3/issue/bulkfetch` returns up to 100 issues, or up to 1000 when
the request names fields to include and asks for no expansions or properties.
Unknown or hidden issues are left out.

## Writing issues

- `POST /rest/api/3/issue` also stores `properties` and applies a `transition`,
  reporting its outcome under `transition`.
- `POST /rest/api/3/issue/bulk` creates up to 50 issues, each exactly as a single
  create would; failures are reported with `failedElementNumber`, and the answer
  is 201 when any issue was created, 400 when none was.
- `PUT /rest/api/3/issue/{issueIdOrKey}` stores `properties` and answers 200 with
  the issue when `returnIssue=true`. A work item whose workflow status sets
  `jira.issue.editable` (or the deprecated `issueEditable`) to `false` refuses
  edits, comment edits and worklog changes with 400; it still takes comments and
  transitions, its edit metadata lists no fields, and the issue page hides Edit
  and the estimate form. `overrideEditableFlag` lifts the lock and
  `overrideScreenSecurity` lets an edit set, and `editmeta` list, fields the
  field configuration hides. Both are for Connect and Forge apps with Administer
  Jira; anyone else, administrators included, gets 403. The workflow editor
  locks or allows editing per status.
- `DELETE /rest/api/3/issue/{issueIdOrKey}` refuses an issue with subtasks unless
  `deleteSubtasks=true`, which deletes them too.
- `GET /issue/{key}/transitions` includes transition screen fields only with
  `expand=transitions.fields`, filters by `transitionId`, and can order by status
  category with `sortByOpsBarAndStatus`. `POST` stores `properties`.

## Changelogs

- `GET /issue/{key}/changelog` is paged, oldest first, 100 at most per page.
- `POST /issue/{key}/changelog/list` returns the changelogs with the given ids.
- `POST /rest/api/3/changelog/bulkfetch` returns the changelogs of up to 1000
  issues, oldest first and then by issue id, optionally only items for up to 10
  `fieldIds`, with a `nextPageToken` while more remain.

Every changelog item carries `fieldId`.

## Issue picker

`GET /rest/api/3/issue/picker` returns two sections: *History Search* (`hs`), the
matching issues the caller viewed most recently, and *Current Search* (`cs`), the
matching issues of `currentJQL`. `query` matches a key or summary and is wrapped
in `<b>`. `currentIssueKey` is excluded, `currentProjectId` narrows the project,
and `showSubTasks=false` or `showSubTaskParent=false` leave out subtasks or the
current issue's parent. Archived issues never appear.

## Notifications

`POST /issue/{key}/notify` queues an email to the reporter, assignee, watchers,
voters, named users and groups, narrowed by `restrict` groups and permissions,
and only to people who can see the issue. The sender is never a recipient;
a notification that would reach only the sender is 400, as is one addressed to
a missing assignee or reporter, an unknown user or group, or an unknown permission.

## Events and limit reports

For administrators:

- `GET /rest/api/3/events` lists Jira's issue events.
- `GET /rest/api/3/issue/limit/report` reports issues at or above 80% of the
  per-issue limits: comments 5000, worklogs 10000, attachments 2000, issue links
  2000 and remote links 2000.
- `GET /rest/api/3/issue/limit/adf/report` does the same for rich-text sizes
  (`comment_adf`, `worklog_adf`, `customfield_adf`, `description_adf`,
  `environment_adf`), listing the entities that breach the limit. An unknown
  `fieldType` is 400.

`isReturningKeys=true` keys both reports by issue key instead of id.

## Archiving

| Method | Path | |
| --- | --- | --- |
| `PUT` | `/rest/api/3/issue/archive` | up to 1000 ids or keys |
| `POST` | `/rest/api/3/issue/archive` | `jql`; 202 with the task URL |
| `PUT` | `/rest/api/3/issue/unarchive` | |
| `PUT` | `/rest/api/3/issues/archive/export` | 202 with the export task |

Archiving and restoring take an issue's subtasks with it; a subtask cannot be
named directly. The answer counts the issues changed and groups the rest under
`issuesNotFound`, `issueIsSubtask`, `issuesInArchivedProjects`,
`issuesInUnlicensedProjects` and `userDoesNotHavePermission`: 200 when any issue
changed, 400 when none did, 412 when more than 1000 were named.

An archived issue can still be read, but it leaves search, boards and the picker,
and every change to it is refused with 400. Only one archive-by-JQL request and
one export run at a time; another is 412. The export emails the requester a link
to a CSV of the matching archived issues.

## Redaction

`POST /rest/api/3/redact` queues redactions of text in issue fields (`summary`,
`description`, `environment`, custom fields), comments and worklogs, and answers
202 with the job id. `GET /rest/api/3/redact/status/{jobId}` reports `PENDING`,
`IN_PROGRESS` or `COMPLETED` with each redaction's result.

Each redaction names the text by `from` and `to` (characters) and must match
`expectedText`, the Base64 SHA-256 digest of that text. Rich text is addressed
by an `adfPointer`. Each redacted character becomes `█`, so other redactions in
the same value keep their positions, and the text is scrubbed from the item's
recorded history. An `externalId` is used once; a repeat, a digest mismatch or an
unknown entity makes that redaction unsuccessful without affecting the others.

## Bulk issue properties

All four operations answer 303 with a task:

- `POST /rest/api/3/issue/properties` sets up to 10 properties on up to 10000 issues.
- `POST /rest/api/3/issue/properties/multi` sets per-issue properties on up to 100 issues.
- `PUT /rest/api/3/issue/properties/{key}` sets one value on issues chosen by
  `filter` (`entityIds`, `currentValue`, `hasProperty`).
- `DELETE /rest/api/3/issue/properties/{key}` removes a property from issues chosen
  by `entityIds` and `currentValue`.

Only issues the caller can see and edit change. An update overlapping one already
queued or running on the same issues is 409. Setting a value from a Jira
`expression` is refused with 400 until Jira expressions are available.

## Issue panels

`POST /rest/api/3/forge/panel/action/bulk/async` pins or unpins an installed issue
panel, named by its module id, on projects, and answers 202 with a task id.

## Evidence

- `internal/api3/issue_comments.go`, `issue_links_api.go`, `issue_operations.go`, `issue_admin_ops.go`
- `internal/store/jira_comments.go`, `jira_link_types.go`, `jira_remote_links.go`, `jira_issue_archive.go`, `jira_redaction.go`, `jira_issue_property_tasks.go`, `jira_issue_panel_pins.go`, `jira_issue_limits.go`, `jira_issue_views.go`
- `migrations/165_jira_issue_surface.sql`
- `internal/api3/issue_surface_test.go`
