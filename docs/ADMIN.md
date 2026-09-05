# Organization and site administration

ZZIRA provisions an organization, site, Jira Software product, Jira Service
Management product, Confluence product, and internal user directory for every
workspace. Existing `admin` and `member` memberships migrate into explicit site
and product role bindings. A database trigger keeps legacy membership writers
consistent while the remaining application commands move to the shared role
model.

## Authorization model

Role bindings have an organization, site, product, project, or space scope and
target either a user or a group. The first supported roles are:

| Role | Current effect |
|---|---|
| `atlassian/org-admin` | Administers every site in its organization |
| `atlassian/site-admin` | Administers the bound site and its directory/product access |
| `atlassian/site-user` | Enters the site |
| `atlassian/product-admin` | Enters and administers the bound product where a product permission uses it |
| `atlassian/product-user` | Enters the bound enabled product |

Direct and group role bindings use the same evaluator. Disabled users cannot
enter a site even if a role remains. Existing authorization calls now resolve
workspace membership and administration through this model.

## Browser journey

Site administrators use `/admin` to inspect organization and Cloud IDs, enabled
products, the internal directory, users, groups, and recent audit events. They
can create a group and add or remove directory users. Every successful group or
membership mutation writes an organization audit event in the same transaction.
Ordinary users do not see the administration navigation item and receive 403 on
direct access.

## Organization API subset

The Organizations REST API is mounted locally at `/admin`. These operations are
implemented:

| Method | Path |
|---|---|
| GET | `/admin/v1/orgs` |
| GET | `/admin/v1/orgs/{orgId}` |
| GET | `/admin/v2/orgs/{orgId}/directories` |
| GET/POST | `/admin/v2/orgs/{orgId}/directories/{directoryId}/groups` |
| POST | `/admin/v2/orgs/{orgId}/directories/{directoryId}/groups/{groupId}/memberships` |
| DELETE | `/admin/v2/orgs/{orgId}/directories/{directoryId}/groups/{groupId}/memberships/{accountId}` |

The API requires `Authorization: Bearer <api-token>` and an organization or site
administrator role. Site Jira APIs continue to accept their existing Jira-style
Basic authentication. Collection cursors are opaque encoded offsets; malformed
cursors are rejected. Groups support `searchTerm` and limits from 1 through 100.

The current server configuration serves one workspace/site. Organization
discovery therefore returns the organization containing that site. Cross-site
organization discovery, directory filters, SCIM lifecycle, user suspension,
product access mutation, policy/domain/event endpoints, role-assignment APIs,
group detail/delete/statistics, and full central-host rate limiting remain in
the active plan and are reported as unassessed or missing in operation coverage.

## Verification

- Store integration tests cover workspace provisioning, membership migration,
  direct roles, group-derived administration, group removal, and audit events.
- API integration tests cover bearer authentication, permission denial,
  organization/directory discovery, group creation, membership add/remove,
  conflicts, and audit persistence.
- Playwright covers the complete admin group journey, ordinary-user denial,
  WCAG scans, light/dark themes, and 320px reflow.
