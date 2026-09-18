# Issue security schemes

Jira issue security schemes, their levels, the holders granted each level, and the scheme assigned to each project. A work item with a security level is visible only to that level's holders. Site administrators manage schemes at `/settings/issue-security-schemes`; project administrators view a project's scheme at `/projects/{key}/settings/issue-security`. Part of the [Jira platform](JIRA_PLATFORM.md); related: [permission schemes](PERMISSION_SCHEMES.md). For status, see [CLOUD_PARITY.md](CLOUD_PARITY.md).

## API

All 20 operations of Jira's issue security groups:

| Method and path | Behavior |
| --- | --- |
| `GET/POST /rest/api/3/issuesecurityschemes` | Every scheme with its levels, or creates one with optional levels and holders. |
| `GET /rest/api/3/issuesecurityschemes/search` | Pages schemes by `id` and `projectId`, with default level and project ids. |
| `GET /rest/api/3/issuesecurityschemes/level` | Pages levels by `id`, `schemeId`, `onlyDefault`. |
| `PUT /rest/api/3/issuesecurityschemes/level/default` | Sets or clears the default level of several schemes. |
| `GET /rest/api/3/issuesecurityschemes/level/member` | Pages level members by `id`, `schemeId`, `levelId`. |
| `GET/PUT /rest/api/3/issuesecurityschemes/project` | Pages project-to-scheme mappings, or queues a project association with old-to-new level mappings. |
| `GET/PUT/DELETE /rest/api/3/issuesecurityschemes/{id}` | Reads (expandable), updates name and description, or deletes an unassigned scheme. |
| `GET /rest/api/3/issuesecurityschemes/{id}/members` | Pages a scheme's members, by `issueSecurityLevelId`. |
| `PUT /rest/api/3/issuesecurityschemes/{id}/level` | Adds levels with their holders atomically. |
| `PUT/DELETE /rest/api/3/issuesecurityschemes/{id}/level/{levelId}` | Updates a level, or queues its removal with a `replaceWith` level. |
| `PUT /rest/api/3/issuesecurityschemes/{id}/level/{levelId}/member` | Adds holders atomically. |
| `DELETE /rest/api/3/issuesecurityschemes/{id}/level/{levelId}/member/{memberId}` | Removes one holder; `managed` holders cannot be removed. |
| `GET /rest/api/3/securitylevel/{id}` | One level. |
| `GET /rest/api/3/project/{projectKeyOrId}/issuesecuritylevelscheme` | A project's scheme, for project administrators. |
| `GET /rest/api/3/project/{projectKeyOrId}/securitylevel` | The levels the caller may set in that project. |

- **Names and ids:** names are unique per workspace ignoring case. Ids are numeric, from dedicated sequences.
- **Validation:** level and holder shapes, users, groups, project roles, custom fields, paging, mapping completeness, assignment conflicts and deletes.
- **Expansions:** member reads expand `user`, `group`, `projectRole`, `field` or `all`, as permission grants do.
- **Tasks:** project association and level removal rewrite affected work items, so both are tasks. They answer 303 with `Location: /rest/api/3/task/{id}`. A second in-flight task for the same project or level is 409.
- **Mapping:** association needs a mapping for every level the project's work items use.
- **Audit:** every change writes an action in the same transaction.

## Holders

The nine Jira holder types: `applicationRole`, `assignee`, `group`, `groupCustomField`, `projectLead`, `projectRole`, `reporter`, `user`, `userCustomField`. Application role holders match people with access to an enabled site product. Removing a user or group, or renaming a group, updates matching holders through the same triggers that maintain role bindings and permission grants.

## Enforcement

- **One function decides.** The PostgreSQL function `jira_issue_security_visible` resolves the project's scheme, the level, the actor's groups and each holder. Workspace, site and organization administrators always pass.
- **No level** means anyone who can browse the project can see the work item.
- **A level the project's scheme does not define** hides the work item from non-administrators.
- **Where it applies:** JQL and search (`VisibleIssuePredicate`), the sync action stream that feeds replicas and the offline worker, notification delivery, the levels offered on create, and REST reads. A restricted work item disappears from search, boards, the navigator, sync, notifications and the API together.

**Copy.** The settings page copies one, named "Copy of X" (then "Copy 2 of X"), carrying its configuration and nothing else: the copy is assigned to no project and is never the site default.

## Gaps

Tracked in [PLAN.md](../PLAN.md).

- The browser's assign-to-project form sends no level mapping, so assigning a scheme to a project whose work items already have levels fails with the mapping error. Only the REST association can remap levels.

## Tests

- `internal/api3/issue_security_schemes_test.go`: all 20 operations, permission refusals, validation, paging, filters, both tasks and their conflicts, cross-user visibility, actions.
- `e2e/issue_security_schemes.spec.ts`: scheme and level creation, project assignment, project settings view, restricted creation, another user's 404 and then 200 once granted, unassignment and deletion, 320px reflow. Both pages are in the light and dark axe sweep.
- `migrations/132_issue_security_schemes.sql`: schema, holder rows and the visibility function.
