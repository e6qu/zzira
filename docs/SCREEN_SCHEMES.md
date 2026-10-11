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
- `project`, `issuetype` and `summary` always stay on a form, even if the screen leaves them out. Subtask creation also keeps its required Parent; optional fields follow the screen.
- Consumers: `createmeta` and the create dialog use `create`; `editmeta` uses `edit`; bulk edit offers only fields on every selected work item's screen. With no governing screen, a form keeps its full field set. An assigned screen with no fields keeps only the required create context, including a subtask parent, and summary; it does not restore optional fields. Edit and view screens retain the empty assignment, and off-screen edits are refused. Edit metadata and bulk field choices resolve edit screens independently of create screens.

## Defaults

Every site has a `Default Screen Scheme`, which points to the `Default Screen`. It also has a `Default Issue Type Screen Scheme`, whose `default` mapping points to that screen scheme. Every project uses the default work type screen scheme until it is reassigned; new projects get it from a trigger.

## UI

`/settings/screen-schemes`: create and edit both kinds of scheme, map operations and work types, assign projects, and delete.

Changing project or work type pauses create-dialog input until its fields refresh.
A failed or timed-out refresh retains the draft and restores its previous
selection for retry; it cannot submit against stale field metadata.

## Code

`internal/api3/screen_schemes.go`, `internal/store/screen_schemes.go`, `internal/store/issue_type_screen_schemes.go`, `internal/web/screen_schemes.go`, `migrations/134_screen_schemes.sql`; tests in `internal/api3/screen_schemes_test.go` and `e2e/screen_schemes.spec.ts`.

**Copy.** The settings page copies one, named "Copy of X" (then "Copy 2 of X"), carrying its configuration and nothing else: the copy is assigned to no project and is never the site default.

Components, Fix versions and Affects versions have project-scoped multiple
pickers in the issue editor and inline details. Empty selections clear only
the submitted field; omitted fields stay unchanged. Fix-version editing also
requires Resolve issues. View-only fields retain their saved values without
controls, hidden fields disappear, and version links follow the view screen.
Both direct version values and add/remove/set operations follow edit-screen
checks. Offline configuration metadata remains tracked in [PLAN.md](../PLAN.md#14-local-first-closure).

The edit dialog also exposes configured Priority, Labels, Due date, Parent,
Resolution and estimate fields, including fields absent from the view screen.
Due dates require Schedule issues; Resolution requires Resolve issues. The
parent picker defaults to keeping its current relationship. Omitted fields
stay unchanged, invalid durations refuse the entire edit, and clearing one
estimate retains its unsubmitted counterpart. These additional controls wait
for server field metadata; legacy offline editors keep their existing fields.

The edit dialog renders native controls, project component/version pickers and
custom fields in the assigned edit screen’s tabs and field order. Tabs without
usable controls are omitted; one usable tab remains a plain form. Hidden fields
and permission checks still apply; summary remains available on an empty screen.
Custom multi-select controls save every selected option and explicitly clear when
all options are deselected, in both the dialog and inline editors. Omitted custom
fields remain unchanged; help text is associated with each editor’s own control.
Arrow keys, Home and End select tabs. Switching tabs retains the draft, and
required fields on inactive tabs are revealed when they block submission.
Legacy offline replicas retain their existing layout until screen metadata is
replicated.

When a required field blocks submission, the form opens its enclosing detail
section and selects its tab before native validation focuses it. Later invalid
fields in the same attempt do not hide the first field again.

Named workflow transition screens also use their configured tabs and field order.
Empty tabs and fields hidden by configuration or outside a custom field's context
are omitted. Inline field lists and one usable tab remain plain forms. Each
transition has its own control and help-text IDs. Assignee and Priority start at
the saved values; tab selection and drafts survive another issue action's refresh.
Multiple selections survive refreshes and save together; deselecting all explicitly
clears a transition field. Unchanged rich Description and Environment documents
retain their formatting. Custom values use the same typed encoder as the editor.
The transition journey is covered by `e2e/transition_screens.spec.ts`; full CI
validation remains tracked in the plan.

## Gaps

Tracked in [PLAN.md](../PLAN.md).

- View screens govern the built-in details the issue page renders and custom
  fields. Fields outside the edit screen show saved values without edit
  controls. The page uses its own layout rather than reproducing screen tabs
  and field order.
