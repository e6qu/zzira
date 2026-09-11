# Jira custom field contexts

Updated: 2026-09-11

A screen decides which fields a form shows and a field configuration decides how
each behaves. A **custom field context** decides something earlier: whether a
custom field reaches a project and work type at all, and what value it starts
with there.

Site administrators manage contexts at `/settings/custom-fields`.

## Jira Cloud REST surface

This checkpoint implements all 14 pinned custom field context operations:

| Method and path | Behavior |
|---|---|
| `GET/POST /rest/api/3/field/{fieldId}/context` | Pages a field's contexts, or creates one scoped to projects and work types. |
| `PUT/DELETE /rest/api/3/field/{fieldId}/context/{contextId}` | Updates a context's name and description, or deletes it. |
| `PUT /rest/api/3/field/{fieldId}/context/{contextId}/project` | Adds projects to a context. |
| `POST /rest/api/3/field/{fieldId}/context/{contextId}/project/remove` | Removes projects from a context. |
| `PUT /rest/api/3/field/{fieldId}/context/{contextId}/issuetype` | Adds work types to a context. |
| `POST /rest/api/3/field/{fieldId}/context/{contextId}/issuetype/remove` | Removes work types from a context. |
| `GET/PUT /rest/api/3/field/{fieldId}/context/defaultValue` | Reads or sets the default value each context supplies. |
| `GET /rest/api/3/field/{fieldId}/context/defaultValues` | Reads the same defaults under Jira's second path. |
| `GET /rest/api/3/field/{fieldId}/context/issuetypemapping` | Pages the work types each context covers. |
| `GET /rest/api/3/field/{fieldId}/context/projectmapping` | Pages the projects each context covers. |
| `POST /rest/api/3/field/{fieldId}/context/mapping` | Resolves the governing context for given project and work type pairs. |

Context names are unique per field without regard to case, IDs come from a
dedicated sequence, and every mutation writes an immutable action in the same
transaction.

## Resolution, and the rule that keeps it unambiguous

`jira_custom_field_context` answers one question — which context governs this
field for this project and work type — and prefers a context that names the
project or work type over one that covers everything. A context that lists no
projects applies to every project, and the same for work types; removing the
last entry returns it to that state, which is what Jira means by removing them.

**At most one context may cover a given project and work type.** Creating or
widening a context that would overlap another is rejected, so resolution never
has to break a tie. A custom field also always keeps at least one context.

That resolution binds three surfaces:

- `IssueCreateMetadata` drops a custom field whose context does not reach the
  project and work type, so the create dialog and `createmeta` agree.
- The governing context's default value is stamped onto the field metadata and
  pre-fills the create dialog, unless the request already carries a value.
- Service request type forms and `CustomFieldsForProject` resolve through the
  same function.

## What replaced the old model

Custom fields were previously scoped by a `field_contexts(field_id, project_id)`
table with no work types, no name, and no defaults, consulted by two queries
that each re-implemented "no rows means global". The migration carries those
rows into the richer model — a field with project rows becomes a project-scoped
context, everything else global — drops the old table, and points both queries
at the shared function. A newly created custom field is provisioned with a
global context by trigger.

## Evidence and current boundary

- `internal/api3/custom_field_contexts_test.go` covers all 14 operations,
  permission rejection, unknown fields, projects and work types, the overlap
  refusal, the last-context guard, and the binding: narrowing a context removes
  the field from another project's `createmeta`, a work-type-scoped context does
  not reach other work types, and a default reaches the field metadata.
- `e2e/custom_field_contexts.spec.ts` covers the browser journey: a new field
  reaches both projects, narrowing its context removes it from one, a default
  pre-fills the create dialog, REST agrees, and the page reflows at 320 px.
- `migrations/136_custom_field_contexts.sql` is exercised from a clean
  PostgreSQL schema. The page is in the light and dark axe sweep.

The seven `Issue custom field options` operations are **not** implemented and
are not assessed. They administer the options of a select-style field, and ZZIRA
custom fields are text, number, or datetime only; there is no option-bearing
field type for them to govern. Implementing them would mean inventing a field
type first, which is its own piece of work rather than a shell around an empty
concept.

Contexts govern which custom fields a form offers, including bulk edit, but the
single-item command path does not yet reject a REST write that sets a custom
field outside its context, the way field configurations reject a hidden field.
`expand` and `orderBy` on these
endpoints, Jira's context-scoped field values on issues, and exact Jira error
wording also remain.
