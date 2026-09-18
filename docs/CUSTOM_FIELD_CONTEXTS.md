# Custom field contexts

A custom field context decides whether a custom field reaches a project and work type at all, which default value it starts with there, and which options it offers ([CUSTOM_FIELD_OPTIONS.md](CUSTOM_FIELD_OPTIONS.md)). [Screens](SCREENS.md) then decide where the field appears on a form, and [field configurations](FIELD_CONFIGURATIONS.md) whether it is required or hidden. Part of the [Jira platform](JIRA_PLATFORM.md). For status, see [CLOUD_PARITY.md](CLOUD_PARITY.md).

## API

All operations need *Administer Jira*.

| Method and path | Behavior |
|---|---|
| `GET/POST /rest/api/3/field/{fieldId}/context` | Pages a field's contexts (filters `contextId`, `isAnyIssueType`, `isGlobalContext`), or creates one scoped to projects and work types. |
| `PUT/DELETE /rest/api/3/field/{fieldId}/context/{contextId}` | Changes name and description, or deletes. |
| `PUT /rest/api/3/field/{fieldId}/context/{contextId}/project` | Adds projects. |
| `POST /rest/api/3/field/{fieldId}/context/{contextId}/project/remove` | Removes projects. |
| `PUT /rest/api/3/field/{fieldId}/context/{contextId}/issuetype` | Adds work types. |
| `POST /rest/api/3/field/{fieldId}/context/{contextId}/issuetype/remove` | Removes work types. |
| `GET/PUT /rest/api/3/field/{fieldId}/context/defaultValue` | Reads or sets each context's default value. |
| `GET /rest/api/3/field/{fieldId}/context/defaultValues` | Same read under Jira's second path. |
| `GET /rest/api/3/field/{fieldId}/context/issuetypemapping` | Pages the work types each context covers. |
| `GET /rest/api/3/field/{fieldId}/context/projectmapping` | Pages the projects each context covers. |
| `POST /rest/api/3/field/{fieldId}/context/mapping` | Returns the governing context for project and work type pairs. |
| `GET /rest/api/3/field/{fieldId}/contexts` | Pages the field's contexts with scope ([ISSUE_FIELDS.md](ISSUE_FIELDS.md)). |

Context names are unique per field, ignoring case. Every change is written to the action log in the same transaction.

## Behavior

- A context with no projects applies to every project; one with no work types applies to every work type. Removing the last project or work type returns the context to "all".
- No two contexts of a field may cover the same project and work type. A create or widen that would overlap is 409 (`assertNoContextOverlapTx`, `internal/store/custom_field_contexts.go`). A global context therefore blocks any other context on that field.
- A custom field always keeps at least one context. A new custom field gets a global context from a database trigger.
- The SQL function `jira_custom_field_context(field, project, work type)` returns the governing context, preferring one that names the project, then one that names the work type. Everything below uses it:
  - `createmeta` and the create dialog leave out a field whose context does not reach the project and work type.
  - The context's default value is added to the field metadata and fills the create dialog, unless the request already has a value.
  - Service request type forms and `CustomFieldsForProject`.
  - Commands refuse a create, edit or transition that sets a field outside its context, so REST clients cannot bypass the scope.
  - Bulk edit offers only fields in context for every selected work item.

## UI

`/settings/custom-fields`: per field, create and delete contexts; add or remove projects and work types; set the default value; manage options; for Assets fields, choose whether a context holds one object or several.

## Code

`internal/api3/custom_field_contexts.go`, `internal/store/custom_field_contexts.go`, `internal/web/custom_field_contexts.go`, `migrations/136_custom_field_contexts.sql`; tests in `internal/api3/custom_field_contexts_test.go` and `e2e/custom_field_contexts.spec.ts`.

## Gaps

Tracked in [PLAN.md](../PLAN.md).

- The overlap rule differs from Jira. Jira allows one global context alongside project-scoped contexts, which take precedence, and forbids only one project being in two contexts.
