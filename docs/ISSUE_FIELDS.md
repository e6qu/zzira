# Issue fields

Updated: 2026-09-11

ZZIRA's work-item fields are discovered, created, searched, renamed and retired
through Jira's field administration surface. What a field means once it exists
— which projects and work types it reaches, which options it offers, whether a
form requires it — belongs to [custom field
contexts](CUSTOM_FIELD_CONTEXTS.md), [screens](SCREENS.md) and [field
configurations](FIELD_CONFIGURATIONS.md); this document covers the field itself.

## Jira Cloud REST surface

All 11 pinned issue-field operations are implemented. An audit against a
running server found two working, `GET /field` and `POST /field`; the rest
answered 404, and two answered 405 with a message saying the operation was not
supported.

| Method and path | Behavior |
|---|---|
| `GET /rest/api/3/field` | Lists the system fields and the workspace's custom fields. |
| `POST /rest/api/3/field` | Creates a custom field. |
| `GET /rest/api/3/field/search` | Pages custom fields, filtered by `query`, `type`, `id` and `projectIds` and ordered by `name` or `description`. |
| `GET /rest/api/3/field/search/trashed` | Pages the fields in the trash. |
| `PUT /rest/api/3/field/{fieldId}` | Renames or re-describes a field. |
| `GET /rest/api/3/field/{fieldId}/association/project` | Pages the projects the field's contexts reach. |
| `GET /rest/api/3/field/{fieldId}/contexts` | Pages the field's contexts with their scope. |
| `POST /rest/api/3/field/{id}/trash` | Moves a field to the trash. |
| `POST /rest/api/3/field/{id}/restore` | Restores a field from the trash. |
| `DELETE /rest/api/3/field/{id}` | Deletes a field that is already in the trash. |
| `GET /rest/api/3/projects/fields` | Pages which fields apply to which project and work type. |

## The trash is a state, not a delete

Jira does not delete a custom field on request. It moves the field to the
trash, where it stops reaching forms, screens, metadata and search, and can be
restored. Only a field already in the trash can be deleted, and that is what
stops one mistaken call from destroying the values recorded against it.

That is the model here. `trashed_at` on the field is the state; every read that
serves live work excludes a trashed field, and the values already on work items
are untouched, so a restore brings the field back with its data. `DELETE`
refuses a field that is not in the trash.

Jira answers the delete with `303` and a task descriptor because the removal
runs in the background there. It completes before the response here, so the
task is reported finished rather than pending — a client that follows the
`Location` sees a completed task rather than one that never progresses.

## Field discovery reports what the search knows

`GET /field` used to return five hand-written system fields. It now returns the
same system field set the search resolves through, so field discovery cannot
advertise a field the search does not know, or omit one it does. `components`
was on work items and filterable in JQL but missing from that definition; it is
there now, which also puts it in `expand=names,schema`.

## Jira's type keys

A client configured against Jira sends the canonical custom field type key, for
example `com.atlassian.jira.plugin.system.customfieldtypes:textfield`.
`POST /field` used to accept only this product's short type names and rejected
every such request. Both forms are now accepted, mapped onto the four types
this product serves: text, number, datetime and select. A key for a type this
product does not have — a cascading select, say — is still refused, because
creating a field that cannot behave as asked would be worse than saying no.

`GET /field/search?type=` resolves the same way, so the filter matches what the
create accepts.

## A routing bug this found

`GET /field/{fieldId}/contexts` answered 405. The custom field context router
added with contexts matched any path containing `/context`, so `/contexts` was
cut into a context id of `s`. The two operations are now matched separately,
`/contexts` first.

## Evidence and current boundary

- `internal/api3/fields_test.go` covers all 11 operations, the canonical type
  key and the refusal of an unsupported one, the field set discovery reports,
  the search filters and their permission, the refused empty rename, the 404s
  for an unknown field, the delete refused before the trash, a trashed field
  leaving discovery and `createmeta`, a value surviving the round trip, and the
  finished task the delete reports.
- `migrations/143_custom_field_trash.sql` is exercised from a clean PostgreSQL
  schema.

Jira's `expand=lastUsed`, field translations (`translatedName`,
`translatedDescription`), `stableId`, the searcher key's effect on how a field
is searched, `PUT`/`DELETE /field/association`, the legacy
`/field/{fieldKey}/option` surface and `/config/fieldschemes` remain.
