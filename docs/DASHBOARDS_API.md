# Jira dashboards

Updated: 2026-09-11

ZZIRA stores dashboards, their share and edit permissions, favourites, layout
and refresh, the gadgets on them and each gadget's properties. People use them
at `/dashboard`.

## Jira Cloud REST surface

All 17 pinned dashboard operations are implemented. An audit against a running
server found sixteen already working; only Jira's bulk edit was missing.

| Method and path | Behavior |
|---|---|
| `GET/POST /rest/api/3/dashboard` | Pages the dashboards a caller may see, or creates one. |
| `GET /rest/api/3/dashboard/search` | Searches dashboards with Jira's ordering and filters. |
| `GET /rest/api/3/dashboard/gadgets` | Lists the gadget modules a dashboard may use. |
| `GET/PUT/DELETE /rest/api/3/dashboard/{id}` | Reads, updates, or removes one dashboard. |
| `POST /rest/api/3/dashboard/{id}/copy` | Copies a dashboard with its gadgets. |
| `PUT /rest/api/3/dashboard/bulk/edit` | Applies one change to several dashboards. |
| `GET/POST /rest/api/3/dashboard/{dashboardId}/gadget` | Lists a dashboard's gadgets, or adds one. |
| `PUT/DELETE /rest/api/3/dashboard/{dashboardId}/gadget/{gadgetId}` | Updates or removes one gadget. |
| `GET /rest/api/3/dashboard/{dashboardId}/items/{itemId}/properties` | Lists a gadget's property keys. |
| `GET/PUT/DELETE /…/items/{itemId}/properties/{propertyKey}` | Reads, stores, or removes one gadget property. |

## Bulk edit

`PUT /dashboard/bulk/edit` applies `changePermission`, `changeOwner`, or
`delete` to up to 1,000 dashboards. **It answers 200 with a per-dashboard error
map rather than failing the whole request**, so a caller learns exactly which
dashboards it could not change and why; a request naming one unknown dashboard
still applies the change to the rest.

The request itself is rejected with 400 when the action is unknown, the id list
is empty or too long, or the details the action needs are absent — those are
faults in the request rather than in one dashboard.

Only the owner may hand a dashboard on, and only to a member of the workspace.
Ownership transfer is otherwise a silent way to take a dashboard away from
someone who can edit it.

## Evidence and current boundary

- `internal/api3/dashboards_test.go` covers the dashboard lifecycle, privacy and
  gadget operations it already exercised, and now bulk edit: the request-level
  rejections, the per-dashboard error map, permissions actually applied,
  ownership transfer refused for a non-owner and accepted for the owner, and
  bulk deletion.
- `e2e/dashboards.spec.ts` covers the browser journey from creating a dashboard
  through layouts, favourites, sharing, refresh and gadgets.

Jira's `extendAdminPermissions`, dashboard item property expansion, and exact
Jira error wording remain.
