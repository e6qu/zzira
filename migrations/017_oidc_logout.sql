-- Back-channel logout replay protection: a logout_token's jti is claimed
-- exactly once, in the same transaction as the session revocation it
-- authorizes, so a replayed token cannot revoke sessions a second time (or
-- succeed after its subject signed back in).
CREATE TABLE oidc_logout_tokens (
  jti TEXT PRIMARY KEY,
  expires_at TIMESTAMPTZ NOT NULL
);
