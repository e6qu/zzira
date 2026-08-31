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

Register the client with the provider using:

- Redirect URI: `<ZZIRA_EXTERNAL_URL>/auth/shauth/callback`
- Post-logout redirect URI: `<ZZIRA_EXTERNAL_URL>/auth/shauth/logout/complete`
- Grant: `authorization_code`
- Scopes: `openid profile email`

The ID token must include a stable subject, `nonce`, `aud`, and a verified
`email` (`email_verified: true`). On the first sign-in ZZIRA binds the trusted
provider’s immutable `(issuer, subject)` pair to an existing member with that
email; later sign-ins use the immutable pair, not a mutable email or username.
The identity provider cannot create or silently grant project access. This
preserves the existing workspace membership and API-token authorization model.

The flow keeps state, nonce, and PKCE verifier server-side with a ten-minute,
single-use lifetime. Browser cookies contain only ZZIRA’s opaque session token;
the verified ID token is retained server-side for the session lifetime. If the
provider advertises an `end_session_endpoint` in its discovery document, logout
uses the standard RP-initiated logout parameters and then returns to the
registered post-logout redirect URI. Providers without that endpoint still end
the ZZIRA session and show the signed-out page.
