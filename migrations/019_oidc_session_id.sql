-- Ory Hydra's back-channel logout token carries a sid claim, not sub: its
-- documented example logout token has no sub field at all, and this
-- deployment's real tokens confirmed it. ClaimOIDCLogoutAndDeleteSessions
-- correlated only by (issuer, subject), so every real back-channel logout
-- request found no subject to match and revoked nothing -- a session
-- survived global sign-out until its 30-day expiry. Recording the sid an
-- OIDC session was issued under lets revocation match what Hydra actually
-- sends.
ALTER TABLE sessions ADD COLUMN oidc_session_id TEXT;

CREATE INDEX sessions_oidc_session_id_idx ON sessions(oidc_session_id) WHERE oidc_session_id IS NOT NULL;
