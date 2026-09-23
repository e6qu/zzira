-- A security key or passkey an account answers a sign-in with, beside the
-- authenticator app the two-step verification already offers.
CREATE TABLE webauthn_credentials (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id       TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  credential_id BYTEA NOT NULL UNIQUE CHECK (octet_length(credential_id) BETWEEN 1 AND 1023),
  public_key    BYTEA NOT NULL CHECK (octet_length(public_key) BETWEEN 1 AND 8192),
  label         TEXT NOT NULL DEFAULT '' CHECK (length(label) <= 80),
  sign_count    BIGINT NOT NULL DEFAULT 0 CHECK (sign_count >= 0),
  user_verified BOOLEAN NOT NULL DEFAULT FALSE,
  aaguid        BYTEA,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_used_at  TIMESTAMPTZ
);

CREATE INDEX webauthn_credentials_user ON webauthn_credentials(user_id, created_at);

-- The challenge a ceremony must answer: issued here, read once, and gone
-- either way.
CREATE TABLE webauthn_challenges (
  challenge  BYTEA PRIMARY KEY CHECK (octet_length(challenge) BETWEEN 16 AND 128),
  user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  purpose    TEXT NOT NULL CHECK (purpose IN ('register','sign-in')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX webauthn_challenges_expiry ON webauthn_challenges(expires_at);
