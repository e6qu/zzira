# Permission schemes

Jira permission schemes, their grants, the scheme assigned to each project, global permissions, and the permission checks built on them. Site administrators manage schemes at `/settings/permission-schemes` and global permissions under `/admin` › Global permissions. Project administrators view a project's effective scheme at `/projects/{key}/settings/permissions`. Part of the [Jira platform](JIRA_PLATFORM.md); related: [project roles](PROJECT_ROLES.md), [issue security schemes](ISSUE_SECURITY_SCHEMES.md), [anonymous access](ANONYMOUS_ACCESS.md). For status, see [CLOUD_PARITY.md](CLOUD_PARITY.md).

## API

Schemes and project assignment (11 operations):

| Method and path | Behavior |
| --- | --- |
| `GET/POST /rest/api/3/permissionscheme` | Lists or creates schemes; grants can be expanded. |
| `GET/PUT/DELETE /rest/api/3/permissionscheme/{schemeId}` | Reads, replaces, or deletes a scheme that is neither the default nor assigned. |
| `GET/POST /rest/api/3/permissionscheme/{schemeId}/permission` | Lists or adds grants. |
| `GET/DELETE /rest/api/3/permissionscheme/{schemeId}/permission/{permissionId}` | Reads or removes a grant. |
| `GET/PUT /rest/api/3/project/{projectKeyOrId}/permissionscheme` | Reads or assigns a project's scheme. Assigning needs Administer Jira. |

Discovery and checks (5 operations):

| Method and path | Behavior |
| --- | --- |
| `GET /rest/api/3/permissions` | Built-in global and project permissions plus those active apps declare. |
| `GET /rest/api/3/mypermissions` | Evaluates `permissions` (required; unknown keys 400) globally or for a project, work item or comment. `projectId` beats `projectKey`, `issueId` beats `issueKey`. An invisible project or work item is 404. With `commentId`, only `BROWSE_PROJECTS` is allowed. |
| `POST /rest/api/3/permissions/check` | Bounded global and project checks; administrators may check another user. |
| `POST /rest/api/3/permissions/project` | Active projects where the caller holds every requested permission. |
| `GET /rest/api/3/user/permission/search` | Pages active users holding the requested permissions in a context. |

Malformed ids, unknown expansions, invalid holders, duplicate grants or scheme names, deleting the default or an assigned scheme, and unauthorized writes return Jira-shaped errors. Every scheme, grant and assignment change writes an action in the same transaction.

## Catalog and holders

- **Permissions:** Jira's 36 project permissions and 9 global permissions (`internal/store/permission_schemes.go`), then permissions active apps declare through Connect's `jiraProjectPermissions` and `jiraGlobalPermissions`. An app permission key is `{appKey}__{moduleKey}`. Schemes grant project permissions only.
- **Holders:** `anyone`, application role, assignee, group, group custom field, project lead, project role, reporter, service portal customer (`sd.customer.portal.only`), user, user custom field. Users, groups and roles must exist in the workspace. Group renames propagate, deleted principals are removed, and role-deletion swaps update grants atomically.
- **Default scheme:** every workspace has scheme `10000`. The Members role holds 33 work permissions; the Administrators role holds `ADMINISTER_PROJECTS`, `EDIT_WORKFLOW` and `EDIT_ISSUE_LAYOUT`. New projects get this scheme (`migrations/130_permission_schemes.sql`).

## Global permissions

- **Administer Jira** follows the organization and site administrator roles ([ADMIN.md](ADMIN.md)).
- **Other global permissions,** including app ones, are granted to groups or to everyone with Jira or Jira Service Management access (`migrations/193_global_permission_grants.sql`).
- **New sites** grant create shared objects, user picker, browse users, bulk change and team-managed project creation to everyone with Jira access.
- **App permissions:** an app global permission whose `defaultGrants` include `ALL` is granted to everyone with Jira access when the app is first installed.
- **Administrators** hold every global permission.

## Enforcement

- **One evaluator.** The PostgreSQL function `jira_has_project_permission` decides project permissions for REST, pages, search, enhanced-search snapshots, sync, board and release lists, and project navigation.
- **Browse projects** hides denied projects and work items before serialization.
- **Issue-dependent holders.** Assignee, reporter and custom field holders are checked against the specific work item on direct reads. In a project-only query, they match when any active work item in the project supplies the relationship, as in Jira.
- **Project configuration.** Administer projects (implied by Administer Jira) covers components, versions and release approvers, properties, features, sender email, details, project-scoped statuses, roles and effective-permission views. Administer Jira is still required for global statuses, project creation, categories, archive, trash, restore, delete, and scheme assignment (`internal/api3/project_administration_test.go`).
- **Work item actions.** `internal/commands` checks these for every caller — REST, pages, bulk tasks, automation (as the rule actor), plans and board drags (`internal/commands/permission_enforcement_test.go`):

  | Action | Permission |
  | --- | --- |
  | Create | Create issues; plus Assign issues for an assignee, Modify reporter for another reporter, Set issue security for a level, Schedule issues for a due date, Resolve issues for fix versions |
  | Edit fields | Edit issues; plus Set issue security for the security level, Schedule issues for the due date, Resolve issues for fix versions and the [resolution](ISSUE_METADATA.md#on-work-items) |
  | Assign | Assign issues; the assignee needs Assignable user |
  | Transition | Transition issues; plus Resolve issues when the transition sets a resolution, and Assign issues when it reassigns |
  | Board column moves | Schedule issues for the rank, and a workflow transition into the column's status, with everything that transition needs |
  | Rank, sprint and backlog moves | Schedule issues; sprint and backlog moves also Edit issues |
  | Sprints | Manage sprints in the board's project |
  | Links | Link issues on the outward work item (REST also asks Edit issues) |
  | Own watch and vote | None beyond seeing the work item ([ISSUE_SURFACE.md](ISSUE_SURFACE.md#watchers-and-assignment)) |
  | Others' watches | Manage watchers; the watcher must be able to see the work item |
  | Move | Move issues where the work item is, Create issues where it goes |
  | Delete | Delete issues |
  | Comments | Add comments; edit or delete all / own comments |
  | Attachments | Create attachments; delete all / own attachments |
  | Worklogs | Work on issues to log; edit or delete all / own worklogs |
  | Properties | Edit issues |

  A portal customer raises and transitions their own requests without project permissions; a service agent needs Service desk agent in the desk's project. Refusals are `403` except where Jira answers `400`: create, edit and transition, reported against the field when one is involved. Bulk operations also need the Bulk change global permission. The workflow validator `system:check-permission-validator` evaluates the project's scheme.

**Permission helper** (`/admin/permission-helper`). Given a person, a work item and a project permission, it says whether the person holds it and why: the administrator role, Administer Jira, or the named grants. Each grant is tested alone in a rolled-back savepoint using `jira_has_project_permission`. For Browse projects it also reports a security level that hides the work item.

**Copy.** The settings page copies one, named "Copy of X" (then "Copy 2 of X"), carrying its configuration and nothing else: the copy is assigned to no project and is never the site default.

## Gaps

Tracked in [PLAN.md](../PLAN.md).


## Tests

- `internal/api3/permission_schemes_test.go`: all 16 operations, validation, assignment conflicts, direct and group grants, delegated administration, visibility in work items, projects and search, discovery, paging, actions.
- `internal/api3/project_administration_test.go`: each project configuration operation for a project administrator who is not a site administrator, and for a member without the permission.
- `e2e/permission_schemes.spec.ts`: scheme creation, grant editing, assignment, delegated inspection, effective API permissions, 320px reflow.
