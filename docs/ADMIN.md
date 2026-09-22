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

**Passwords** belong to the person who signs in with them. An invitation
provisions an account whose password is a value nobody typed, so a new person
is reached by a **sign-in link**: an administrator issues one from the person's
row on `/admin`, and the page shows it once, on the answer to the request that
made it -- it is a credential, so it is never redirected to or written into an
audit detail. Only the link's hash is stored. It works once, expires after 24
hours, and issuing another retires the one before it. When SMTP is configured
and `ZZIRA_EXTERNAL_URL` is set, it is emailed to the person as well.

Opening the link asks for a new password and nothing else, so a link that has
been used, has expired, or was never issued shows no form. Setting a password
through a link ends every session that account had, because whoever set it is
the only one who should now be signed in. A person under a policy that enforces
single sign-on has no password to set, and the page says so.

Someone who has forgotten their password asks for a link themselves at
`/password/forgot`, linked from the sign-in page. The answer is the same
whatever address is typed, so the page does not say who has an account here,
and one account is sent at most one link every five minutes. A site that
cannot send email -- no SMTP, or no `ZZIRA_EXTERNAL_URL` to write into the
link -- says so and sends the person to an administrator instead of pretending
a message is on its way.

Anyone signed in changes their own password on their profile, under
**Password**: the current password, then the new one twice. The new password
meets the rule their authentication policy applies (at least 8 characters, or
longer where the policy says so, and at most 72 -- where bcrypt stops reading).
The change ends every other session they left open and keeps the one they
changed it from.

**Two-step verification** is the RFC 6238 code an authenticator app shows.
A person turns it on for their own account from their profile: the key is
shown once, they confirm with a code from the app, and ten recovery codes are
handed over -- once, and kept only as hashes. From then on a password earns a
sign-in challenge rather than a session: `/login/verify` asks for the code, and
a recovery code stands in for the app once each. A challenge lasts ten minutes
and five wrong codes, then it is thrown away. Turning it off asks for the
password again, so a session someone walked away from cannot quietly take the
second step off the account.

The secret is sealed with the site's credential encryption key
(`ZZIRA_IDENTITY_ENCRYPTION_KEY`); without one, the site says it cannot keep a
secret rather than keeping one in the clear. `mfaEnabled` on the managed
account, and the search filter over it, report this and nothing else.

An authentication policy with `requireTwoStep` holds a sign-in by someone who
has not enrolled: the password is accepted, and `/login/enrol` is where they
set up the app before anything else. An administrator resets a second step
from the person's row on `/admin` when both the app and the recovery codes are
gone, which is written to the audit log as `user.two-step.reset`.

Single sign-on is not asked for a code: the identity provider is where that
account's verification lives. An API token is not either -- it is a credential
of its own, revoked on its own.

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
- **Authentication policies:** `type=authentication-policy` carries `config` instead of `rule.in`: `enforceSSO`, `requireTwoStep`, `sessionDurationMinutes` (5 minutes to 30 days, default 30 days), `passwordMinimumLength` (8 to 72, default 8) and `default`. `GET/POST/DELETE .../policies/{policyId}/members[/{accountId}]` list, add and remove the people it covers; a person belongs to one policy, so adding them to a second leaves the first, and anyone in none -- and in no group the policies cover -- gets the enabled policy marked `default`. Groups are covered from `/admin`; the admin API covers people. An enabled policy is applied at sign-in: `enforceSSO` refuses a password (403 with a page that names the identity provider) and admits only the OIDC flow, and every session the policy covers -- password or SSO -- expires after its duration. A disabled policy, or no policy, leaves the site's own 30-day session. See **Authentication policies** below.
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
| SCIM user provisioning | `/scim/directory/{directoryId}` serves SCIM 2.0 Users and Groups ([SCIM.md](SCIM.md)). The organization bean reports `scimManaged: true` once a provider has written to the directory, and `/admin` shows where to point a provider, whether one has written, who it manages, and the keys it writes with: a key provisions one directory, is shown once, says when it was last used, and is revoked on its own. |
| Authentication policies (enforced SSO, required two-step verification, session duration, shortest password, policy membership) | Built and enforced where each applies; see **Authentication policies** below. |
| Two-step verification | Built: enrolment, recovery codes, the code at sign-in, a policy that requires it, and an administrator's reset. The key is shown as text and a setup link; there is no QR image. |
| Data security policies | Missing. |

## Authentication policies

An authentication policy says how the people it covers sign in. `/admin` writes
them under **Authentication policies**: name, session duration in minutes,
**Single sign-on only**, and **Covers everyone in no other policy**, which marks
the organization's default. Each policy lists the people and the groups it
covers, with a select to add one and a button to remove one, and the settings
are editable in place.

- A person belongs to one policy. Adding them to a second takes them out of the
  first (`authentication_policy_members` is unique on the person), so one policy
  always answers for them.
- A policy also covers groups, which is how a site puts its contractors under
  one policy once rather than naming every contractor and the next one to
  arrive. A group belongs to one policy for the same reason a person does.
- Being named on a policy wins over being in a group under one, because naming
  somebody is the more particular statement. Somebody in two covered groups
  gets the policy of the group they joined first, so two reads agree.
- Anyone in no policy of their own, and in no covered group, gets the enabled
  policy marked default. With
  no default policy, nothing is enforced: that is every site until an
  administrator writes one.
- A disabled policy enforces nothing, which is how a policy is retired without
  deleting it.
- `enforceSSO` refuses `POST /login` for its members with 403 and a page saying
  the organization signs that account in through its identity provider. It also
  refuses a password change and a sign-in link, which would both be setting a
  password nothing would accept. API tokens are unaffected;
  `ZZIRA_LOCAL_CREDENTIALS=off` is the site-wide switch that closes those too.
- `requireTwoStep` holds a password sign-in by someone who has not enrolled
  until they do: they are sent to `/login/enrol` rather than refused, because
  refusing them would leave nobody able to enrol. Single sign-on is not held.
- `passwordMinimumLength` is read where a password is set, not at sign-in: a
  password already in use goes on working until it is replaced. It is between
  8 and 72 characters, and a site with no policy asks for 8.
- The session duration applies to the session cookie and the stored session
  alike, for password and single sign-on alike.
- Adding and removing members is audited as `policy.member-added` and
  `policy.member-removed`; creating, updating and deleting a policy keep the
  existing policy audit actions.

## Gaps

Tracked in [PLAN.md](../PLAN.md).

- One server serves one site; an organization cannot hold several sites, and organization discovery returns only that site's organization.
- SAML single sign-on.
- SCIM provisions people and groups ([SCIM.md](SCIM.md)); product access is not provisioned with them, and a provider authenticates as an organization administrator rather than with a directory-scoped key. The browser page reads what a provider has written rather than connecting one.
- Authentication policies enforce single sign-on, two-step verification, session duration and the shortest password; password expiry and the rest of Atlassian's password strength rules are missing, and a policy covers people one at a time rather than a whole group.
- Two-step verification shows its key as text and a setup link rather than a QR image, and the authenticator app is the only second factor: no WebAuthn, no passkeys, no SMS.
- Account claiming from verified domains (managed vs unmanaged accounts), and domain ownership checks across organizations.
- The Atlassian user management API (`/users/{account_id}/manage/...`: profile, email, lifecycle, API tokens).
- Organization API keys distinct from user API tokens.
- Data security policies (app access rules, public links, export controls).
- Data residency placement; policies are recorded but not applied.

## Tests

- `internal/store/admin_test.go`: provisioning, membership mirroring, direct and group roles, managed profiles, directory-scoped suspension, credential revocation, audit events.
- `internal/admin/http_test.go`: every Organizations API operation, auth, errors, partial results, conflicts, expansions, audit persistence.
- `internal/admin/authentication_policies_test.go`: settings validation, membership moving with the person, the default policy, a disabled policy enforcing nothing, and what sign-in and a password change do under each.
- `internal/authn/totp_test.go`: the RFC 6238 vectors, the steps either side of now, and what is not a code.
- `internal/authn/two_step_test.go`: the challenge a password earns, the code and recovery code that answer it, the guessing it cuts off, and turning it off.
- `internal/web/forgot_password_test.go`: the link a forgotten password asks for, the same answer for an address with no account, the cooldown, and a site that cannot send email.
- `internal/authn/password_test.go`: changing a password, the rules that refuse one, the sessions a change ends, and the sign-in link's single use and expiry.
- `e2e/password.spec.ts`: an invited person reached by a sign-in link, setting a password, replacing it from their profile, and the session that ends with it.
- `e2e/two_step.spec.ts`: enrolling an authenticator app, the code at sign-in, a recovery code used once, and turning it off.
- `e2e/admin.spec.ts`: group and product access, invitations, product activity, domains, policies, authentication policies (a member refused a password sign-in and admitted again once the policy goes), managed profiles, ordinary-user denial, accessibility, themes and 320px reflow.
