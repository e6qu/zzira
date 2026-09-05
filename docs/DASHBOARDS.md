# Custom dashboards

The dashboard directory at `/dashboards` provides private and shared dashboards.
Owners can change dashboard details and sharing, while people named in edit
permissions can arrange gadgets and configure their queries. Every viewer's
gadget results are evaluated with that viewer's issue-security permissions.

## Delivered Jira Cloud operations

ZZIRA implements 16 of the 17 dashboard operations in the pinned Jira Cloud
platform REST v3 contract:

| Operation | Delivered behavior |
|---|---|
| GET/POST `/rest/api/3/dashboard` | Paginated visible dashboards with `my` and `favourite` filters; create a private or shared dashboard |
| GET `/rest/api/3/dashboard/search` | Name and owner filtering, active status, pagination, supported expansions and Jira orderings |
| GET/PUT/DELETE `/rest/api/3/dashboard/{id}` | Permission-scoped details, owner-only metadata/sharing updates and deletion |
| POST `/rest/api/3/dashboard/{id}/copy` | Copy every gadget and property into a private dashboard owned by the caller |
| GET `/rest/api/3/dashboard/gadgets` | Native ZZIRA gadget catalog |
| GET/POST `/rest/api/3/dashboard/{id}/gadget` | Filtered gadget listing and native gadget creation |
| PUT/DELETE `/rest/api/3/dashboard/{id}/gadget/{gadgetId}` | Title, color and position updates with stable row compaction; gadget removal |
| GET `/rest/api/3/dashboard/{id}/items/{itemId}/properties` | Sorted property key listing |
| GET/PUT/DELETE `/rest/api/3/dashboard/{id}/items/{itemId}/properties/{propertyKey}` | JSON property lifecycle with Jira key and value bounds |

The unimplemented operation is `PUT /rest/api/3/dashboard/bulk/edit`. Group and
project share permissions, archived/deleted search, anonymous dashboards, admin
permission extension and dashboard layout/favourite REST extensions also remain
compatibility gaps.

## Native gadgets and presentation

The native catalog contains:

- `com.zzira:filter-results`
- `com.zzira:issue-statistics`
- `com.zzira:pie-chart`
- `com.zzira:assigned-to-me`

Gadgets accept direct JQL or a saved filter through the reserved
`zzira.config` item property. Lists return up to 50 results. Statistics and pie
charts calculate their full permission-filtered total and group by status,
priority, work type or assignee. Pie charts include an equivalent data table.
Assigned-to-me adds `assignee = currentUser()` when each viewer loads it.

The browser supports Jira-style one, two and three-column layouts (`A`, `AA`,
`AB`, `BA`, `AAA`), gadget reordering, eight accent colors, favourites, manual
refresh, and automatic refresh at one, five or fifteen minutes. The content
refresh endpoint is never placed in the service-worker page cache. Revoking a
share clears a viewer's rendered gadgets on their next refresh.

## Compatibility boundary

ZZIRA validates module keys against its native catalog. It does not download or
execute arbitrary gadget URLs, Atlassian Connect gadgets, Forge modules or Jira
system gadget module keys. Clients sending a URI, an unknown module key, or
`ignoreUriAndModuleKeyValidation=true` receive an explicit validation error.

Dashboard writes add ID-only invalidation records to the workspace action log.
They never serialize dashboard configuration or gadget results. Dashboard
materialization in the offline SQLite replica is not delivered yet, so custom
dashboard pages intentionally require an online session.
