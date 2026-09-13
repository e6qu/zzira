# Issue types, priorities and resolutions

Jira's issue metadata: the issue types work is recorded as, the priorities it is
ranked by, the resolutions it is finished with, the schemes that decide which of
those a project offers, and the properties apps store against an issue type.

## Sites, defaults and ids

Each of these is site-wide in Jira, and every site starts with the same defaults:

| Kind | Defaults | Jira ids |
| --- | --- | --- |
| Issue types | Epic, Story, Task, Bug, Sub-task | 10000–10004 |
| Priorities | Highest, High, Medium, Low, Lowest | 1–5 (Medium is the default) |
| Resolutions | Done, Won't Do, Duplicate, Cannot Reproduce | 10000–10003 (Done is the default) |

Many sites share one database here. A default is stored once and shared, and a
site's rename, re-description, reorder or deletion of a default is kept for that
site alone — renaming Medium in one site never renames it in another. What a site
creates belongs to that site and is invisible to every other.

Clients see Jira's numeric ids. Lookups also accept a name; the ids the product
stores internally are never sent.

## Issue types

| Method | Path | |
| --- | --- | --- |
| `GET` | `/rest/api/3/issuetype` | the site's types, parents first |
| `POST` | `/rest/api/3/issuetype` | `standard` at level 0 or `subtask` at level −1; a duplicate name is 409 |
| `GET` | `/rest/api/3/issuetype/project?projectId=&level=` | the types the project's scheme offers |
| `GET` / `PUT` / `DELETE` | `/rest/api/3/issuetype/{id}` | |
| `GET` | `/rest/api/3/issuetype/{id}/alternatives` | the site's other types of the same kind |
| `POST` | `/rest/api/3/issuetype/{id}/avatar2` | JPEG, GIF or PNG; `X-Atlassian-Token: no-check` required |

A new type joins the site's default issue type scheme, as in Jira.

Deleting a type that issues use requires `alternativeIssueTypeId`: without one it
is 404, naming the type itself is 409, and naming a type of the other kind is 400.
The issues move to the alternative, and the type leaves every issue type scheme,
screen scheme, field configuration scheme, custom field context and workflow
scheme mapping that named it.

## Issue type properties

`GET /rest/api/3/issuetype/{id}/properties` lists keys;
`GET`, `PUT` and `DELETE` on `/properties/{key}` read, store and remove a value.
`PUT` answers 201 for a new key and 200 for a replaced one. The value must be
valid, non-empty JSON of at most 32768 characters.

## Priorities

| Method | Path | |
| --- | --- | --- |
| `GET` | `/rest/api/3/priority` | in the site's order |
| `POST` | `/rest/api/3/priority` | `name` and `statusColor` (`#rgb` or `#rrggbb`) required; answers `{id}` |
| `GET` | `/rest/api/3/priority/search` | `id`, `projectId`, `priorityName`, `onlyDefault`; paged |
| `PUT` | `/rest/api/3/priority/default` | |
| `PUT` | `/rest/api/3/priority/move` | `ids` with `after` or `position` (`First`, `Last`) |
| `GET` / `PUT` | `/rest/api/3/priority/{id}` | an update needs at least one field |
| `DELETE` | `/rest/api/3/priority/{id}` | asynchronous: 303 with the task |

A new priority joins the site's default priority scheme. Deleting one runs as a
task: its issues take the site's default priority and it leaves every priority
scheme. The default priority cannot be deleted, and a second delete of the same
priority while the first is running is 409.

## Resolutions

| Method | Path | |
| --- | --- | --- |
| `GET` / `POST` | `/rest/api/3/resolution` | |
| `GET` | `/rest/api/3/resolution/search` | `id`, `onlyDefault`; paged, with `default` on each |
| `PUT` | `/rest/api/3/resolution/default` | |
| `PUT` | `/rest/api/3/resolution/move` | |
| `GET` / `PUT` | `/rest/api/3/resolution/{id}` | |
| `DELETE` | `/rest/api/3/resolution/{id}?replaceWith=` | asynchronous; `replaceWith` required |

Deleting a resolution moves its issues to the replacement, so a resolved issue is
never made unresolved by a deletion.

## Resolution on issues

An issue carries `fields.resolution` and `fields.resolutiondate`, both `null`
while unresolved. Moving it into a status in the done category gives it the site's
default resolution and records when; moving it out of one clears both. That is
what Jira's default workflows do. Issues already in a done status when this was
introduced were given Done, dated by their last update.

### In JQL

`resolution` reads the real resolution:

- `resolution = Unresolved` and `resolution is EMPTY` match issues with no
  resolution; `resolution != Unresolved` matches resolved ones.
- `resolution in (Done, Unresolved)` matches either.
- `NOT IN` never matches an unresolved issue, as in Jira.
- `resolutiondate` compares the moment the issue was resolved.

`ORDER BY priority` and `ORDER BY resolution` order by the site's order — Highest
before High — not alphabetically.

## Issue type schemes

| Method | Path | |
| --- | --- | --- |
| `GET` / `POST` | `/rest/api/3/issuetypescheme` | list pages `id`, `queryString`, `orderBy`, `expand=issueTypes,projects` |
| `GET` | `/rest/api/3/issuetypescheme/mapping` | |
| `GET` / `PUT` | `/rest/api/3/issuetypescheme/project` | |
| `PUT` / `DELETE` | `/rest/api/3/issuetypescheme/{id}` | |
| `PUT` | `/rest/api/3/issuetypescheme/{id}/issuetype` | |
| `PUT` | `/rest/api/3/issuetypescheme/{id}/issuetype/move` | |
| `DELETE` | `/rest/api/3/issuetypescheme/{id}/issuetype/{typeId}` | |

Every site has a default scheme that unassigned projects use. The rules are Jira's:

- a scheme name is unique within the site (409);
- the default type must be one of the scheme's types;
- assigning a scheme to a project is refused while any issue in the project uses a
  type the scheme lacks;
- adding types fails entirely if any is already present;
- a type cannot be removed while issues in the scheme's projects use it, from the
  default scheme, or if it is the scheme's last standard type;
- the default scheme, and a scheme any project uses, cannot be deleted.

## Priority schemes

| Method | Path | |
| --- | --- | --- |
| `GET` / `POST` | `/rest/api/3/priorityscheme` | |
| `POST` | `/rest/api/3/priorityscheme/mappings` | priorities a change would strand |
| `GET` | `/rest/api/3/priorityscheme/priorities/available` | |
| `PUT` / `DELETE` | `/rest/api/3/priorityscheme/{id}` | |
| `GET` | `/rest/api/3/priorityscheme/{id}/priorities` | with each priority's `sequence` |
| `GET` | `/rest/api/3/priorityscheme/{id}/projects` | |

A change that would leave issues with a priority their project no longer offers
needs a mapping for each such priority, or it is refused with 400. An update
accepts complete lists or Jira's `add` and `remove` lists. The default scheme's
projects are every project without another scheme, and it cannot be deleted; a
scheme with projects cannot be deleted either.

## Boundary

- Updating a priority scheme applies its mappings before answering, so the 202
  carries no task to follow.
- Alternative issue types are the site's other types of the same kind. Jira
  narrows them further to types sharing the same workflow, field configuration and
  screen schemes.
- Team-managed project scoping (`scope`, `entityId`) is not modelled; every type
  is a company-managed type.
- Avatars are stored and selected, but only the site's own uploads are offered;
  there is no system avatar catalogue for issue types yet.
