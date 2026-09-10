# Jira issue security schemes

Updated: 2026-09-10

ZZIRA stores reusable Jira issue security schemes, their visibility levels, the
holders granted each level, and the scheme assigned to every project. Site
administrators manage the catalog at `/settings/issue-security-schemes`.
Project administrators inspect the effective scheme at
`/projects/{key}/settings/issue-security`.

## Jira Cloud REST surface

This checkpoint implements all 20 pinned Jira Cloud issue-security operations:

| Method and path | Behavior |
|---|---|
| `GET/POST /rest/api/3/issuesecurityschemes` | Lists every scheme with its levels, or creates a scheme with optional levels and holders. |
| `GET /rest/api/3/issuesecurityschemes/search` | Pages schemes with `id` and `projectId` filters and returns each scheme's default level and project IDs. |
| `GET /rest/api/3/issuesecurityschemes/level` | Pages levels with `id`, `schemeId` and `onlyDefault` filters. |
| `PUT /rest/api/3/issuesecurityschemes/level/default` | Sets or clears the default level of several schemes at once. |
| `GET /rest/api/3/issuesecurityschemes/level/member` | Pages level members with `id`, `schemeId` and `levelId` filters. |
| `GET/PUT /rest/api/3/issuesecurityschemes/project` | Pages project-to-scheme mappings, or queues a project association with old-to-new level mappings. |
| `GET/PUT/DELETE /rest/api/3/issuesecurityschemes/{id}` | Reads an expanded scheme, updates its name and description, or deletes an unassigned scheme. |
| `GET /rest/api/3/issuesecurityschemes/{id}/members` | Pages one scheme's members, filtered by `issueSecurityLevelId`. |
| `PUT /rest/api/3/issuesecurityschemes/{id}/level` | Atomically adds levels, each with its own holders. |
| `PUT/DELETE /rest/api/3/issuesecurityschemes/{id}/level/{levelId}` | Updates a level, or queues its removal with a `replaceWith` remap. |
| `PUT /rest/api/3/issuesecurityschemes/{id}/level/{levelId}/member` | Atomically adds holders to a level. |
| `DELETE /rest/api/3/issuesecurityschemes/{id}/level/{levelId}/member/{memberId}` | Removes one unmanaged holder. |
| `GET /rest/api/3/securitylevel/{id}` | Reads one level by ID. |
| `GET /rest/api/3/project/{projectKeyOrId}/issuesecuritylevelscheme` | Returns a project's expanded scheme to a project administrator. |
| `GET /rest/api/3/project/{projectKeyOrId}/securitylevel` | Returns only the levels the calling user may apply in that project. |

Names are unique per workspace without regard to case. IDs are Jira-style
numeric values drawn from dedicated sequences. The API validates level and
holder shapes, active users, workspace groups, project roles, custom fields,
pagination, mapping completeness, assignment conflicts and deletion safety.
Scheme, level, holder and assignment mutations write immutable actions in the
same transaction.

Project association and level removal rewrite every affected work item, so both
are durable API tasks: they answer `303` with a `Location` header pointing at
`/rest/api/3/task/{id}`, and a second in-flight task for the same project or
level is rejected with `409`. Associating a scheme requires a mapping for every
level the project currently uses, which keeps work items from being left at a
level the new scheme does not define.

zzira's two pre-v3 extensions stay available for existing clients: `POST
/rest/api/3/issuesecurityschemes` still accepts a caller-supplied `id` with
account-ID level members, and `GET/PUT
/rest/api/3/issuesecurityschemes/project/{projectKeyOrId}` still reads and sets
a project's scheme by key. The legacy `PUT` assigns without level mappings, so
Jira Cloud clients should use the paginated `/issuesecurityschemes/project`
endpoint above.

## Holders and enforcement

Levels grant visibility through the nine Jira issue-security holder types:
`applicationRole`, `assignee`, `group`, `groupCustomField`, `projectLead`,
`projectRole`, `reporter`, `user` and `userCustomField`.

A single PostgreSQL function, `jira_issue_security_visible`, decides every
issue-security question. It resolves the project's scheme, the requested level,
the actor's groups, and each holder grant, and it always admits workspace
administrators and organization or site administrators. Work items with no
level are visible to anyone who can browse the project.

That one function backs JQL and search (`VisibleIssuePredicate`), the
synchronized action page that feeds replicas and the offline worker, notification
delivery, the security levels offered in the create-issue dialog, and the REST
issue reads — so a restricted work item disappears coherently from search,
boards, the navigator, sync, notifications and the API at once, rather than per
surface. Removing a user or group, or renaming a group, cleans up or renames the
matching holders through the same triggers that maintain role bindings and
permission grants.

Because visibility is now computed from the scheme actually assigned to the
project, a work item that carries a level its project's scheme does not define
is hidden from non-administrators instead of being treated as unrestricted. The
association and level-removal tasks remap those work items, so the case only
arises through the legacy assignment extension described above.

## Evidence and current boundary

- `internal/api3/issue_security_schemes_test.go` covers all 20 operations,
  permission rejection, validation, paging, filters, both durable tasks and
  their single-flight conflicts, cross-user issue visibility and the immutable
  action record.
- `e2e/issue_security_schemes.spec.ts` covers site scheme creation, a level with
  a contextual rule, project assignment, project-settings inspection, restricted
  creation from the modal, a second user's 404, the same user's 200 after being
  granted the level, 320 px reflow, unassignment and deletion. It provisions its
  own project so the journey starts from work that carries no security level.
- `migrations/132_issue_security_schemes.sql` is exercised from a clean
  PostgreSQL schema, and its backfill maps a pre-existing scheme's workspace,
  `isDefault` level and account-ID member arrays onto the new holder rows. Both
  pages are in the light and dark axe sweep.

The assessment remains partial while issue-security JQL functions, level
reordering, `expand` on the paged beans, the nested user/group/role/field
expansion beans, per-holder `managed` administration, scheme copy, and exact
Jira error wording and self links on every response remain. The browser
assignment form carries no level-remapping step, so assigning a scheme to a
project that already has restricted work is a REST operation; the form reports
the store's mapping error rather than silently moving those restrictions. Application-role
holders resolve against enabled site products rather than Atlassian
license-tier membership.
