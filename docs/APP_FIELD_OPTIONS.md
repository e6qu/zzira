# App-provided select list options

A Connect app can declare a select-list work item field. Its options are managed through Jira's issue field option resource, `/field/{fieldKey}/option`. This resource is separate from the [context-scoped options](CUSTOM_FIELD_OPTIONS.md) that administrators manage. Part of the [Jira platform](JIRA_PLATFORM.md) and [apps](APPS.md). For status, see [CLOUD_PARITY.md](CLOUD_PARITY.md).

## Declaring the field

`translateConnectIssueField` (`internal/apps/connect_descriptor.go`) accepts the descriptor types `string`, `text`, `rich_text`, `single_select`, `multi_select`, `number`, `date` and `datetime`. `single_select` maps to the select type and `multi_select` to the multi-select type. This resource serves both.

## API

The app that provides the field needs no Jira permission. Anyone else must be a site administrator. The two suggestion reads need only site access.

| Method and path | Behavior |
|---|---|
| `GET /rest/api/3/field/{fieldKey}/option` | Pages the options. |
| `POST /rest/api/3/field/{fieldKey}/option` | Adds an option with app `properties` and `config`. Duplicate or empty values are 400. |
| `GET /rest/api/3/field/{fieldKey}/option/{optionId}` | Reads one option. |
| `PUT /rest/api/3/field/{fieldKey}/option/{optionId}` | Replaces value, properties and config. |
| `DELETE /rest/api/3/field/{fieldKey}/option/{optionId}` | Removes an option; one still in use is 409. |
| `GET /rest/api/3/field/{fieldKey}/option/suggestions/search` | Pages options the user may see. |
| `GET /rest/api/3/field/{fieldKey}/option/suggestions/edit` | Pages options the user may select. |
| `DELETE /rest/api/3/field/{fieldKey}/option/{optionId}/issue` | Deselects the option on work items, optionally setting `replaceWith` and optionally limited by `jql`. Answers `303` with a task. |

Unknown fields and options are 404.

## Behavior

- **Separation.** This resource refuses fields that were not provided by an app. The context option resource refuses app fields with 400 and points to this path. Both share one option table, so forms, write validation and search handle a select value the same way either way.
- **Scope.** `config.scope.projects` limits the projects an option is offered in. An option with no scope is offered everywhere.
- **Attributes.** `config.attributes` can hold `notSelectable`. Such options appear in `suggestions/search` but not in `suggestions/edit`.
- **Deselect.** The affected work items are collected (filtered by `jql` if given) and queued as an ordinary [bulk edit](BULK_ISSUES.md), which applies permissions and validation. If `replaceWith` is the option itself or cannot be selected, the request is refused before anything is queued.
- **Overrides.** Deselect accepts `overrideScreenSecurity` and `overrideEditableFlag`. Only a Connect or Forge app with *Administer Jira* may pass them; anyone else gets 403. With them, the queued edit sets the field even where the field configuration hides it, and on work items whose status is not editable.

## Code

`internal/api3/app_field_options.go`, `internal/store/app_field_options.go`, `migrations/144_app_field_options.sql`; tests in `internal/api3/app_field_options_test.go`.

## Gaps

Tracked in [PLAN.md](../PLAN.md).

- The `projects2` scope form with per-project attributes.
- The `defaultValue` option attribute.
- Option property indexes for JQL.
