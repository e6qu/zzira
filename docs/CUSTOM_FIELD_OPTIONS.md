# Jira select custom fields and their options

Updated: 2026-09-11

ZZIRA custom fields were text, number, or datetime. A **select** field holds one
of a fixed set of choices, and those choices belong to the **context** that
governs the field, so the same field can offer different options in different
projects or work types.

Administrators manage options alongside contexts at `/settings/custom-fields`.
Contexts themselves are described in
[CUSTOM_FIELD_CONTEXTS.md](CUSTOM_FIELD_CONTEXTS.md).

## Jira Cloud REST surface

This checkpoint implements all seven pinned custom field option operations:

| Method and path | Behavior |
|---|---|
| `GET /rest/api/3/customFieldOption/{id}` | Reads one option by its own ID. |
| `GET/POST/PUT /rest/api/3/field/{fieldId}/context/{contextId}/option` | Pages a context's options in display order, appends options, or renames and enables or disables them. |
| `PUT /rest/api/3/field/{fieldId}/context/{contextId}/option/move` | Reorders options with `after`, or `position` First or Last. |
| `DELETE /rest/api/3/field/{fieldId}/context/{contextId}/option/{optionId}` | Removes an option that no work item holds. |
| `DELETE /rest/api/3/field/{fieldId}/context/{contextId}/option/{optionId}/issue` | Moves the work items holding the option to `replaceWith`, then removes it. |

Option values are unique per context without regard to case, IDs come from a
dedicated sequence, and every mutation writes an immutable action in the same
transaction. Only a select field has options; asking for them on a text field is
a validation error rather than an empty list.

## What the choices govern

The options of the **governing context** become the field's allowed values in
`createmeta` and the choices in the create dialog, in the order an administrator
arranged them. The command path accepts only an option that context offers, so
create, edit, and transition reject a value borrowed from another project's
context or invented by a client.

**A disabled option keeps existing work items valid but can no longer be
chosen.** It leaves the form and the allowed values while the work items holding
it keep their value, which is what makes retiring a choice safe.

Deleting an option that work items still hold is refused. Jira's
`.../option/{id}/issue` form supplies a replacement: the work items move to it
first, in the same transaction, so no work item is left holding a value its
field no longer offers.

## Evidence and current boundary

- `internal/api3/custom_field_contexts_test.go` covers all seven operations,
  the select-only guard, duplicate values, every move form, the unknown-option
  rejections, the disabled-option rule, and the replacement migration, alongside
  the context operations the options hang from.
- `e2e/custom_field_options.spec.ts` covers the browser journey: add options,
  reorder them, watch the create dialog offer exactly those choices in that
  order, create a work item with one, disable it and watch it leave the form
  while the work item keeps it, and reflow at 320 px.
- `migrations/137_custom_field_options.sql` is exercised from a clean PostgreSQL
  schema.

## Multi-select fields

A field created with type `multiselect`, or Jira's
`com.atlassian.jira.plugin.system.customfieldtypes:multiselect` and
`multicheckboxes` keys, holds several options of its context. Its options are
managed exactly like a select field's, and createmeta describes it as an
`array` of `option` with the context's options as `allowedValues`; the create
form offers them as a multiple choice.

## Cascading select fields

A `cascadingselect` field has two levels of options. An option created with
`optionId` is a child of that first-level option of the same context; a child
cannot have children, and only a cascading select's options can have a parent.
Its value is an option and optionally one of that option's children.

## Values on work items

Option fields accept Jira's value forms on create, edit and transition: an
option id, `{"id": ...}` or `{"value": ...}` naming an option of the governing
context; for a multi-select a list of them (a single one is taken as a list of
one, each chosen once); and for a cascading select
`{"value": ..., "child": {"value": ...}}` or the same with ids. Issue responses
describe an option as Jira does — `{"self", "value", "id"}`, a list of those
for a multi-select, and a cascading option carrying its `child` — and user and
group picker values as user beans and `{"groupId", "name", "self"}`.

JQL matches an option field by option id or option value: `=`, `!=`, `in`,
`not in`, `is empty` and `is not empty`, and for a cascading select
`in cascadeOption(parent)`, `cascadeOption(parent, child)` and
`cascadeOption(parent, none)`. Changes appear in the changelog as `custom` items
naming the options, people or groups.

The create form offers a cascading select's first-level options; its child is
set through the API. Jira's `optionId` filter on the option list, `expand`, and
exact Jira error wording remain.
