# Custom field options

Select, multi-select and cascading select custom fields take their options from the [context](CUSTOM_FIELD_CONTEXTS.md) that governs the field, so one field can offer different options in different projects or work types. App-provided select lists use a separate resource ([APP_FIELD_OPTIONS.md](APP_FIELD_OPTIONS.md)). Part of the [Jira platform](JIRA_PLATFORM.md). For status, see [CLOUD_PARITY.md](CLOUD_PARITY.md).

## API

| Method and path | Behavior |
|---|---|
| `GET /rest/api/3/customFieldOption/{id}` | One option's `self` and `value`. Allowed for administrators, and for anyone who can browse a project the option's context applies to, where a field configuration that project uses shows the field. |
| `GET/POST/PUT /rest/api/3/field/{fieldId}/context/{contextId}/option` | `GET` pages options in display order (filters `optionId`, which returns an option and its cascading children, and `onlyOptions`, which omits children). `POST` appends options. `PUT` renames, enables or disables them. |
| `PUT /rest/api/3/field/{fieldId}/context/{contextId}/option/move` | Reorders with `after`, or `position` `First` or `Last`. |
| `DELETE /rest/api/3/field/{fieldId}/context/{contextId}/option/{optionId}` | Removes an option no work item holds. |
| `DELETE /rest/api/3/field/{fieldId}/context/{contextId}/option/{optionId}/issue` | Moves work items holding the option to `replaceWith`, then removes it, in one transaction. |

- The context routes need *Administer Jira*.
- Option values are unique per context, ignoring case. Every change is written to the action log.
- Options exist only on select, multi-select and cascading select fields. Asking for them on another field type is 400.
- An app-provided field is 400 here, and the error names `/rest/api/3/field/{fieldKey}/option`.

## Behavior

- The governing context's options, in their display order, are the field's `allowedValues` in `createmeta` and the create dialog choices.
- Commands accept only an option from the governing context on create, edit and transition.
- A **disabled** option leaves forms and `allowedValues`, but work items that already hold it keep it.
- Deleting an option still in use is refused unless the `/issue` form supplies a replacement.

### Multi-select

Type `multiselect`, or Jira's `multiselect` and `multicheckboxes` keys. It holds several options from its context. `createmeta` describes it as an `array` of `option`, and the create form shows it as a multiple choice.

### Cascading select

Type `cascadingselect`. It has two levels: an option created with `optionId` is a child of that first-level option in the same context. Children cannot have children, and only cascading select options can have a parent. The value is an option plus, optionally, one of its children.

## Values on work items

Accepted on create, edit and transition:

- An option: an option id, `{"id": ...}` or `{"value": ...}` from the governing context.
- Multi-select: a list of those. A single value counts as a list of one, and each option may appear once.
- Cascading select: `{"value": ..., "child": {"value": ...}}`, or the same with ids.
- Project picker: `{"id"}` or `{"key"}`.
- Version and multi-version pickers: versions of the work item's project, by `{"id"}` or `{"name"}`.

Responses use Jira's beans:

- Options: `{"self", "value", "id"}`, or a list of them. A cascading option also has `child`.
- Users: user beans. Groups: `{"groupId", "name", "self"}`.
- Projects and versions: project and version beans.

[JQL](JQL.md) matches option fields by option id or value with `=`, `!=`, `in`, `not in`, `is empty` and `is not empty`. Cascading selects also support `in cascadeOption(parent)`, `cascadeOption(parent, child)` and `cascadeOption(parent, none)`. Projects are matched by id or key, and versions by id or name. The changelog records option, user and group changes as `custom` items.

## UI

- `/settings/custom-fields`: for each context, add an option (under a first-level option, for a cascading select), move it up, down, first or last, disable or enable it, and delete it — naming the option that replaces it on work items that hold it, since an option in use is otherwise refused.
- Create form: shows a cascading select's first-level options.
- Work item page and edit dialog: every picker lists its choices (cascading pairs, people, groups, projects, the project's versions) and names the chosen values. When the choices are not loaded, as in the offline replica, the field is a plain input, so an edit never clears it.

## Code

`internal/api3/custom_field_options.go`, `internal/api3/custom_field_values.go`, `internal/web/custom_field_contexts.go` (the settings page), `migrations/137_custom_field_options.sql`; tests in `internal/api3/custom_field_contexts_test.go`, `custom_field_pickers_test.go`, `internal/web/custom_field_options_admin_test.go` and `e2e/custom_field_options.spec.ts`.

## Gaps

Tracked in [PLAN.md](../PLAN.md).

