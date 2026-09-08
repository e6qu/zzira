ALTER TABLE identity_provider_settings
  DROP CONSTRAINT IF EXISTS identity_provider_settings_provider_key_check;

CREATE TABLE identity_provider_registrations (
  organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  provider_key TEXT NOT NULL CHECK (provider_key ~ '^[a-z][a-z0-9-]{1,30}$'),
  display_name TEXT NOT NULL CHECK (length(btrim(display_name)) BETWEEN 1 AND 80),
  issuer TEXT NOT NULL CHECK (length(issuer) BETWEEN 8 AND 2048),
  client_id TEXT NOT NULL CHECK (length(client_id) BETWEEN 1 AND 2048),
  secret_ciphertext BYTEA NOT NULL CHECK (octet_length(secret_ciphertext) >= 30),
  created_by TEXT REFERENCES users(id) ON DELETE SET NULL,
  updated_by TEXT REFERENCES users(id) ON DELETE SET NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (organization_id, provider_key),
  UNIQUE (organization_id, issuer)
);
