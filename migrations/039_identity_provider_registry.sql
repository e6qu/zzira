ALTER TABLE oidc_login_states
  ADD COLUMN provider_key TEXT NOT NULL DEFAULT 'shauth';

ALTER TABLE oidc_identities
  DROP CONSTRAINT IF EXISTS oidc_identities_user_id_key;

CREATE INDEX oidc_identities_user_id_idx ON oidc_identities(user_id);
