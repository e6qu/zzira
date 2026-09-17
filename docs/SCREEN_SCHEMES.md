# Screen schemes and work type screen schemes

A form reaches its [screen](SCREENS.md) through a chain. The project uses a **work type screen scheme**, which maps each work type to a **screen scheme**, which maps each form operation (`create`, `edit`, `view`) to a screen. This chain decides which fields the create and edit forms show. Part of the [Jira platform](JIRA_PLATFORM.md). For status, see [CLOUD_PARITY.md](CLOUD_PARITY.md).

## API

All operations need *Administer Jira*.

| Method and path | Behavior |
|---|---|
| `GET/POST /rest/api/3/screenscheme` | Pages screen schemes with their operation mappings, or creates one. Filters: `id`, `queryString`. `orderBy`: name or id. `expand` lists the work type screen schemes that use each one. |
| `PUT/DELETE /rest/api/3/screenscheme/{screenSchemeId}` | Changes name, description or operation mappings, or deletes an unused, non-default scheme. |
| `GET/POST /rest/api/3/issuetypescreenscheme` | Pages work type screen schemes, or creates one with its mappings. Filters: `id`, `queryString`. `orderBy`: name or id. `expand` lists the projects that use each one. |
| `PUT/DELETE /rest/api/3/issuetypescreenscheme/{id}` | Changes name and description, or deletes an unassigned, non-default scheme. |
| `GET /rest/api/3/issuetypescreenscheme/mapping` | Pages work type → screen scheme mappings, filtered by scheme. |
| `PUT /rest/api/3/issuetypescreenscheme/{id}/mapping` | Adds or repoints work type mappings. |
| `PUT /rest/api/3/issuetypescreenscheme/{id}/mapping/default` | Repoints the fallback mapping. |
| `POST /rest/api/3/issuetypescreenscheme/{id}/mapping/remove` | Removes work type mappings. |
| `GET/PUT /rest/api/3/issuetypescreenscheme/project` | Pages project assignments, or assigns a project. |
| `GET /rest/api/3/issuetypescreenscheme/{id}/project` | Pages the projects using a scheme; filter `query`. |

Names are unique per site, ignoring case. Every change is written to the action log.

## Resolution

`ResolveScreenFields(project, work type, operation)` works through these steps:

1. Find the project's work type screen scheme.
2. Take that scheme's mapping for the work type, or its `default` mapping.
3. Take that screen scheme's screen for the operation, or its `default` screen.
4. Return the screen's fields, ordered by tab, then by position within the tab.

Rules:

- Every screen scheme has a `default` screen and every work type screen scheme has a `default` mapping, so resolution always finds a screen.
- Anything still referenced cannot be removed: screens used by a screen scheme, screen schemes used by a mapping, work type screen schemes assigned to a project, and the two site defaults. Trying returns a conflict.
- `project`, `issuetype` and `summary` always stay on a form, even if the screen leaves them out. Every other field is up to the screen.
- Consumers: `createmeta` and the create dialog use `create`; `editmeta` uses `edit`; bulk edit offers only fields on every selected work item's screen. With no governing screen, a form keeps its full field set.

## Defaults

Every site has a `Default Screen Scheme`, which points to the `Default Screen`. It also has a `Default Issue Type Screen Scheme`, whose `default` mapping points to that screen scheme. Every project uses the default work type screen scheme until it is reassigned; new projects get it from a trigger.

## UI

`/settings/screen-schemes`: create and edit both kinds of scheme, map operations and work types, assign projects, and delete.

## Code

`internal/api3/screen_schemes.go`, `internal/store/screen_schemes.go`, `internal/store/issue_type_screen_schemes.go`, `internal/web/screen_schemes.go`, `migrations/134_screen_schemes.sql`; tests in `internal/api3/screen_schemes_test.go` and `e2e/screen_schemes.spec.ts`.

## Gaps

Tracked in [PLAN.md](../PLAN.md).

- The `view` operation is stored and resolvable, but the work item view ignores it and uses its own layout.
- Workflow transition screens do not use screens; they store their own field list (see [SCREENS.md](SCREENS.md#gaps)).
- No copy action for screen schemes or work type screen schemes.
