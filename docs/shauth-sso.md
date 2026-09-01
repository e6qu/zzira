# ShAuth SSO

ZZIRA supports optional browser SSO through OpenID Connect Discovery. The
`SHAUTH` name reflects the reference provider used by e6qu, but the
implementation uses the standard Authorization Code flow with S256 PKCE and
works with any compliant provider.

SSO is enabled only when all settings below are set. Leaving all of them unset
keeps the normal password sign-in flow; a partial configuration prevents the
server from starting rather than silently changing authentication behavior.

| Variable | Purpose |
| --- | --- |
| `ZZIRA_SHAUTH_ISSUER` | Provider issuer URL. HTTPS is required in production. |
| `ZZIRA_SHAUTH_CLIENT_ID` | Registered OIDC client ID. |
| `ZZIRA_SHAUTH_CLIENT_SECRET` | Registered OIDC client secret. |
| `ZZIRA_EXTERNAL_URL` | Canonical externally reachable ZZIRA origin. |
| `ZZIRA_ALLOW_INSECURE_OIDC=true` | Local-development only: permits an HTTP loopback issuer. Never set this in production. |
| `ZZIRA_BOOTSTRAP_ADMIN_EMAIL` | Optional. Grants this email admin membership in the default workspace on every boot, creating the user first if none exists. ZZIRA's OIDC binding only ever matches a verified sign-in to a pre-existing member by email — set this to your identity provider's break-glass account so it has somewhere to bind to. |

Register the client with the provider using:

- Redirect URI: `<ZZIRA_EXTERNAL_URL>/auth/shauth/callback`
- Post-logout redirect URI: `<ZZIRA_EXTERNAL_URL>/auth/shauth/logout/complete`
- Back-channel logout URI: `<ZZIRA_EXTERNAL_URL>/auth/shauth/backchannel-logout`
- Grant: `authorization_code`
- Scopes: `openid profile email`

`GET /auth/validation` is the relying-party identity check some providers'
SSO validators load after login: with a session it returns `200` and the
signed-in member's display name and email as `data-testid`-marked HTML;
without one it redirects `302` to `/signed-out`.

`POST /auth/shauth/backchannel-logout` implements OpenID Connect Back-Channel
Logout 1.0. The provider posts a `logout_token` here when a session it owns
ends; ZZIRA verifies it (signature, issuer, audience, a required `sid` or
`sub`, the exact `http://schemas.openid.net/event/backchannel-logout` event,
and a five-minute freshness window) and revokes every session bound to the
token's subject. Each token's `jti` is claimed exactly once, so a replayed
token is rejected rather than revoking sessions a second time.

The ID token must include a stable subject, `nonce`, `aud`, and a verified
`email` (`email_verified: true`). On the first sign-in ZZIRA binds the trusted
provider’s immutable `(issuer, subject)` pair to an existing member with that
email; later sign-ins use the immutable pair, not a mutable email or username.
The identity provider cannot create or silently grant project access. This
preserves the existing workspace membership and API-token authorization model.

If the ID token carries a `preferred_username` claim, it is recorded as the
account’s display handle (`data-shauth-user` on the account control in the
product header) and refreshed on every sign-in. Its absence is not an error —
the account control falls back to the email’s local part.

The flow keeps state, nonce, and PKCE verifier server-side with a ten-minute,
single-use lifetime. Browser cookies contain only ZZIRA’s opaque session token;
the verified ID token is retained server-side for the session lifetime. If the
provider advertises an `end_session_endpoint` in its discovery document, logout
uses the standard RP-initiated logout parameters and then returns to the
registered post-logout redirect URI. Providers without that endpoint still end
the ZZIRA session and show the signed-out page.
