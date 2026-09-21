-- Two-step verification: the shared secret an authenticator app holds, the
-- recovery codes that stand in for it when the phone is gone, and the
-- half-finished sign-in that waits for a code. The secret is sealed with the
-- site's credential encryption key; recovery codes are kept as hashes, like
-- every other credential here.
CREATE TABLE user_two_step (
  user_id      TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  secret       BYTEA NOT NULL,
  confirmed_at TIMESTAMPTZ,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE user_recovery_codes (
  user_id   TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  code_hash TEXT NOT NULL,
  used_at   TIMESTAMPTZ,
  PRIMARY KEY (user_id, code_hash)
);

-- A sign-in that has passed the password and is waiting for a code. It is not
-- a session: it carries no access at all, and it expires in minutes.
CREATE TABLE sign_in_challenges (
  token_hash TEXT PRIMARY KEY,
  user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  attempts   INTEGER NOT NULL DEFAULT 0,
  expires_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
