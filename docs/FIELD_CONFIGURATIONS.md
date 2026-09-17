# Field configurations

A field configuration decides how fields behave on work item forms: required, hidden, or showing an administrator's help text. A project uses a field configuration scheme, which maps each work type to a configuration. [Screens](SCREENS.md) decide which fields a form shows. [Field association schemes](FIELD_ASSOCIATION_SCHEMES.md) are Jira's newer API over the same model. Part of the [Jira platform](JIRA_PLATFORM.md). For status, see [CLOUD_PARITY.md](CLOUD_PARITY.md).

## API

| Method and path | Behavior |
|---|---|
| `GET/POST /rest/api/3/fieldconfiguration` | Pages configurations (filters `id`, `isDefault`, `query` over name and description), or creates one. |
| `PUT/DELETE /rest/api/3/fieldconfiguration/{id}` | Changes name and description, or deletes an unused, non-default configuration. |
| `GET/PUT /rest/api/3/fieldconfiguration/{id}/fields` | Pages the configuration's explicit field rules, or partially updates them. |
| `GET/POST /rest/api/3/fieldconfigurationscheme` | Pages schemes (filter `id`), or creates one. |
| `PUT/DELETE /rest/api/3/fieldconfigurationscheme/{id}` | Changes name and description, or deletes an unassigned, non-default scheme. |
| `GET /rest/api/3/fieldconfigurationscheme/mapping` | Pages work type → configuration mappings, filtered by scheme. |
| `PUT /rest/api/3/fieldconfigurationscheme/{id}/mapping` | Adds or repoints work type mappings. |
| `POST /rest/api/3/fieldconfigurationscheme/{id}/mapping/delete` | Removes work type mappings. |
| `GET/PUT /rest/api/3/fieldconfigurationscheme/project` | Pages project assignments, or assigns a project. |

Names are unique per site, ignoring case. Every change is written to the action log. Anything still in use returns a conflict.

## Resolution

`ResolveFieldBehaviour` follows project → scheme → the work type's mapping (or the `default` mapping) → configuration. A configuration stores rows only for fields that differ from the baseline; **a field with no row is optional and visible**.

## Enforcement

- `createmeta` and the create dialog mark required fields, drop hidden ones, and show the administrator's help text in place of the built-in text.
- `editmeta` applies the configuration for the work item's own work type.
- Bulk edit offers only fields that every selected work item's form allows.
- Commands enforce the rules for all clients. On create, a required field must be present and a hidden field must be absent. On edit and on a transition with field updates, a required field cannot be cleared and a hidden field cannot be set. An update that doesn't touch a governed field is never refused.
- A field cannot be both required and hidden. `summary` is always required and visible, and the context fields (project, work type) are exempt.
- Workflow post functions that set fields bypass the configuration, as in Jira.
- `overrideScreenSecurity` lets an app with *Administer Jira* set hidden fields ([ISSUE_SURFACE.md](ISSUE_SURFACE.md#writing-work-items)).
- Help text appears in the browser form and in `/fieldconfiguration/{id}/fields`, but not in `createmeta`, because Jira's `FieldMetadata` has no description.

## Defaults

- Every site has a `Default Field Configuration`, whose only rule is that `summary` is required.
- Every site has a `Default Field Configuration Scheme`, whose `default` mapping points to that configuration.
- Every project uses that scheme until it is reassigned. New projects get it from a trigger.
- A new configuration starts from the same baseline. A new scheme maps every work type to the default configuration.

## UI

`/settings/field-configurations`: create configurations and schemes, set required, hidden and help text per field, map work types, assign projects, and delete.

## Code

`internal/api3/field_configurations.go`, `internal/store/field_configurations.go`, `internal/store/field_configuration_schemes.go`, `migrations/135_field_configurations.sql`; tests in `internal/api3/field_configurations_test.go` and `e2e/field_configurations.spec.ts`.

## Gaps

Tracked in [PLAN.md](../PLAN.md).

- A field configuration item's `renderer` (wiki or plain text) is not stored; each field type renders one way.
