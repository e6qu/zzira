# Issue fields

Field administration: listing, creating, searching, renaming, trashing and deleting work item fields. Which projects and work types a field reaches, and the options it offers, is set by [custom field contexts](CUSTOM_FIELD_CONTEXTS.md) and [options](CUSTOM_FIELD_OPTIONS.md). Where a field appears on a form is set by [screens](SCREENS.md), and whether it is required or hidden by [field configurations](FIELD_CONFIGURATIONS.md). Part of the [Jira platform](JIRA_PLATFORM.md). For status, see [CLOUD_PARITY.md](CLOUD_PARITY.md).

## API

| Method and path | Behavior |
|---|---|
| `GET /rest/api/3/field` | System fields plus the site's custom fields. The system field set is the one JQL resolves, `components` included, so it also drives `expand=names,schema`. |
| `POST /rest/api/3/field` | Creates a custom field. |
| `GET /rest/api/3/field/search` | Pages custom fields. Filters: `query`, `type`, `id`, `projectIds`. `orderBy`: `name`, `description`, `contextsCount`, `screensCount`, `lastUsed` (prefix `-` for descending). `expand`: `key`, `stableId`, `lastUsed`. |
| `GET /rest/api/3/field/search/trashed` | Pages trashed fields. |
| `PUT /rest/api/3/field/{fieldId}` | Changes `name`, `description` or `searcherKey`. An empty rename is 400. |
| `GET /rest/api/3/field/{fieldId}/association/project` | Pages the projects the field's contexts reach. |
| `GET /rest/api/3/field/{fieldId}/contexts` | Pages the field's contexts with their scope. |
| `POST /rest/api/3/field/{id}/trash` | Moves a field to the trash. |
| `POST /rest/api/3/field/{id}/restore` | Restores a trashed field. |
| `DELETE /rest/api/3/field/{id}` | Deletes a trashed field. |
| `GET /rest/api/3/projects/fields` | Pages which fields apply to which project and work type, and whether they are required. Filters: `projectId`, `workTypeId`, `fieldId`. |

Everything except `GET /field` and `GET /projects/fields` needs *Administer Jira*. An unknown field is 404, except on the operations that publish no 404: the project associations answer 400, and `/field/{fieldId}/screens` and `/field/{fieldId}/contexts` answer an empty page.

## Environment

Environment is a system rich-text field, stored as an ADF document or cleared with `null`. It ships on the site's default screen, as Jira's does, so a project that has not customized its screens accepts it; add or remove it elsewhere through screen administration. Create and edit commands enforce those screen assignments; field configurations control required/hidden behavior and text/wiki rendering.

`fields.environment` is accepted on issue create and update. Update also accepts one `update.environment` `set` operation; mixing both forms, list operations and non-ADF values is refused atomically. Work item reads, search projections, `renderedFields`, expressions (`issue.environment.plainText`), workflow field updates/copies and automation edit actions use the persisted value. Changes carry system-field changelog entries in the same action transaction.

The create form, edit dialog and transition screen collect Environment as text and store it as ADF. Submitting unchanged Environment or Description text keeps the existing rich document. JQL `environment ~ "text"`, `environment is EMPTY` and `text ~ "text"` search Environment text without indexing its ADF structure. Hidden Environment is omitted from browser view and edit controls.

The local replica receives the field value through the existing issue action payload. Offline Environment controls and offline screen/renderer configuration remain part of [local-first delivery](../PLAN.md#14-local-first-closure).

Evidence: `internal/web/environment_test.go`, `internal/commands/workflow_fields_test.go` and `e2e/environment.spec.ts`.

## Trash

- `trashed_at` marks a trashed field. Trashed fields leave forms, screens, `createmeta` and search. Values on work items are kept, so restoring a field brings its data back.
- `DELETE` refuses a field that is not in the trash.
- `DELETE` answers `303` with a `Location` task that is already `COMPLETE`, because the removal finishes before the response.

## Translations

`translatedName` and `translatedDescription` are what the caller's own
language calls the field: their chosen locale, or the one their client asked
for in `Accept-Language`. `name` stays what the site calls it. An
administrator writes the translations on the custom fields page; a locale
with a region falls back to the language alone, so a `pt-BR` reader sees a
`pt` name when nobody wrote a Brazilian one.

## Type keys

`POST /field` and the `type` filter accept Jira's canonical key (for example `com.atlassian.jira.plugin.system.customfieldtypes:textfield`) or the short internal type name. The field keeps the key it was created with and reports it as `schema.custom`.

| Jira type keys | Value | Schema |
| --- | --- | --- |
| `textfield`, `textarea`, `readonlyfield` | text | `string` |
| `url` | absolute http or https URL | `string` |
| `float`, `importid` | number | `number` |
| `datetime` | date and time | `datetime` |
| `datepicker` | `yyyy-MM-dd` | `date` |
| `select`, `radiobuttons` | option | `option` |
| `multiselect`, `multicheckboxes` | options | `array` of `option` |
| `cascadingselect` | option plus optional child | `option-with-child` |
| `userpicker` / `multiuserpicker` | person / people | `user` / `array` of `user` |
| `grouppicker` / `multigrouppicker` | group / groups | `group` / `array` of `group` |
| `labels` | labels | `array` of `string` |
| `project` | project | `project` |
| `version` / `multiversion` | version(s) of the work item's project | `version` / `array` of `version` |
| `com.atlassian.teams:rm-teams-custom-field-team` | Atlassian team of the site | `team` |
| `com.atlassian.jira.plugins.cmdb:cmdb-object-cf` | Assets object | `any` |

Short keys in the table are under `com.atlassian.jira.plugin.system.customfieldtypes:`. Any other key (for example `daterange`) is 400; it is never stored as a different type. App-provided fields are covered in [APP_FIELD_OPTIONS.md](APP_FIELD_OPTIONS.md) and [JIRA_PLATFORM.md](JIRA_PLATFORM.md#app-custom-field-configuration-and-values).

## Searchers

`searcherKey`, on create or update, must be one Jira allows for the field's type; anything else is 400. It decides how [JQL](JQL.md) searches the field:

- Exact number and exact text searchers match values.
- Number, date and version range searchers also allow `>`, `>=`, `<`, `<=`.
- Text searchers match only with `~`.
- A field without a searcher keeps its type's operators. Autocomplete offers the same operators.

## UI

`/settings/custom-fields` (site administration) creates a custom field of any type the forms render, renames and describes it, manages its [contexts](CUSTOM_FIELD_CONTEXTS.md#ui) and [options](CUSTOM_FIELD_OPTIONS.md#ui), and moves it through the trash: a trashed field keeps its values and leaves every form until it is restored, and deleting it removes its values for good. An app's fields are managed by the app, so they have no trash action. The system fields with a catalogue of their own — work type, priority and resolution — are administered on their own pages ([ISSUE_METADATA.md](ISSUE_METADATA.md#settings-pages)).

## Code

`internal/api3/fields.go`, `internal/api3/api3_v5.go` (list and create), `internal/store/fields.go`, `migrations/143_custom_field_trash.sql`; tests in `internal/api3/fields_test.go`, `field_searchers_test.go`, `custom_field_types_test.go`.
