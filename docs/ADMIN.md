# Organization and site administration

Every workspace is provisioned as an Atlassian organization with one site, three products (Jira Software, Jira Service Management, Confluence) and an internal user directory. Administrators manage people, groups, product access, domains, access policies, identity providers, apps and Jira site settings from `/admin`, and through the Atlassian Organizations REST API mounted at `/admin/v1` and `/admin/v2`. For status, see [CLOUD_PARITY.md](CLOUD_PARITY.md).

## Where things are administered

| Surface | Page | Doc |
| --- | --- | --- |
| People, groups, product access, domains, policies, audit log | `/admin` | this page |
| Identity providers (OpenID Connect) | `/admin` › Identity providers | [shauth-sso.md](shauth-sso.md) |
| Apps and Connect data migration | `/admin` › Apps | [APPS.md](APPS.md), [JIRA_PLATFORM.md](JIRA_PLATFORM.md#connect-app-migration) |
| Jira configuration: announcement banner, features, time tracking, navigator columns, application properties | `/admin` › Jira configuration | [JIRA_SITE_CONFIGURATION.md](JIRA_SITE_CONFIGURATION.md) |
| Global permissions and the permission helper | `/admin` › Global permissions, `/admin/permission-helper` | [PERMISSION_SCHEMES.md](PERMISSION_SCHEMES.md) |
| Issue events and the notification helper | `/admin` › Events, `/admin/notification-helper` | [NOTIFICATION_SCHEMES.md](NOTIFICATION_SCHEMES.md) |
| Filter emails (all filter subscriptions) | `/admin` › Filter emails | [FILTERS.md](FILTERS.md) |
| Project categories | `/admin` › Project categories | [PROJECT_GOVERNANCE.md](PROJECT_GOVERNANCE.md) |
| Data classification levels | `/admin` › Data classification levels | [CLASSIFICATION_LEVELS.md](CLASSIFICATION_LEVELS.md) |
| Jira schemes, roles, screens, fields, workflows, statuses, automation | `/settings/...` | [JIRA_PLATFORM.md](JIRA_PLATFORM.md), [PROJECT_ROLES.md](PROJECT_ROLES.md), [AUTOMATION.md](AUTOMATION.md) |
| Teams and services | People › Teams, Service management › Services | [JIRA_PLATFORM.md](JIRA_PLATFORM.md#plans-and-teams) |
| Confluence site administration | wiki admin pages | [CONFLUENCE_SITE_SURFACES.md](CONFLUENCE_SITE_SURFACES.md), [SITE_SETTINGS.md](SITE_SETTINGS.md) |

## Authorization model

Role bindings have an organization, site, product, project or space scope and target a user or a group. Direct and group bindings use one evaluator (`internal/authz/authz.go`).

| Role | Effect |
| --- | --- |
| `atlassian/org-admin` | Administers every site in the organization |
| `atlassian/site-admin` | Administers the site, its directory and product access |
| `atlassian/site-user` | Enters the site |
| `atlassian/product-admin`, `atlassian/admin` | Enter the product and manage its access |
| `atlassian/user-access-admin` | Manages product access without product use |
| `atlassian/product-user`, `atlassian/user`, `atlassian/basic`, `atlassian/guest`, `atlassian/contributor`, `atlassian/viewer` | Enter the bound product |
| `atlassian/customer`, `atlassian/stakeholder` | Enter Jira Service Management; bindable only to that product |
| `atlassian/ai-access` | Bindable to a product through the API; grants no site entry |

- Disabled users cannot enter a site even with a role.
- Workspace `admin` and `member` memberships are mirrored into site and product role bindings by the `sync_membership_role_bindings` trigger (`migrations/033_organization_authorization.sql`).
- Jira project access comes from [permission schemes](PERMISSION_SCHEMES.md); Confluence space access from [space permissions](SPACE_PERMISSIONS.md).

## UI

Site administrators use `/admin`. Other users do not see the navigation item and get 403.

- **Overview:** organization and Cloud ids, enabled products, the internal directory.
- **People:** invite (with product access and groups), edit a managed profile, suspend, restore, remove. Administrators cannot suspend or remove themselves.
- **Groups:** create, delete, add or remove members, grant or revoke each product's access.
- **Products:** set each product's plan (free, standard, premium, enterprise).
- **Domains:** add a domain claim, copy its DNS TXT challenge, verify, remove.
- **Policies:** create an IP allowlist or data residency policy, review rules and product scope, enable, disable, delete.
- **Audit log:** search event text and filter by action.

Every user, group, membership, role, domain, policy and product-plan change writes an organization audit event in the same transaction.

**API tokens** are made by the person who uses them, on their own profile: a
label saying what it is for, an expiry within a year (the form opens on a year
out, and an empty date means none), and the secret shown once, in the response
to the request that made it. Only the token's hash is kept, so a secret that
was not copied is revoked and replaced rather than recovered. A token signs in
as its owner over HTTP Basic -- the email address as the user, the token as the
password -- and is revoked from the same card. One person holds at most 20.

Because the secret exists once, the page renders it rather than redirecting:
a redirect would either lose it or carry it in a URL, where it would outlive
the response in browser history and access logs. A reload therefore replays
the request, so the form carries the id of the creation it makes; a second
arrival of that id creates nothing and the page says the token was already
made.

**Suspension and removal** revoke the account's sessions and API tokens when it has no other active directory. On reconnect, the affected browser checks access before replaying its outbox, purges its private replica and page cache, and goes to the signed-out page.

## Organizations REST API

All operations of the pinned Organizations API (`api/specs/organization-admin.json`) are served:

| Method | Path |
| --- | --- |
| GET | `/admin/v1/orgs`, `/admin/v1/orgs/{orgId}` |
| GET | `/admin/v1/orgs/{orgId}/events`, `/events-stream`, `/events/{eventId}`, `/event-actions` |
| GET | `/admin/v1/orgs/{orgId}/domains`, `/domains/{domainId}` |
| GET/POST | `/admin/v1/orgs/{orgId}/policies` |
| GET/PUT/DELETE | `/admin/v1/orgs/{orgId}/policies/{policyId}` |
| POST | `/admin/v1/orgs/{orgId}/policies/{policyId}/resources` |
| PUT/DELETE | `/admin/v1/orgs/{orgId}/policies/{policyId}/resources/{resourceId}` |
| GET | `/admin/v1/orgs/{orgId}/policies/{policyId}/validate` |
| GET | `/admin/v1/orgs/{orgId}/policies/{policyId}/members` |
| POST/DELETE | `/admin/v1/orgs/{orgId}/policies/{policyId}/members/{accountId}` |
| GET | `/admin/v1/orgs/{orgId}/users` |
| GET | `/admin/v1/orgs/{orgId}/directory/users/{accountId}/last-active-dates` |
| POST | `/admin/v1/orgs/{orgId}/users/{userId}/role-assignments/{assign\|revoke}`, `/roles/{assign\|revoke}` |
| GET | `/admin/v2/orgs/{orgId}/directories` |
| GET/POST | `/admin/v2/orgs/{orgId}/directories/{directoryId}/groups` |
| GET | `.../groups/count`, `.../groups/stats` |
| POST | `.../groups/search` |
| GET/DELETE | `.../groups/{groupId}` |
| POST | `.../groups/{groupId}/memberships` |
| DELETE | `.../groups/{groupId}/memberships/{accountId}` |
| GET | `.../groups/{groupId}/role-assignments` |
| POST | `.../groups/{groupId}/role-assignments/{assign\|revoke}` |
| GET | `.../users`, `.../users/count`, `.../users/stats`, `.../users/{userId}` |
| POST | `.../users/search` |
| DELETE | `.../users/{accountId}` |
| POST | `.../users/{accountId}/{suspend\|restore}` |
| GET | `.../users/{accountId}/role-assignments` |
| POST | `/admin/v2/orgs/{orgId}/users/invite` |
| POST | `/admin/v2/orgs/{orgId}/workspaces` |

`...` is `/admin/v2/orgs/{orgId}/directories/{directoryId}`.

### Behavior

- **Auth:** `Authorization: Bearer <api-token>` from an organization or site administrator; otherwise 401 or 403. An `orgId` other than the site's organization is 404. Site Jira APIs keep Basic authentication.
- **Paging:** cursors are opaque encoded offsets; a malformed cursor is 400.
- **Search:** group and user search support Atlassian's sorting, exact-list, text, directory, membership, lifecycle, resource, role, domain and expansion filters. Directory id `-` covers every directory the caller administers. Unknown query parameters are 400.
- **Workspaces:** discovery returns product ARIs.
- **Role assignments:** filter by directory, resource owner, resource id and role id, and report whether access is direct or inherited from a group.
- **Last active dates:** product activity is recorded after an authenticated product page stays visible for two seconds (`POST /rest/zzira/1/product-activity`). The response lists product instance ids, Jira product keys, UTC dates and times, and the first organization membership time; a user who never viewed a product has an empty `product_access`.
- **Events:** the same immutable records as the audit log. Query by text, action, actor, IP, product, location, millisecond time bounds, limit (max 500). Each event records the request's client address and user agent (set on the database connection and merged in by a trigger). Filtered queries are limited to 10 per user per minute (429 with `Retry-After`); `events-stream` is not limited, defaults to ascending order and always returns a reusable cursor.
- **Domains:** names are normalized to lowercase FQDNs. Verification looks up `_zzira-challenge.<domain>` for the exact `zzira-domain-verification=<token>` TXT value. A verified domain is required for a project's custom sender email ([PROJECT_GOVERNANCE.md](PROJECT_GOVERNANCE.md)).
- **Policies:** types `ip-allowlist` and `data-residency` can be created; `type=data-security` is accepted as a list filter only. IP values must be addresses or CIDR ranges; resources must be product ARIs of the organization. Enabled IP allowlists are enforced: a Jira Software, Jira Service Management or Confluence request from outside every enabled allowlist covering that product gets 403 (`internal/store/ip_allowlist.go`). Administration and sign-in stay reachable. Data residency policies are recorded only; all data lives in one PostgreSQL database.
- **Authentication policies:** `type=authentication-policy` carries `config` instead of `rule.in`: `enforceSSO`, `sessionDurationMinutes` (5 minutes to 30 days, default 30 days) and `default`. `GET/POST/DELETE .../policies/{policyId}/members[/{accountId}]` list, add and remove the people it covers; a person belongs to one policy, so adding them to a second leaves the first, and anyone in none gets the enabled policy marked `default`. An enabled policy is applied at sign-in: `enforceSSO` refuses a password (403 with a page that names the identity provider) and admits only the OIDC flow, and every session the policy covers -- password or SSO -- expires after its duration. A disabled policy, or no policy, leaves the site's own 30-day session. See **Authentication policies** below.
- **Plans and invitations:** inviting needs at least one enabled paid product (402). An invitation that takes a free product past its limit (10 users; 3 agents for Jira Service Management) is 409. Each account's access, groups, optional email and audit event commit atomically. A multi-account request with failures returns `206 Partial Content` with per-assignment `ERROR` results and keeps the successes.
- **Email:** invitation email needs SMTP (503 otherwise). Delivery uses a leased PostgreSQL outbox with exponential backoff (capped at one hour); a message is dead after eight failed attempts.

| Environment variable | Purpose |
| --- | --- |
| `ZZIRA_SMTP_ADDR` | SMTP endpoint, `host:port` |
| `ZZIRA_SMTP_FROM` | Sender address |
| `ZZIRA_SMTP_USERNAME`, `ZZIRA_SMTP_PASSWORD` | Optional SMTP credentials; set both or neither |

## Enterprise identity

| Capability | State |
| --- | --- |
| Domain verification | Built (DNS TXT). |
| Managed accounts | Every directory account is reported `claimStatus: managed`; administrators edit profiles, suspend, restore and remove. Claim status is not derived from verified domains. |
| OpenID Connect SSO (Google, Microsoft Entra ID, Atlassian, any discovered OIDC provider) | Built; see [shauth-sso.md](shauth-sso.md). |
| IP allowlists | Built and enforced. |
| SAML SSO | Missing. |
| SCIM user provisioning | `/scim/directory/{directoryId}` serves SCIM 2.0 Users and Groups ([SCIM.md](SCIM.md)). The organization bean reports `scimManaged: true` once a provider has written to the directory. |
| Authentication policies (enforced SSO, session duration, policy membership) | Built and enforced at sign-in; see **Authentication policies** below. Two-step verification and password rules are not: `mfaEnabled` is stored and reported but nothing enrolls or enforces it, and there is no password-change flow to hold rules. |
| Data security policies | Missing. |

## Authentication policies

An authentication policy says how the people it covers sign in. `/admin` writes
them under **Authentication policies**: name, session duration in minutes,
**Single sign-on only**, and **Covers everyone in no other policy**, which marks
the organization's default. Each policy lists its members with a select to add
one and a button to remove one, and the settings are editable in place.

- A person belongs to one policy. Adding them to a second takes them out of the
  first (`authentication_policy_members` is unique on the person), so one policy
  always answers for them.
- Anyone in no policy of their own gets the enabled policy marked default. With
  no default policy, nothing is enforced: that is every site until an
  administrator writes one.
- A disabled policy enforces nothing, which is how a policy is retired without
  deleting it.
- `enforceSSO` refuses `POST /login` for its members with 403 and a page saying
  the organization signs that account in through its identity provider. API
  tokens are unaffected; `ZZIRA_LOCAL_CREDENTIALS=off` is the site-wide switch
  that closes those too.
- The session duration applies to the session cookie and the stored session
  alike, for password and single sign-on alike.
- Adding and removing members is audited as `policy.member-added` and
  `policy.member-removed`; creating, updating and deleting a policy keep the
  existing policy audit actions.

## Gaps

Tracked in [PLAN.md](../PLAN.md).

- One server serves one site; an organization cannot hold several sites, and organization discovery returns only that site's organization.
- SAML single sign-on.
- SCIM provisions people and groups ([SCIM.md](SCIM.md)); product access is not provisioned with them, and a provider authenticates as an organization administrator rather than with a directory-scoped key.
- Authentication policies enforce single sign-on and session duration; two-step verification enrolment and password requirements are missing, and a policy covers people one at a time rather than a whole group.
- Account claiming from verified domains (managed vs unmanaged accounts), and domain ownership checks across organizations.
- The Atlassian user management API (`/users/{account_id}/manage/...`: profile, email, lifecycle, API tokens).
- Organization API keys distinct from user API tokens.
- Data security policies (app access rules, public links, export controls).
- Data residency placement; policies are recorded but not applied.

## Tests

- `internal/store/admin_test.go`: provisioning, membership mirroring, direct and group roles, managed profiles, directory-scoped suspension, credential revocation, audit events.
- `internal/admin/http_test.go`: every Organizations API operation, auth, errors, partial results, conflicts, expansions, audit persistence.
- `internal/admin/authentication_policies_test.go`: settings validation, membership moving with the person, the default policy, a disabled policy enforcing nothing, and what sign-in does under each.
- `e2e/admin.spec.ts`: group and product access, invitations, product activity, domains, policies, authentication policies (a member refused a password sign-in and admitted again once the policy goes), managed profiles, ordinary-user denial, accessibility, themes and 320px reflow.
