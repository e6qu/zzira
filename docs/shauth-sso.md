# Identity provider sign-in

ZZIRA supports simultaneous browser sign-in through Shauth-compatible OpenID
Connect, Google, Microsoft Entra ID, and Atlassian accounts. Google, Microsoft,
and Shauth use the OpenID Connect Authorization Code flow with S256 PKCE.
Atlassian uses its OAuth 2.0 three-legged authorization flow and User Identity
API because Atlassian 3LO does not return an OpenID Connect ID token.

SSO is enabled only when all settings below are set. Leaving all of them unset
keeps the normal password sign-in flow; a partial configuration prevents the
server from starting rather than silently changing authentication behavior.

| Variable | Purpose |
| --- | --- |
| `ZZIRA_SHAUTH_ISSUER` | Provider issuer URL. HTTPS is required in production. |
| `ZZIRA_SHAUTH_CLIENT_ID` | Registered OIDC client ID. |
| `ZZIRA_SHAUTH_CLIENT_SECRET` | Registered OIDC client secret. |
| `ZZIRA_GOOGLE_CLIENT_ID` | Google OAuth client ID. |
| `ZZIRA_GOOGLE_CLIENT_SECRET` | Google OAuth client secret. |
| `ZZIRA_MICROSOFT_CLIENT_ID` | Microsoft Entra application client ID. |
| `ZZIRA_MICROSOFT_CLIENT_SECRET` | Microsoft Entra application client secret. |
| `ZZIRA_MICROSOFT_TENANT_ID` | Tenant UUID or verified tenant domain. Tenant-independent authorities are rejected so the token issuer can be checked exactly. |
| `ZZIRA_ATLASSIAN_CLIENT_ID` | Atlassian OAuth 2.0 integration client ID. |
| `ZZIRA_ATLASSIAN_CLIENT_SECRET` | Atlassian OAuth 2.0 integration client secret. The integration must include the User Identity API and `read:me` scope. |
| `ZZIRA_EXTERNAL_URL` | Canonical externally reachable ZZIRA origin. |
| `ZZIRA_ALLOW_INSECURE_OIDC=true` | Local-development only: permits an HTTP loopback issuer. Never set this in production. |
| `COOKIE_SECURE` | Optional override for cookie transport security. If unset, an HTTPS `ZZIRA_EXTERNAL_URL` enables secure cookies automatically; never disable it for a production HTTPS origin. |
| `ZZIRA_BOOTSTRAP_ADMIN_EMAIL` | Optional. Grants this email admin membership in the default workspace on every boot, creating the user first if none exists. A first OIDC sign-in provisions an ordinary `member`; set this to your identity provider's break-glass account (or any account that needs admin) to give it the admin role instead. |

Configure any provider by setting its complete variable group. A partial group
prevents startup. The sign-in page shows every configured provider alongside
password sign-in, and the administration page reports the active provider,
protocol, and issuer without exposing secrets.

Register callback URLs as follows:

| Provider | Callback URL |
| --- | --- |
| Shauth | `<ZZIRA_EXTERNAL_URL>/auth/shauth/callback` |
| Google | `<ZZIRA_EXTERNAL_URL>/auth/google/callback` |
| Microsoft | `<ZZIRA_EXTERNAL_URL>/auth/microsoft/callback` |
| Atlassian | `<ZZIRA_EXTERNAL_URL>/auth/atlassian/callback` |

Shauth additionally supports:

- Post-logout redirect URI: `<ZZIRA_EXTERNAL_URL>/auth/shauth/logout/complete`
- Back-channel logout URI: `<ZZIRA_EXTERNAL_URL>/auth/shauth/backchannel-logout`
- Grant: `authorization_code`
- Scopes: `openid profile email`

`GET /auth/validation` is the relying-party identity check some providers'
SSO validators load after login: with a session it returns `200` and the
signed-in member's provider username and email as `data-testid`-marked HTML;
without one it redirects `302` to `/signed-out`.

`POST /auth/shauth/backchannel-logout` implements OpenID Connect Back-Channel
Logout 1.0. The provider posts a `logout_token` here when a session it owns
ends; ZZIRA verifies it (signature, issuer, audience, a required `sid` or
`sub`, the exact `http://schemas.openid.net/event/backchannel-logout` event,
and a five-minute freshness window) and revokes only the OIDC sessions bound
to the token's issuer-scoped `sid`, or its `(issuer, subject)` pair. Password
sessions and a coincidentally equal `sid` from another issuer are not revoked.
Each token's `jti` is claimed exactly once, so a replayed token is rejected
rather than revoking sessions a second time.

`GET /monitoring/observation`, bearer-authenticated against
`ZZIRA_MONITORING_TOKEN`, publishes a live observation of ZZIRA's own health
(`e6qu.monitoring/v2`) for centralized collection: real database reachability
and a real stored-issue count, never a cached or fabricated figure.

OIDC ID tokens must include a stable subject, `nonce`, and `aud`. Google and
Shauth must also assert `email_verified: true`. Microsoft Entra tenant tokens
are accepted only after signature, audience, nonce, and exact tenant issuer
validation; ZZIRA uses `email`, or an email-shaped `preferred_username` when
the optional email claim is absent. A token with multiple audiences must
identify ZZIRA as its authorized party (`azp`). Atlassian identities must have
an active `atlassian` account type, stable `account_id`, and valid email from
`GET https://api.atlassian.com/me` using the newly exchanged access token.

On the first sign-in ZZIRA binds the trusted provider's immutable
`(issuer, subject)` pair to an existing active member
with that email if one exists, or otherwise provisions a new `member` of the
default workspace for that email; later sign-ins use the immutable pair, not
a mutable email or username. Multiple providers with the same trusted email
link to the same ZZIRA account. Each successful provider sign-in is recorded in
the organization audit log.

Signed-in users can review connected provider name, issuer, current asserted
email, immutable subject, and connection time on their profile. Connecting a
new provider starts a provider-bound authorization transaction tied to the
current user instead of relying on email matching. Disconnecting a provider
revokes every browser session from that issuer while preserving sessions from
other providers. ZZIRA refuses to remove the last external identity, preventing
an externally provisioned account from losing its final known sign-in path.

If an identity carries a `preferred_username` or Atlassian nickname, it is recorded as the
account’s display handle (`data-shauth-user` on the account control in the
product header) and refreshed on every sign-in. Its absence is not an error —
the account control falls back to the email’s local part.

The flow keeps the provider key, state, nonce, and PKCE verifier server-side
with a ten-minute, single-use lifetime. A state issued for one provider cannot
be consumed by another. State is also HMAC-bound to a short-lived, HttpOnly,
SameSite browser cookie, preventing a callback initiated in another browser
from swapping that browser into the attacker's identity. Browser session
cookies contain only ZZIRA’s opaque token;
the verified ID token is retained server-side for the session lifetime. If the
provider advertises an `end_session_endpoint` in its discovery document, logout
uses the standard RP-initiated logout parameters and then returns to the
registered post-logout redirect URI. Providers without that endpoint still end
the ZZIRA session and show the signed-out page. Discovered authorization and
logout endpoints are transport-validated during startup, and each final URL is
validated again after its OAuth/OIDC query parameters are assembled immediately
before the intentional cross-origin redirect.

Standards references:

- [OpenID Connect Core 1.0](https://openid.net/specs/openid-connect-core-1_0.html)
- [OpenID Connect Discovery 1.0](https://openid.net/specs/openid-connect-discovery-1_0.html)
- [OpenID Connect Back-Channel Logout 1.0](https://openid.net/specs/openid-connect-backchannel-1_0.html)
- [OpenID Connect RP-Initiated Logout 1.0](https://openid.net/specs/openid-connect-rpinitiated-1_0.html)
- [OAuth 2.0 Security Best Current Practice (RFC 9700)](https://www.rfc-editor.org/rfc/rfc9700.html)
- [Google OpenID Connect](https://developers.google.com/identity/openid-connect/openid-connect)
- [Microsoft identity platform OpenID Connect](https://learn.microsoft.com/en-us/entra/identity-platform/v2-protocols-oidc)
- [Atlassian OAuth 2.0 (3LO)](https://developer.atlassian.com/cloud/jira/platform/oauth-2-3lo-apps/)
