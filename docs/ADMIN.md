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
| `atlassian/user` and product roles | Atlassian-compatible direct or group access to the bound product |
| `atlassian/user-access-admin` | Administers access for the bound product without receiving product use |

Direct and group role bindings use the same evaluator. Disabled users cannot
enter a site even if a role remains. Existing authorization calls now resolve
workspace membership and administration through this model.

## Browser journey

Site administrators use `/admin` to inspect organization and Cloud IDs, enabled
products, the internal directory, users, groups, and recent audit events. They
can invite, suspend, restore, or remove managed accounts; assign product access
and groups during invitation; create or delete a group; add or remove directory
users; and grant or revoke each group's Jira Software, Jira Service Management,
and Confluence access. Suspension and removal revoke the
account's active sessions and API tokens. Every successful user, group,
membership, or role mutation writes an organization audit event in the same
transaction. Administrators cannot suspend or remove their own account.
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
| GET | `/admin/v2/orgs/{orgId}/directories/{directoryId}/groups/count` |
| POST | `/admin/v2/orgs/{orgId}/directories/{directoryId}/groups/search` |
| GET | `/admin/v2/orgs/{orgId}/directories/{directoryId}/groups/stats` |
| GET/DELETE | `/admin/v2/orgs/{orgId}/directories/{directoryId}/groups/{groupId}` |
| POST | `/admin/v2/orgs/{orgId}/directories/{directoryId}/groups/{groupId}/memberships` |
| DELETE | `/admin/v2/orgs/{orgId}/directories/{directoryId}/groups/{groupId}/memberships/{accountId}` |
| POST | `/admin/v2/orgs/{orgId}/workspaces` |
| POST | `/admin/v1/orgs/{orgId}/users/{userId}/role-assignments/{assign\|revoke}` |
| POST | `/admin/v1/orgs/{orgId}/users/{userId}/roles/{assign\|revoke}` |
| GET | `/admin/v2/orgs/{orgId}/directories/{directoryId}/groups/{groupId}/role-assignments` |
| POST | `/admin/v2/orgs/{orgId}/directories/{directoryId}/groups/{groupId}/role-assignments/{assign\|revoke}` |
| GET | `/admin/v2/orgs/{orgId}/directories/{directoryId}/users/{accountId}/role-assignments` |
| GET | `/admin/v1/orgs/{orgId}/users` |
| GET | `/admin/v2/orgs/{orgId}/directories/{directoryId}/users` |
| GET | `/admin/v2/orgs/{orgId}/directories/{directoryId}/users/count` |
| POST | `/admin/v2/orgs/{orgId}/directories/{directoryId}/users/search` |
| GET | `/admin/v2/orgs/{orgId}/directories/{directoryId}/users/stats` |
| GET | `/admin/v2/orgs/{orgId}/directories/{directoryId}/users/{userId}` |
| DELETE | `/admin/v2/orgs/{orgId}/directories/{directoryId}/users/{accountId}` |
| POST | `/admin/v2/orgs/{orgId}/directories/{directoryId}/users/{accountId}/{suspend\|restore}` |
| POST | `/admin/v2/orgs/{orgId}/users/invite` |

The API requires `Authorization: Bearer <api-token>` and an organization or site
administrator role. Site Jira APIs continue to accept their existing Jira-style
Basic authentication. Collection cursors are opaque encoded offsets; malformed
cursors are rejected. Group and user searches implement the documented opaque
pagination, sorting, exact-list, text, directory, membership, lifecycle,
resource, role, domain, and expansion filters. `-` scopes search and statistics
to every directory the caller can administer. Group count accepts its full
documented filter set. Workspace discovery returns product ARIs. Role lookups support directory,
resource-owner, resource-ID, and role-ID filters and report whether effective
user access is direct or inherited from a group.

Invitation access, group membership, optional email enqueueing, and audit
evidence commit atomically for each account. A multi-account request returns
`206 Partial Content` with per-assignment `ERROR` results if an account cannot
be invited while preserving successful invitations. Email requests return 503
when SMTP is not configured. Configured delivery uses a durable PostgreSQL
outbox with leases, bounded exponential retries, and a terminal state after
eight failed attempts.

| Environment variable | Purpose |
|---|---|
| `ZZIRA_SMTP_ADDR` | SMTP endpoint in `host:port` form |
| `ZZIRA_SMTP_FROM` | Envelope and message sender |
| `ZZIRA_SMTP_USERNAME` | Optional SMTP username; configure with the password |
| `ZZIRA_SMTP_PASSWORD` | Optional SMTP password; configure with the username |

The current server configuration serves one workspace/site. Organization
discovery therefore returns the organization containing that site. Cross-site
organization discovery, directory filters, SCIM lifecycle, user suspension,
policy/domain/event endpoints, richer stored account profiles, per-directory
account suspension for multi-site deployments, license limits, and full
central-host rate limiting remain in
the active plan and are reported as unassessed or missing in operation coverage.

## Verification

- Store integration tests cover workspace provisioning, membership migration,
  direct roles, group-derived administration and product access, revocation,
  and audit events.
- API integration tests cover bearer authentication, permission denial,
  organization/directory/product discovery, group creation/detail/search/count/
  statistics/deletion, directory-user search/statistics, atomic invitation
  assignments and delivery enqueueing, membership and role mutations, effective
  assignments, partial results, conflicts, expansions, and audit persistence.
- Playwright covers the complete group and product-access journey,
  invitation-time product/group access, account lifecycle, ordinary-user denial, WCAG scans, light/dark
  themes, and 320px reflow.
