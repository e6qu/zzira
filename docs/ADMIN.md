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
can invite, edit the managed profile of, suspend, restore, or remove managed
accounts; assign product access and groups during invitation; create or delete
a group; add or remove directory users; and grant or revoke each group's Jira
Software, Jira Service Management, and Confluence access. Suspension and
removal revoke active sessions and API tokens when the account has no other
active directory. On reconnect, an affected browser verifies access before
outbox replay, purges its private replica and authenticated page cache, and
returns to the signed-out page. Every successful user, group,
membership, or role mutation writes an organization audit event in the same
transaction. Administrators cannot suspend or remove their own account.
Ordinary users do not see the administration navigation item and receive 403 on
direct access.

With `ZZIRA_IDENTITY_ENCRYPTION_KEY` configured, the same page registers custom
OpenID Connect providers through validated discovery, rotates their client
secrets, enables or disables sign-in, and deletes registrations. Secrets use an
authenticated AES-256-GCM envelope bound to the workspace and provider key and
are never returned to the browser. Environment-configured providers remain
deployment-managed. Deleting a stored provider and disabling any provider
revoke its issuer sessions and write organization audit evidence.

## Organization API subset

The Organizations REST API is mounted locally at `/admin`. These operations are
implemented:

| Method | Path |
|---|---|
| GET | `/admin/v1/orgs` |
| GET | `/admin/v1/orgs/{orgId}` |
| GET | `/admin/v1/orgs/{orgId}/events` |
| GET | `/admin/v1/orgs/{orgId}/events-stream` |
| GET | `/admin/v1/orgs/{orgId}/events/{eventId}` |
| GET | `/admin/v1/orgs/{orgId}/event-actions` |
| GET | `/admin/v1/orgs/{orgId}/domains` |
| GET | `/admin/v1/orgs/{orgId}/domains/{domainId}` |
| GET/POST | `/admin/v1/orgs/{orgId}/policies` |
| GET/PUT/DELETE | `/admin/v1/orgs/{orgId}/policies/{policyId}` |
| POST | `/admin/v1/orgs/{orgId}/policies/{policyId}/resources` |
| PUT/DELETE | `/admin/v1/orgs/{orgId}/policies/{policyId}/resources/{resourceId}` |
| GET | `/admin/v1/orgs/{orgId}/policies/{policyId}/validate` |
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
| GET | `/admin/v1/orgs/{orgId}/directory/users/{accountId}/last-active-dates` |
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

Product activity is recorded only after an authenticated product page remains
visible for two seconds. The last-active endpoint returns the accessed product
instance IDs, Jira-compatible product keys, UTC dates and timestamps, the first
organization membership time, and opaque paging. A user who has never viewed a
product has an empty `product_access` array.

Organization events expose the same immutable evidence as the browser audit
log. Query supports text, action, actor, IP, product, location, millisecond time
bounds, limits up to 500, and opaque paging. The polling endpoint defaults to
ascending processing order and returns a reusable cursor even at the current
end of the stream. Event detail and the localized action catalog use the
published resource shapes. The administration page searches event text and
filters by action through this shared query path.

Administrators can add email-domain claims, copy the generated DNS TXT
challenge, verify it, and remove the claim from `/admin`. Verification queries
`_zzira-challenge.<domain>` and changes state only when the exact
`zzira-domain-verification=<token>` value exists. Domain names are normalized
to lowercase fully qualified DNS names. The domain list and detail APIs expose
the published `domains` resource and claim status shapes with opaque paging.

IP allowlist and data-residency policies have durable rules, enabled/disabled
state, product-resource attachments, resource metadata and ticket links. The
API implements list/type filtering, create, detail, whole-policy update,
deletion, resource add/update/remove, and validation with the published 200,
202, and 204 response shapes. IP values must be valid addresses or CIDR ranges;
resources must be product ARIs owned by the organization. `/admin` lets an
administrator create a scoped policy, review its rules and application state,
enable or disable it, and delete it. Mutations and resource changes are audited.
Network enforcement and physical data placement remain separate runtime work.

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
organization discovery, directory filters, SCIM provisioning and global
deactivation, policy enforcement, cross-organization domain ownership checks,
license limits, and full
central-host rate limiting remain in
the active plan and are reported as unassessed or missing in operation coverage.

## Verification

- Store integration tests cover workspace provisioning, membership migration,
  direct roles, group-derived administration and product access, managed
  profiles, directory-scoped suspension across organizations, credential
  revocation after the final active directory, and audit events.
- API integration tests cover bearer authentication, permission denial,
  organization/directory/product discovery, group creation/detail/search/count/
  statistics/deletion, directory-user search/statistics, atomic invitation
  assignments and delivery enqueueing, product activity and last-active dates,
  organization event query/poll/detail/action operations, membership and role
  mutations, domain claims, all policy/resource operations, effective
  assignments, partial results, conflicts, expansions, and audit persistence.
- Playwright covers the complete group and product-access journey,
  invitation-time product/group access, two-second visible product activity,
  domain claim add/remove, policy create/enable/delete, managed-profile editing,
  ordinary-user denial, WCAG scans, light/dark themes, and 320px reflow.
