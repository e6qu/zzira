# App-provided select lists and their options

Updated: 2026-09-11

A Connect app can declare a select-list work-item field, and Jira's issue field
option surface manages that list's options. This is deliberately a different
resource from the [context-scoped options](CUSTOM_FIELD_OPTIONS.md) an
administrator manages, and Jira says so in every operation's description.

## An app can now declare a select list

`translateConnectIssueField` accepted `string`, `text`, `rich_text`, `number`,
`date` and `datetime`. A descriptor declaring `single_select` was refused, so
there was no field for this surface to serve. It is accepted now and maps to
this product's select type.

`multi_select` stays refused. There is no multi-select field here, and quietly
downgrading one to a single choice would give an app a field that does not
behave as its descriptor says.

## Jira Cloud REST surface

All eight pinned operations are implemented. An audit against a running server
found none of them working.

| Method and path | Behavior |
|---|---|
| `GET /rest/api/3/field/{fieldKey}/option` | Pages the select list's options. |
| `POST /rest/api/3/field/{fieldKey}/option` | Adds an option, with app properties and a scope. |
| `GET /rest/api/3/field/{fieldKey}/option/{optionId}` | Reads one option. |
| `PUT /rest/api/3/field/{fieldKey}/option/{optionId}` | Replaces an option's value, properties and config. |
| `DELETE /rest/api/3/field/{fieldKey}/option/{optionId}` | Removes an option; one still in use is a 409. |
| `GET /rest/api/3/field/{fieldKey}/option/suggestions/search` | Pages the options a user may see. |
| `GET /rest/api/3/field/{fieldKey}/option/suggestions/edit` | Pages the options a user may choose. |
| `DELETE /rest/api/3/field/{fieldKey}/option/{optionId}/issue` | Deselects the option from work items, optionally replacing it and optionally narrowed by JQL. |

## The two option resources do not overlap

This surface answers only for a select list an app provides, and refuses a field
created here. The context option surface does the opposite: asked about an
app's field it answers 400 and names this path instead. Either resource
answering for the other's fields would let a client edit options through a
route that was never meant to govern them.

They share one option table, because downstream nothing cares who supplied the
field: the create form, write validation and search resolve a select value the
same way either way. What separates the two resources is the field, not the
storage.

## Deselecting runs as a real background task

Jira's deselect is asynchronous and answers `303` with a link to a task. It is
asynchronous here too, and it is not a path of its own: the affected work items
are collected — narrowed by the JQL query when one is given — and queued as an
ordinary bulk edit, which is the machinery that already applies field writes,
permissions and validation.

A replacement that cannot be selected is refused before the queue. Letting it
through produced a deselect that reported success while every work item stayed
on the option it was meant to leave, because the per-item write was refused.
That is worse than saying no, so the check happens up front.

## Scope and attributes

An option's `config.scope.projects` limits the projects it is offered in; an
option with no scope is offered everywhere, which is what the absence of one
means. `config.attributes` carries Jira's deprecated `notSelectable`, which is
what separates the two suggestion endpoints: `search` reports what a user may
see, `edit` only what they may choose.

## Evidence and current boundary

- `internal/api3/app_field_options_test.go` installs an app that declares a
  `single_select` field and covers all eight operations, the duplicate and empty
  value, the 404 for an unknown option and an unknown field, the administration
  permission, both directions of the separation from context options, the 409
  for an option in use, the per-option project scope, the unselectable option
  leaving `suggestions/edit`, the refused self- and unselectable replacement,
  and the deselect actually moving a work item onto its replacement.
- `migrations/144_app_field_options.sql` is exercised from a clean PostgreSQL
  schema.

Jira's `projects2` scope form with per-project attributes, the `defaultValue`
attribute, `overrideScreenSecurity` and `overrideEditableFlag`, option property
indexes for JQL, and `multi_select` app fields remain.
