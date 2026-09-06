ALTER TABLE oidc_login_states
  ADD COLUMN link_user_id TEXT REFERENCES users(id) ON DELETE CASCADE;

ALTER TABLE oidc_identities
  ADD COLUMN created_at TIMESTAMPTZ NOT NULL DEFAULT now();
