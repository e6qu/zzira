# Jira field configurations

Updated: 2026-09-11

A screen decides which fields a work item form shows. A **field configuration**
decides how each of those fields behaves: required, hidden, or carrying an
administrator's help text. A project is assigned a **field configuration
scheme**, which maps each work type to a configuration.

Site administrators manage both at `/settings/field-configurations`. Screens and
their schemes are separate; see [SCREENS.md](SCREENS.md) and
[SCREEN_SCHEMES.md](SCREEN_SCHEMES.md).

## Jira Cloud REST surface

This checkpoint implements all 15 pinned operations:

| Method and path | Behavior |
|---|---|
| `GET/POST /rest/api/3/fieldconfiguration` | Pages field configurations, or creates one. |
| `PUT/DELETE /rest/api/3/fieldconfiguration/{id}` | Updates a configuration's name and description, or deletes an unused non-default configuration. |
| `GET/PUT /rest/api/3/fieldconfiguration/{id}/fields` | Pages the configuration's explicit field rules, or applies a partial update to them. |
| `GET/POST /rest/api/3/fieldconfigurationscheme` | Pages field configuration schemes, or creates one. |
| `PUT/DELETE /rest/api/3/fieldconfigurationscheme/{id}` | Updates a scheme's name and description, or deletes an unassigned non-default scheme. |
| `GET /rest/api/3/fieldconfigurationscheme/mapping` | Pages work type to configuration mappings, filtered by scheme. |
| `PUT /rest/api/3/fieldconfigurationscheme/{id}/mapping` | Adds or repoints work type mappings. |
| `POST /rest/api/3/fieldconfigurationscheme/{id}/mapping/delete` | Removes work type mappings. |
| `GET/PUT /rest/api/3/fieldconfigurationscheme/project` | Pages project assignments, or assigns a project to a scheme. |

Names are unique per workspace without regard to case, IDs come from dedicated
sequences, and every mutation writes an immutable action in the same
transaction.

## What a rule actually does

Only fields that differ from the baseline need a row, so **an absent rule means
optional and visible**. `ResolveFieldBehaviour` answers one question — how do
this project's fields behave for this work type — by following project →
scheme → work type mapping (falling back to `default`) → configuration.

The rules are enforced, not merely advertised:

- `IssueCreateMetadata` marks required fields required, drops hidden fields, and
  replaces the built-in help text with the administrator's, so the create dialog
  and `createmeta` agree.
- `editmeta` applies the same configuration for the work item's own type.
- **The command path rejects a write that breaks a rule**, so it holds for REST
  clients that never read the metadata. On create, a required field may not be
  omitted and a hidden field may not be supplied. On edit and on a transition
  carrying field updates, a required field may not be cleared and a hidden field
  may not be given a value; an update that leaves a governed field alone is never
  rejected, so the rules never block edits to unrelated fields.

A field cannot be both required and hidden, and **summary can be neither
optional nor hidden**: the command path requires it independently, so a
configuration that relaxed it would advertise something the server would still
refuse. Context fields — project and work type — are exempt for the same reason.

Jira's `FieldMetadata` carries no description, so an administrator's help text
reaches the browser form and the `/fieldconfiguration/{id}/fields` response
rather than `createmeta`.

## Defaults and continuity

Every workspace is provisioned with a `Default Field Configuration` whose only
rule is that summary is required — exactly what the command path already
enforced — and a `Default Field Configuration Scheme` whose `default` mapping
points at it. Every project is assigned that scheme, existing projects in the
migration and new ones by trigger. A newly created configuration starts from the
same baseline, and a new scheme points every work type at the default
configuration so it can be assigned before anything else is mapped.

## Evidence and current boundary

- `internal/api3/field_configurations_test.go` covers all 15 operations,
  permission rejection, the required-and-hidden and summary guards, unknown
  fields and work types, every in-use conflict, and the enforcement itself: a
  create missing a required field and a create supplying a hidden field are both
  rejected, while the same request with the field supplied succeeds, and
  removing the mapping falls back to the default configuration.
- `e2e/field_configurations.spec.ts` covers the browser journey: make priority
  required with help text, hide labels, map a work type, assign a project, watch
  the create dialog mark priority required and drop labels, confirm REST rejects
  the same omission, create the work item, reflow at 320 px, hand the project
  back, and delete in dependency order.
- `migrations/135_field_configurations.sql` is exercised from a clean PostgreSQL
  schema. The page is in the light and dark axe sweep.

Jira's `renderer` on a field configuration item is not stored; ZZIRA renders each
field type one way. Workflow rules that set fields during a transition are
automation rather than a person's edit, so they bypass the configuration, as
post-functions do in Jira. Bulk edit still offers the project's full field
superset, as it does for screens, so a bulk write is not yet judged by these
rules. The `/config/fieldschemes` field association surface, `expand` and
`orderBy` on these endpoints, and exact Jira error wording also remain.

One pre-existing limitation is worth knowing while these rules are enforced:
`PUT /rest/api/3/issue/{key}` reads a priority only by `id`, so a client that
sends `{"priority":{"name":"Medium"}}` supplies an empty id. That silently
cleared the priority before this checkpoint; where a configuration marks
priority required it is now reported as clearing a required field instead.
