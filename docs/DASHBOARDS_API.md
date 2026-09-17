# Dashboards REST API

This page covers the Jira Cloud platform REST v3 dashboard operations: dashboards, their share and edit permissions, gadgets, and gadget item properties. The browser pages, the gadget catalog, wallboards and dashboard emails are in [DASHBOARDS.md](DASHBOARDS.md). Part of [Jira Software](JIRA_SOFTWARE.md); see [CLOUD_PARITY.md](CLOUD_PARITY.md) for status.

## Operations

| Method and path | Behavior |
|---|---|
| `GET /rest/api/3/dashboard` | Pages the dashboards the caller can see. `filter` is `my` or `favourite`. |
| `POST /rest/api/3/dashboard` | Creates a private or shared dashboard. |
| `GET /rest/api/3/dashboard/search` | Searches by `dashboardName`, `accountId`/`owner`, `groupname`, `groupId` and `projectId`, with Jira's `orderBy` and paging. Accepts every documented expansion. `status` other than `active` is 400, because dashboards are never archived here. |
| `GET/PUT/DELETE /rest/api/3/dashboard/{id}` | Reads a dashboard. Only the owner can update its details and sharing, or delete it. |
| `POST /rest/api/3/dashboard/{id}/copy` | Copies every gadget and property into a new private dashboard owned by the caller. |
| `PUT /rest/api/3/dashboard/bulk/edit` | Applies `changePermission`, `changeOwner` or `delete` to up to 1,000 dashboards. |
| `GET /rest/api/3/dashboard/gadgets` | Lists the built-in gadget catalog. Filters: `moduleKey`, `uri`, `gadgetId`. |
| `GET/POST /rest/api/3/dashboard/{dashboardId}/gadget` | Lists or adds gadgets. Filters: `moduleKey`, `uri`, `gadgetId`. |
| `PUT/DELETE /rest/api/3/dashboard/{dashboardId}/gadget/{gadgetId}` | Updates a gadget's title, color or position, or removes it. After a move, the rows are renumbered without gaps and gadgets keep their relative order. |
| `GET /rest/api/3/dashboard/{dashboardId}/items/{itemId}/properties` | Lists a gadget's property keys in sorted order. |
| `GET/PUT/DELETE …/items/{itemId}/properties/{propertyKey}` | Reads, stores or removes a JSON property. Key length and value size are limited as in Jira. PUT returns 201 when it creates a property and 200 when it replaces one. |

## Behavior

**Sharing**
- A dashboard can be shared with users, groups, a project's browsers, a project role's members, or everyone signed in.
- Jira Cloud no longer shares dashboards publicly, so there are no anonymous dashboards.
- Gadget results are always computed with the viewer's own permissions.

**Bulk edit**
- The response is 200 with an error map keyed by dashboard, so one failing dashboard does not stop the others.
- The whole request is 400 when:
  - the action is unknown;
  - `entityIds` is empty or has more than 1,000 entries;
  - `changeOwnerDetails.newOwner` or `permissionDetails` is missing.
- Only the owner can transfer a dashboard, and only to a workspace member.

**Admin override**
- With `extendAdminPermissions=true`, a site administrator (Administer Jira) can update, copy or delete a dashboard they neither own nor have been shared.
- Anyone else gets 403. Any value other than `true` or `false` is 400.

**Gadget validation**
- A gadget created through REST must use a module key from the built-in catalog.
- Sending a `uri`, an unknown module key, or `ignoreUriAndModuleKeyValidation=true` returns a validation error.

**Sync**
- Dashboard writes add action log entries that carry only the dashboard id.
- Dashboard configuration and gadget results never enter the action log.

## Gaps

- The REST API cannot read or write the site's layout and favourite extensions.
- Gadget item properties cannot be returned inline with the gadget (no expansion).

Remaining work is tracked in [PLAN.md](../PLAN.md).

## See also

- Code: `internal/api3/dashboards.go`, `internal/store/dashboards.go`, `internal/store/dashboard_gadgets.go`.
- Tests: `internal/api3/dashboards_test.go`, `internal/api3/expansions_filters_test.go`, `e2e/dashboards.spec.ts`.
