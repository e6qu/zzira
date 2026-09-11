# Jira screen schemes and work type screen schemes

Updated: 2026-09-11

A screen on its own is only a field layout. Jira reaches a work item form
through a chain: a project is assigned a **work type screen scheme**, which maps
each work type to a **screen scheme**, which maps each form operation to a
**screen**. This checkpoint implements that chain and makes it authoritative for
the create and edit forms.

Site administrators manage both scheme families at `/settings/screen-schemes`.
Screens themselves are edited at `/settings/screens`; see
[SCREENS.md](SCREENS.md).

## Jira Cloud REST surface

This checkpoint implements all 15 pinned operations:

| Method and path | Behavior |
|---|---|
| `GET/POST /rest/api/3/screenscheme` | Pages screen schemes with their operation mappings, or creates one. |
| `PUT/DELETE /rest/api/3/screenscheme/{screenSchemeId}` | Updates a scheme's name, description, or operation mappings, or deletes an unused non-default scheme. |
| `GET/POST /rest/api/3/issuetypescreenscheme` | Pages work type screen schemes, or creates one with its work type mappings. |
| `PUT/DELETE /rest/api/3/issuetypescreenscheme/{issueTypeScreenSchemeId}` | Updates a scheme's name and description, or deletes an unassigned non-default scheme. |
| `GET /rest/api/3/issuetypescreenscheme/mapping` | Pages work type to screen scheme mappings, filtered by scheme. |
| `PUT /rest/api/3/issuetypescreenscheme/{id}/mapping` | Adds or repoints work type mappings. |
| `PUT /rest/api/3/issuetypescreenscheme/{id}/mapping/default` | Repoints the scheme's fallback mapping. |
| `POST /rest/api/3/issuetypescreenscheme/{id}/mapping/remove` | Removes work type mappings. |
| `GET/PUT /rest/api/3/issuetypescreenscheme/project` | Pages project assignments, or assigns a project to a scheme. |
| `GET /rest/api/3/issuetypescreenscheme/{id}/project` | Pages the projects using one scheme. |

Names are unique per workspace without regard to case, and IDs are Jira-style
numeric values from dedicated sequences. Every mutation writes an immutable
action in the same transaction.

## Resolution, and what a screen may not hide

`ResolveScreenFields` answers one question — which fields does this project's
form show for this work type and operation — and every form asks it:

1. the project's work type screen scheme,
2. that scheme's mapping for the work type, falling back to its `default`
   mapping,
3. that screen scheme's mapping for the operation, falling back to its
   `default` screen,
4. the screen's fields, ordered by tab position and then field position.

Two structural rules make the chain safe to edit. A screen scheme always has a
`default` screen, and a work type screen scheme always has a `default` mapping,
so resolution can never dead-end. Rows that are still referenced cannot be
removed: a screen used by a screen scheme, a screen scheme used by a work type
mapping, and a work type screen scheme assigned to a project each report a
conflict, as do the two workspace defaults.

**Context fields and summary always survive a screen.** The command path cannot
create work without a project, a work type, and a summary, so a screen that
omits them narrows the rest of the form rather than producing an uncreatable
work item. Everything else — description, assignee, priority, labels, parent,
components, versions, the security level, and custom fields — is the screen's
to decide.

Where no screen governs a form, callers keep their full field set, so a
workspace without screens behaves exactly as it did before this checkpoint.

## Defaults and continuity

Every workspace is provisioned with a `Default Screen Scheme` pointing at the
`Default Screen`, and a `Default Issue Type Screen Scheme` whose `default`
mapping points at it; every project is assigned the latter, existing projects in
the migration and new ones by trigger.

Because the default screen is now authoritative, the migration expands it to
carry every system field the forms rendered before — summary, description,
assignee, priority, labels, parent, components, fix versions, affects versions,
and the security level — and replaces the trigger that provisioned five fields
for a new workspace. A newly created custom field is added to the default
screen by trigger, so it stays usable on the forms the default scheme drives.

## Evidence and current boundary

- `internal/api3/screen_schemes_test.go` covers all 15 operations, permission
  rejection, the required default screen and default mapping, unknown work
  types and operations, every in-use conflict, and — the point of the
  checkpoint — that assigning a lean scheme removes `priority` from createmeta
  and editmeta while context fields survive, then that restoring the workspace
  default brings the full form back.
- `e2e/screen_schemes.spec.ts` covers the browser journey: build a screen, wrap
  it in both schemes, assign a project, watch the create dialog lose priority
  and keep labels, create a work item on the lean form, confirm editmeta agrees,
  reflow at 320 px, hand the project back, and delete in dependency order.
- `migrations/134_screen_schemes.sql` is exercised from a clean PostgreSQL
  schema. Both pages are in the light and dark axe sweep.

The `view` operation is stored and resolvable but no read-only view form
consumes it yet; the issue view renders its own layout. Bulk edit now offers the
intersection of the screens every selected work item resolves, rather than the
project's field superset. Workflow transition screens still carry their own field
list rather than referencing a screen. Jira's `expand`, `orderBy`, and
`queryString` parameters on these endpoints, screen scheme copy, and exact Jira
error wording also remain.
