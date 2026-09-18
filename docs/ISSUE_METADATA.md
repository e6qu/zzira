# Work types, priorities and resolutions

Site-wide work item metadata: work types (issue types), priorities, resolutions, the schemes that choose which of them a project offers, and work type properties. Part of the [Jira platform](JIRA_PLATFORM.md). For status, see [CLOUD_PARITY.md](CLOUD_PARITY.md).

## Defaults and ids

| Kind | Defaults (Jira id) |
| --- | --- |
| Work types | Epic (10000), Story (10001), Task (10002), Sub-task (10003), Bug (10004) |
| Priorities | Highest (1), High (2), Medium (3, default), Low (4), Lowest (5) |
| Resolutions | Done (10000, default), Won't Do (10001), Duplicate (10002), Cannot Reproduce (10003) |

- Defaults are stored once and shared by all sites. A site's rename, re-description, reorder or deletion of a default applies to that site only. What a site creates is private to it.
- Clients only see Jira's numeric ids; lookups also accept a name. This holds across the whole Jira API: work items, changelogs, `createmeta`, every scheme, custom field contexts, workflow schemes and drafts, bulk operations, status and workflow usages, request types and the project list. See [WIRE_IDS.md](WIRE_IDS.md).

## Work type hierarchy

A site's levels run Subtask (`-1`), Base (`0`), Epic (`1`) and any named levels an administrator adds above Epic (`issue_type_hierarchy_levels`, `internal/store/work_type_hierarchy.go`). `subtask` equals `hierarchy_level = -1`.

- **Settings:** `/settings/hierarchy` lists the levels top down with their work types. A site administrator adds a level (it goes on top), renames Epic and the levels above it, moves a work type to another level, and removes the top level once it is empty. Base and Subtask are fixed, as in Jira.
- **Moving a work type** is refused while its work items have a parent or children, and a shared default type moves for this site only.
- **Parent:** a work item's parent is a work item exactly one level above it — a task under an epic, an epic under the level above it. Anything else is `Given parent work item does not belong to appropriate hierarchy.` (`internal/commands/commands_v1.go`, `hierarchyParent`). The parent may be in any project. The create form and the work item view offer only the work items of the level above (`ParentOptions`).
- **Sub-tasks are the exception:** a sub-task's parent is any work item of a non-sub-task type in the sub-task's own project. Sub-tasks are refused entirely while the site switches them off ([JIRA_SITE_CONFIGURATION.md](JIRA_SITE_CONFIGURATION.md)).
- **REST:** `POST /rest/api/3/issuetype` creates standard types at level 0 and subtask types at −1, as Jira's API does; higher levels are set in the settings page. `GET /rest/api/3/project/{projectId}/hierarchy` reports the site's levels with their names ([JIRA_PLATFORM.md](JIRA_PLATFORM.md#site-and-project-reads)).
- Boards and backlogs use the epic level. Epic behavior is in [JIRA_SOFTWARE.md](JIRA_SOFTWARE.md).

## Work types

| Method | Path | Notes |
| --- | --- | --- |
| `GET` | `/rest/api/3/issuetype` | parents first |
| `POST` | `/rest/api/3/issuetype` | `type` `standard` (level 0) or `subtask` (level −1); duplicate name is 409 |
| `GET` | `/rest/api/3/issuetype/project?projectId=&level=` | types the project's scheme offers |
| `GET` / `PUT` / `DELETE` | `/rest/api/3/issuetype/{id}` | |
| `GET` | `/rest/api/3/issuetype/{id}/alternatives` | the site's other types of the same kind |
| `POST` | `/rest/api/3/issuetype/{id}/avatar2` | JPEG, GIF or PNG; needs `X-Atlassian-Token: no-check` |
| `GET` | `/rest/api/3/issuetype/{id}/properties` | property keys |
| `GET` / `PUT` / `DELETE` | `/rest/api/3/issuetype/{id}/properties/{key}` | `PUT` is 201 new, 200 replaced; value is non-empty JSON, at most 32768 characters |

- A new type joins the site's default work type scheme.
- Deleting a type in use needs `alternativeIssueTypeId`. Missing: 404. Same type: 409. Other kind (standard vs subtask): 400. Work items move to the alternative. The type is removed from every work type scheme, work type screen scheme, field configuration scheme, custom field context and workflow scheme mapping.
- Avatars: the system catalogue has five work type icons (`internal/store/universal_avatars.go`), plus the site's uploads.

## Priorities

| Method | Path | Notes |
| --- | --- | --- |
| `GET` / `POST` | `/rest/api/3/priority` | `POST` needs `name` and `statusColor` (`#rgb` or `#rrggbb`); answers `{id}` |
| `GET` | `/rest/api/3/priority/search` | `id`, `projectId`, `priorityName`, `onlyDefault`; paged |
| `PUT` | `/rest/api/3/priority/default` | |
| `PUT` | `/rest/api/3/priority/move` | `ids` with `after` or `position` (`First`, `Last`) |
| `GET` / `PUT` | `/rest/api/3/priority/{id}` | update needs at least one field |
| `DELETE` | `/rest/api/3/priority/{id}` | 303 with a task |

A new priority joins the default priority scheme. Deletion runs as a task: work items take the site default and the priority leaves every scheme. The default priority cannot be deleted. A second delete while one runs is 409.

A priority's icon is either an `iconUrl` or an `avatarId`, never both (400). The site's ten built-in icons are `highest`, `high`, `medium`, `low`, `lowest`, `blocker`, `critical`, `major`, `minor` and `trivial` under `/images/icons/priorities/`. A priority created without an icon takes Jira's medium icon, so every priority renders (`DefaultPriorityIconURL`, `internal/store/issue_metadata.go`). An empty `iconUrl` on an update leaves the icon alone.

## Resolutions

| Method | Path | Notes |
| --- | --- | --- |
| `GET` / `POST` | `/rest/api/3/resolution` | |
| `GET` | `/rest/api/3/resolution/search` | `id`, `onlyDefault`; paged, `default` on each |
| `PUT` | `/rest/api/3/resolution/default` | |
| `PUT` | `/rest/api/3/resolution/move` | |
| `GET` / `PUT` | `/rest/api/3/resolution/{id}` | |
| `DELETE` | `/rest/api/3/resolution/{id}?replaceWith=` | 303 with a task; `replaceWith` required |

Deleting a resolution moves its work items to the replacement, so no resolved work item becomes unresolved.

### On work items

- `fields.resolution` and `fields.resolutiondate` are `null` while unresolved.
- A person chooses the resolution when a transition screen asks for it (the `system:transition-screen` rule lists `resolution`, [WORKFLOW_RULES.md](WORKFLOW_RULES.md#system-rules)), or edits it on the work item; both need the Resolve issues permission ([PERMISSION_SCHEMES.md](PERMISSION_SCHEMES.md#enforcement)). `fields.resolution` takes an id or a name over REST, and `null` clears it. `resolution` is in the [screen](SCREENS.md#field-catalog) field catalog, so a screen can place it on a form.
- Without a chosen resolution, entering a status in the done category applies the site's default and records the time. Leaving the done category clears both (`internal/store/mutations.go`). Every change is recorded in the changelog.
- JQL ([JQL.md](JQL.md)): `resolution = Unresolved` and `resolution is EMPTY` match unresolved items; `resolution != Unresolved` matches resolved ones; `resolution in (Done, Unresolved)` matches either; `NOT IN` never matches an unresolved item; `resolutiondate` compares the resolve time.
- `ORDER BY priority` and `ORDER BY resolution` use the site's order, not alphabetical order.

## Work type schemes

| Method | Path | Notes |
| --- | --- | --- |
| `GET` / `POST` | `/rest/api/3/issuetypescheme` | list: `id`, `queryString`, `orderBy`, `expand=issueTypes,projects` |
| `GET` | `/rest/api/3/issuetypescheme/mapping` | |
| `GET` / `PUT` | `/rest/api/3/issuetypescheme/project` | |
| `PUT` / `DELETE` | `/rest/api/3/issuetypescheme/{id}` | |
| `PUT` | `/rest/api/3/issuetypescheme/{id}/issuetype` | |
| `PUT` | `/rest/api/3/issuetypescheme/{id}/issuetype/move` | |
| `DELETE` | `/rest/api/3/issuetypescheme/{id}/issuetype/{typeId}` | |

Rules:

- Projects without a scheme use the site's default scheme.
- Scheme names are unique per site (409).
- The default type must be one of the scheme's types.
- A project cannot be assigned a scheme while any of its work items uses a type the scheme lacks.
- Adding types fails entirely if any of them is already in the scheme.
- A type cannot be removed from the default scheme, while work items in the scheme's projects use it, or if it is the scheme's last standard type.
- The default scheme and schemes in use cannot be deleted.

## Priority schemes

| Method | Path | Notes |
| --- | --- | --- |
| `GET` / `POST` | `/rest/api/3/priorityscheme` | |
| `POST` | `/rest/api/3/priorityscheme/mappings` | priorities a change would strand |
| `GET` | `/rest/api/3/priorityscheme/priorities/available` | |
| `PUT` / `DELETE` | `/rest/api/3/priorityscheme/{id}` | `PUT` answers 202 |
| `GET` | `/rest/api/3/priorityscheme/{id}/priorities` | includes `sequence` |
| `GET` | `/rest/api/3/priorityscheme/{id}/projects` | |

- A change that would leave work items with a priority their project no longer offers needs a mapping for each such priority; otherwise it is 400.
- Updates accept full lists or Jira's `add`/`remove` lists.
- The default scheme covers every project without another scheme and cannot be deleted. A scheme with projects cannot be deleted.

A project offers the priorities of the scheme it uses, and nothing else: its
create form, its work item view and its transition screens list those, a command
that sets any other priority is rejected, and a work item created without a
priority takes the scheme's default (`ProjectOffersPriority`,
`ProjectDefaultPriority`, `PrioritiesForProject` in `internal/store/issue_schemes.go`).

## Settings pages

| Page | What an administrator does |
| --- | --- |
| `/settings/work-types` | Create a standard or sub-task work type, rename and describe it, delete it (moving its work items to another type), create a work type scheme with its types and default, assign a scheme to a project, add or remove a type, delete a scheme |
| `/settings/priorities` | Create a priority with its status colour and an icon from the built-in set, rename and describe it, make it the default, move it to the top, delete it (its work items take the default). Create a priority scheme with its priorities and default, edit one, say where work items using a dropped priority go, assign a project to it, remove a project from it, delete it |
| `/settings/resolutions` | Create a resolution, rename and describe it, make it the default, move it to the top, delete it (its work items take the replacement chosen) |
| `/settings/hierarchy` | The levels, as described above |

Work types, priorities and resolutions need site administration. `/settings/hierarchy` is readable by any member and editable only by a site administrator, which is why its forms appear for administrators alone. Deleting a priority or resolution runs as a task, as the REST API does.

## Code

`internal/api3/issue_metadata.go`, `internal/store/issue_metadata.go`, `internal/store/issue_metadata_tasks.go`, `internal/store/issue_schemes.go`, `internal/store/work_type_hierarchy.go`, `internal/web/issue_metadata_admin.go`, `internal/web/work_type_hierarchy.go`, `migrations/162_issue_metadata.sql`; tests in `internal/api3/issue_metadata_test.go`, `internal/store/priority_schemes_test.go`, `internal/store/work_type_hierarchy_test.go`, `e2e/priority_schemes.spec.ts`, `internal/commands/resolution_test.go` and `internal/commands/hierarchy_workflow_test.go`.

## Gaps

Tracked in [PLAN.md](../PLAN.md).

- `alternatives` does not narrow to types that share the same workflow, field configuration and screen schemes.
- Team-managed scoping (`scope`, `entityId`) is not modelled; every type is company-managed.
- A priority scheme update applies its mappings before answering, so the 202 has no `task`.
- The system avatar catalogue has five work type icons; Jira's is larger.

## See also

- [Permission schemes](PERMISSION_SCHEMES.md#enforcement) — the permission each work item command checks, Resolve issues included.
- [Screens](SCREENS.md) and [screen schemes](SCREEN_SCHEMES.md) — where `resolution`, `priority` and `issuetype` sit on a form.
- [Workflow rules](WORKFLOW_RULES.md) — transition screens and the rules that set a resolution.
- [Jira Software](JIRA_SOFTWARE.md) — epics, boards and backlogs on the epic level.
- [Ids clients see](WIRE_IDS.md) — the numbers clients get for types, priorities and resolutions.
