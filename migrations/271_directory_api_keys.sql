-- A provisioning provider holds a key of its own. Until now SCIM ran as
-- whichever administrator's personal API token the provider was given, so
-- revoking that person's token stopped provisioning, and a key handed to
-- Okta or Entra could read and write everything that person could.
--
-- A key belongs to one directory: it provisions that directory and nothing
-- else. Only its hash is kept, so a key that is lost is replaced rather than
-- recovered, like every other token this site issues.
CREATE TABLE directory_api_keys (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  directory_id UUID NOT NULL REFERENCES directories(id) ON DELETE CASCADE,
  name         TEXT NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 255),
  token_hash   TEXT NOT NULL UNIQUE,
  created_by   TEXT REFERENCES users(id) ON DELETE SET NULL,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_used_at TIMESTAMPTZ,
  revoked_at   TIMESTAMPTZ
);
CREATE INDEX directory_api_keys_directory ON directory_api_keys (directory_id, created_at DESC);
