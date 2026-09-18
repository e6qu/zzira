# Identity provider sign-in

Browser sign-in through OpenID Connect providers (Shauth, Google, Microsoft Entra ID, and any provider an administrator registers) and Atlassian accounts, alongside password sign-in. OIDC providers use the Authorization Code flow with S256 PKCE. Atlassian uses OAuth 2.0 (3LO) and the User Identity API, because Atlassian 3LO returns no ID token. Part of [organization and site administration](ADMIN.md); SAML, SCIM and authentication policies are not built (see [ADMIN.md](ADMIN.md#enterprise-identity)). For status, see [CLOUD_PARITY.md](CLOUD_PARITY.md).

## Configuration

Each provider is enabled by setting its whole variable group; a partial group stops the server from starting. With no provider configured, only password sign-in is offered.

| Variable | Purpose |
| --- | --- |
| `ZZIRA_SHAUTH_ISSUER`, `ZZIRA_SHAUTH_CLIENT_ID`, `ZZIRA_SHAUTH_CLIENT_SECRET` | Shauth (any discovered OIDC issuer). HTTPS required. |
| `ZZIRA_GOOGLE_CLIENT_ID`, `ZZIRA_GOOGLE_CLIENT_SECRET` | Google. |
| `ZZIRA_MICROSOFT_CLIENT_ID`, `ZZIRA_MICROSOFT_CLIENT_SECRET`, `ZZIRA_MICROSOFT_TENANT_ID` | Microsoft Entra ID. The tenant is a UUID or verified domain; tenant-independent authorities are refused so the issuer can be checked exactly. |
| `ZZIRA_ATLASSIAN_CLIENT_ID`, `ZZIRA_ATLASSIAN_CLIENT_SECRET` | Atlassian. The integration needs the User Identity API and `read:me`. |
| `ZZIRA_EXTERNAL_URL` | Required with any provider. The canonical origin, without a path. |
| `ZZIRA_IDENTITY_ENCRYPTION_KEY` | Optional base64 32-byte AES key. Required to register providers or rotate secrets in `/admin`. Keep it stable and identical on every replica. |
| `ZZIRA_ALLOW_INSECURE_OIDC=true` | Local development only: allows an HTTP loopback issuer. |
| `COOKIE_SECURE` | Optional. Defaults to secure cookies when `ZZIRA_EXTERNAL_URL` is HTTPS. |
| `ZZIRA_BOOTSTRAP_ADMIN_EMAIL` | Optional. On every boot, gives this email admin membership in the default workspace, creating the user if needed. First OIDC sign-ins otherwise provision ordinary members. |
| `ZZIRA_LOCAL_CREDENTIALS=off` | Optional. Accept only sessions an identity provider established; see [Single sign-on only](#single-sign-on-only). The default, `on`, keeps password sign-in and API tokens. |
| `ZZIRA_ANONYMOUS_ACCESS=off` | Optional. Refuse every caller with no credentials at all, attachment downloads included; see [anonymous access](ANONYMOUS_ACCESS.md). The default, `on`, keeps Jira's anonymous user. |

Callback URLs are `<ZZIRA_EXTERNAL_URL>/auth/<provider-key>/callback` (`shauth`, `google`, `microsoft`, `atlassian`, or a registered key). OIDC providers also use:

- Post-logout redirect: `<ZZIRA_EXTERNAL_URL>/auth/<provider-key>/logout/complete`
- Back-channel logout: `<ZZIRA_EXTERNAL_URL>/auth/<provider-key>/backchannel-logout`
- Grant `authorization_code`, scopes `openid profile email`

## Single sign-on only

`ZZIRA_LOCAL_CREDENTIALS=off` refuses the credentials the installation issued itself. It is the instance's own configuration, like anonymous access, and not a site setting the REST API exposes; the default keeps today's behaviour.

An installation published on the internet behind single sign-on needs it, because `-mode=demo` mints a sign-in password and an API token for every person in its scenario ([DEMO_DATA.md](DEMO_DATA.md)). Seeding such an instance would otherwise hand out working logins that go nowhere near the identity provider.

With it off:

- Password sign-in is refused, and the sign-in page offers no password box. The signed-out page drops its "Use password" link.
- An API token is refused, in Basic authentication and as a bearer token, including on the organization administration API.
- A session created from a password before the switch is refused too: only a session an identity provider established is accepted.
- Every refusal is the ordinary 401 — Jira's `errorMessages` body on the REST APIs, the sign-in page on the browser — so a client sees what it always sees when its credential is not accepted.
- The server refuses to start with no identity provider configured, because nothing would be able to sign anyone in.

Signed app principals are unaffected: a Connect or Forge app authenticates by signing its own request, not with a credential a person holds ([APPS.md](APPS.md)).

## Administration

`/admin` › Identity providers lists every provider with protocol and issuer, never secrets.

- **Disable / enable:** disabling removes the provider from sign-in and account linking, revokes its issuer's sessions and is audited. The setting survives restarts.
- **Register** (needs `ZZIRA_IDENTITY_ENCRYPTION_KEY`): issuer, client id and secret. Discovery and every advertised endpoint are validated before saving. The secret is encrypted with AES-256-GCM bound to the workspace and provider key and never rendered.
- **Rotate:** replaces the secret after a fresh discovery check.
- **Delete:** revokes the issuer's sessions.
- Environment-configured providers are labeled deployment-managed and cannot be overwritten, rotated or deleted.

Registration, rotation, deletion, enabling and disabling are audited.

## Sign-in behavior

**Token checks.**
- ID tokens need a stable `sub`, `nonce` and `aud`. With several audiences, `azp` must name ZZIRA.
- Every OIDC provider except Microsoft must assert `email_verified: true`.
- Microsoft tokens are checked for signature, audience, nonce and exact tenant issuer. The email comes from `email`, or an email-shaped `preferred_username`.
- Atlassian identities come from `GET https://api.atlassian.com/me` with the new access token and need an active `atlassian` account type, a stable `account_id` and a valid email.

**Account binding.**
- The first sign-in binds the provider's `(issuer, subject)` to the active member with that email, or provisions a new `member` of the default workspace.
- Later sign-ins use the `(issuer, subject)` pair, never the email.
- Providers asserting the same trusted email link to the same account.
- Each sign-in is audited.
- `preferred_username`, or the Atlassian nickname, becomes the display handle (`data-shauth-user` on the header's account control) and refreshes on each sign-in. Without one, the handle is the email's local part.

**Profile.** `/profile` lists connected providers with issuer, current email, subject and connection time.
- Connecting another provider starts an authorization bound to the signed-in user; it does not rely on email matching.
- Disconnecting (`POST /profile/identities/{provider}/unlink`) revokes that issuer's sessions only.
- The last external identity cannot be removed.

**Flow security.**
- Provider key, state, nonce and PKCE verifier are kept server-side, single use, for ten minutes. A state issued for one provider cannot be used by another.
- State is HMAC-bound to a short-lived HttpOnly SameSite cookie, so a callback started in another browser is refused.
- Session cookies hold only ZZIRA's opaque token; the ID token stays server-side for the session.
- Authorization and logout URLs are validated at startup and again after query parameters are added, just before the redirect.

**Logout.**
- If discovery advertises `end_session_endpoint`, logout uses RP-initiated logout and returns to the post-logout redirect. For Shauth, that redirect hands off to Shauth's own `/oauth/logout/complete`.
- Otherwise ZZIRA ends its own session and shows `/signed-out`.

**Back-channel logout.** `POST /auth/{provider}/backchannel-logout` implements OpenID Connect Back-Channel Logout 1.0.
- The `logout_token` is checked for signature, issuer, audience, a `sid` or `sub`, the exact `http://schemas.openid.net/event/backchannel-logout` event, and freshness (issued within five minutes).
- Only OIDC sessions bound to that issuer's `sid`, or to its `(issuer, subject)`, are revoked. Password sessions and equal `sid`s from other issuers are kept.
- Each `jti` is accepted once; replays are refused.

## Related endpoints

- `GET /auth/validation`: with a session, 200 HTML with the member's provider username and email in `data-testid` elements (for providers' SSO validators); without one, 302 to `/signed-out`.
- `GET /monitoring/observation`: bearer-authenticated with `ZZIRA_MONITORING_TOKEN`; reports live database reachability and stored work item count as `e6qu.monitoring/v2`.

## References

- [OpenID Connect Core 1.0](https://openid.net/specs/openid-connect-core-1_0.html)
- [OpenID Connect Discovery 1.0](https://openid.net/specs/openid-connect-discovery-1_0.html)
- [OpenID Connect Back-Channel Logout 1.0](https://openid.net/specs/openid-connect-backchannel-1_0.html)
- [OpenID Connect RP-Initiated Logout 1.0](https://openid.net/specs/openid-connect-rpinitiated-1_0.html)
- [OAuth 2.0 Security Best Current Practice (RFC 9700)](https://www.rfc-editor.org/rfc/rfc9700.html)
- [Google OpenID Connect](https://developers.google.com/identity/openid-connect/openid-connect)
- [Microsoft identity platform OpenID Connect](https://learn.microsoft.com/en-us/entra/identity-platform/v2-protocols-oidc)
- [Atlassian OAuth 2.0 (3LO)](https://developer.atlassian.com/cloud/jira/platform/oauth-2-3lo-apps/)
