# Field association schemes

Jira's newer field scheme API (`/config/fieldschemes`), which covers the same ground as [field configurations](FIELD_CONFIGURATIONS.md): which fields a project's forms show, which work types each field reaches, and whether it is required. Part of the [Jira platform](JIRA_PLATFORM.md). For status, see [CLOUD_PARITY.md](CLOUD_PARITY.md).

## Model

**A field association scheme is a field configuration scheme.** It is the same row, with the same id, projects and field rules:

- `parameters` are the field configuration item's `isRequired` and `description`.
- `restrictedToWorkTypes` is the scheme's work type mapping.
- The scheme's projects are its project assignments.

Because both APIs share one model, they always agree on whether a project's form shows a field.

## API

All operations need *Administer Jira*.

| Method and path | Behavior |
|---|---|
| `GET /rest/api/3/config/fieldschemes` | Pages schemes, filtered by `projectId` or a name/description `query`. Each result carries `matchedFilters` when a filter is applied. |
| `POST /rest/api/3/config/fieldschemes` | Creates a scheme with the site's default rules. |
| `GET/PUT/DELETE /rest/api/3/config/fieldschemes/{id}` | Reads, renames or deletes a scheme. |
| `POST /rest/api/3/config/fieldschemes/{id}/clone` | Copies a scheme's associations into a new scheme. |
| `GET /rest/api/3/config/fieldschemes/{id}/fields` | Pages associated fields with their rules and work type restrictions. |
| `PUT/DELETE /rest/api/3/config/fieldschemes/fields` | Associates or removes fields across schemes; results are per item. |
| `GET /rest/api/3/config/fieldschemes/{id}/fields/{fieldId}/parameters` | One field's rules and per-work-type overrides. |
| `PUT/DELETE /rest/api/3/config/fieldschemes/fields/parameters` | Sets or removes per-work-type overrides. |
| `GET /rest/api/3/config/fieldschemes/{id}/projects` | Pages a scheme's projects. |
| `GET/PUT /rest/api/3/config/fieldschemes/projects` | Reads or sets the scheme a project uses. |
| `PUT/DELETE /rest/api/3/field/association` | Associates or removes fields for every work type on the given projects. Only `PROJECT_ID` association contexts are accepted. |

An unknown scheme is 404, and so is a field the scheme does not associate. Results for an unknown field are reported per item.

## Behavior

- **Copy on write.** New schemes point every work type at the site's default configuration, which other schemes share. So when a write would change a configuration shared with another scheme (or the site default), the scheme first takes its own copy and remaps just that work type to it. A write for a work type without its own mapping also creates a copy, so the change does not leak into the fallback. A work type with its own copy stops following later edits to the fallback, as in Jira.
- **Restrictions.** `restrictedToWorkTypes` replaces earlier work type associations. Restricting a field to one work type hides it in the fallback and in every other work type. Reads report a restriction only when it is a real subset, and an override only when it differs from the fallback. Removing an override returns the work type to the fallback.
- **`/field/association`.** Changes apply through each project's scheme, so every project sharing that scheme sees the change. Associating a field also widens its [custom field context](CUSTOM_FIELD_CONTEXTS.md) to the project if needed. Removing an association only hides the field in the configuration; the context is left alone.
- `createmeta` honors restrictions: a field restricted to one work type appears only on that work type's create form.

## UI

There is no separate page; the same data is edited at `/settings/field-configurations`.

## Code

`internal/api3/field_schemes.go`; tests in `internal/api3/field_schemes_test.go`.

## Gaps

Tracked in [PLAN.md](../PLAN.md).

- The `rendererType` field parameter is not stored.
