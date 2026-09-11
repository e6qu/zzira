# Jira screens

Updated: 2026-09-10

A screen is the reusable field layout behind a Jira work item form. ZZIRA stores
screens, their tabs, and the ordered fields on each tab. Site administrators
manage the catalog at `/settings/screens`.

Every workspace is provisioned with a `Default Screen` carrying one `Field Tab`
of summary, description, assignee, priority, and labels, and the same screen is
created for each new workspace by a trigger.

## Jira Cloud REST surface

This checkpoint implements all 17 pinned Jira Cloud screen, tab, and tab-field
operations:

| Method and path | Behavior |
|---|---|
| `GET/POST /rest/api/3/screens` | Pages screens with `id` and `queryString` filters, or creates a screen with its first tab. |
| `PUT/DELETE /rest/api/3/screens/{screenId}` | Updates a screen's name and description, or deletes a screen with its tabs and fields. |
| `GET /rest/api/3/screens/{screenId}/availableFields` | Lists catalog fields the screen does not already show. |
| `POST /rest/api/3/screens/addToDefault/{fieldId}` | Adds one field to the default screen's first tab. |
| `GET /rest/api/3/field/{fieldId}/screens` | Pages the screens that currently show one field. |
| `GET /rest/api/3/screens/tabs` | Reads tabs for selected screens, or for every screen when no `screenId` is given. |
| `GET/POST /rest/api/3/screens/{screenId}/tabs` | Reads a screen's tabs in display order, or appends a tab. |
| `PUT/DELETE /rest/api/3/screens/{screenId}/tabs/{tabId}` | Renames a tab, or removes it with its fields. |
| `POST /rest/api/3/screens/{screenId}/tabs/{tabId}/move/{pos}` | Moves a tab to an explicit zero-based position. |
| `GET/POST /rest/api/3/screens/{screenId}/tabs/{tabId}/fields` | Reads a tab's fields in display order, or adds one catalog field. |
| `DELETE /rest/api/3/screens/{screenId}/tabs/{tabId}/fields/{id}` | Removes one field from a tab. |
| `POST /rest/api/3/screens/{screenId}/tabs/{tabId}/fields/{id}/move` | Moves a field with `after`, or `position` First, Earlier, Later, or Last. |

Screen names are unique per workspace and tab names are unique per screen, both
without regard to case. IDs are Jira-style numeric values drawn from dedicated
sequences. Every mutation writes an immutable action in the same transaction.

Two structural rules are enforced by the store rather than left to the caller:
a screen always keeps at least one tab, so fields always have somewhere to
live, and a field appears at most once per screen, so moving it between tabs is
an update rather than a duplicate. The workspace default screen cannot be
deleted. Removing a tab or a field renumbers what remains, so positions stay
dense and reads are stable.

## The field catalog

A screen may only reference a field the issue forms can already render: the
built-in summary, description, assignee, priority, labels, parent, components,
fix versions, affects versions, restrict-to, issue type, and project fields,
followed by the workspace's custom fields. Adding an unknown field is rejected
rather than stored, and deleting a custom field removes it from every screen
through the same trigger pattern that maintains role bindings, permission
grants, and issue security holders.

## Evidence and current boundary

- `internal/api3/screens_test.go` covers all 17 operations, permission
  rejection, name and tab uniqueness, the unknown-field rejection, every move
  form, the last-tab and default-screen guards, and the immutable action record.
- `e2e/screens.spec.ts` covers screen creation, adding and reordering fields,
  adding and reordering tabs, REST agreement with what the browser shows,
  320 px reflow, field and tab removal, the last-tab guard, and deletion. It
  creates and deletes its own screen.
- `migrations/133_screens.sql` is exercised from a clean PostgreSQL schema and
  provisions the default screen for existing and future workspaces. The page is
  in the light and dark axe sweep.

Screens now drive the create and edit forms through screen schemes and work
type screen schemes; see [SCREEN_SCHEMES.md](SCREEN_SCHEMES.md) for the
resolution chain and its boundary. A screen a screen scheme uses cannot be
deleted, and the default screen carries every system field the forms render.
Workflow transition screens still carry their own field list rather than
referencing a screen. Field configurations, per-project field scoping, the
`expand` and `projectKey` query parameters, and exact Jira error wording also
remain.
