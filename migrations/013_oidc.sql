CREATE TABLE oidc_login_states (
  state_hash TEXT PRIMARY KEY,
  nonce TEXT NOT NULL,
  code_verifier TEXT NOT NULL,
  expires_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE oidc_identities (
  issuer TEXT NOT NULL,
  subject TEXT NOT NULL,
  user_id TEXT NOT NULL REFERENCES users(id),
  PRIMARY KEY (issuer, subject),
  UNIQUE (user_id)
);

ALTER TABLE sessions ADD COLUMN oidc_id_token TEXT;
