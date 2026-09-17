# Anonymous access

Jira's REST specification marks which operations "can be accessed anonymously". ZZIRA serves those reads to callers with no credentials, as Jira's anonymous user, subject to permission scheme grants to `anyone`. Part of the [Jira platform](JIRA_PLATFORM.md); see [permission schemes](PERMISSION_SCHEMES.md). For status, see [CLOUD_PARITY.md](CLOUD_PARITY.md).

## Which operations

`api/conformance/anonymous_operations.py` reads `api/specs/jira-v3.json` and generates `internal/api3/anonymous_operations_generated.go`: every GET the specification opens to anonymous callers, plus the POSTs that only read (searches, JQL parsing, expression evaluation, bulk work item and comment reads, changelog lists, permission checks). CI runs it with `--check`, so the table always matches the specification.

- A request is anonymous only with no credential at all: no `Authorization` header, no session cookie, no signed app principal.
- A credential that fails verification is 401; it is never treated as anonymous.
- Every other operation, and every write, requires an account.

## What the anonymous user sees

The anonymous user holds no global permissions, is in no group or role, and is never a project lead, assignee, reporter or administrator. Access comes only from grants to `anyone` ("Anyone on the web"). The same checks apply to signed-in callers:

- **Browse projects:** needed for projects, components, versions, project properties, statuses and JQL project suggestions. Hidden projects are 404 and drop out of lists.
- **Browse projects and issue security:** needed for work items, comments, worklogs, properties, remote links, changelogs, votes and watchers. Search results are filtered the same way.
- **Action permissions:**
  - transitions are listed only with Transition issues;
  - editable fields only with Edit issues;
  - voters and watchers only with View voters and watchers;
  - create metadata only for projects with Create issues.
- **People:** without Browse users and groups, user search and browse-user search return nothing, the user picker matches only an exact display name, and the group-and-user picker is refused. Assignable-user search needs Browse projects in every requested project.
- **Email addresses** are shown only to the person and to administrators.
- **Filters and dashboards** can be shared at most with signed-in users, as in Jira Cloud, so anonymous callers see none. Recent projects and favourite filters are empty, and viewing a project records no history.

## Gaps

Tracked in [PLAN.md](../PLAN.md).

- Anonymous access is REST-only. Browser pages such as `/browse/{key}` redirect a signed-out visitor to `/login` even when the project grants Browse projects to `anyone` (`internal/web/web.go` `BrowseIssue`).

## Tests

`internal/api3/anonymous_access_test.go` calls every operation in the table without credentials, against a project granting Browse projects to `anyone` and a private project. It fails on a 401, a server error, or any response exposing the private project, its work or an email address. It also checks public work item, search, project, component and permission reads, and that writes, other operations and bad credentials stay unauthorized.
