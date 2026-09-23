-- A site may sign people in through SAML as well as through OpenID Connect:
-- the provider says who it is, where it answers, and which certificates it
-- signs its assertions with.
CREATE TABLE saml_identity_providers (
  organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  provider_key    TEXT NOT NULL CHECK (provider_key ~ '^[a-z][a-z0-9-]{1,30}$'),
  display_name    TEXT NOT NULL CHECK (length(btrim(display_name)) BETWEEN 1 AND 80),
  entity_id       TEXT NOT NULL CHECK (length(btrim(entity_id)) BETWEEN 1 AND 2048),
  sso_url         TEXT NOT NULL CHECK (length(btrim(sso_url)) BETWEEN 8 AND 2048),
  certificates    TEXT NOT NULL CHECK (length(certificates) BETWEEN 1 AND 32768),
  email_attribute TEXT NOT NULL DEFAULT '' CHECK (length(email_attribute) <= 255),
  name_attribute  TEXT NOT NULL DEFAULT '' CHECK (length(name_attribute) <= 255),
  enabled         BOOLEAN NOT NULL DEFAULT TRUE,
  created_by      TEXT REFERENCES users(id) ON DELETE SET NULL,
  updated_by      TEXT REFERENCES users(id) ON DELETE SET NULL,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (organization_id, provider_key),
  UNIQUE (organization_id, entity_id)
);

-- A sign-in this site started: the request the answer must name, and where
-- the person was going. A row is used once and then gone, so an answer
-- cannot be replayed.
CREATE TABLE saml_sign_in_requests (
  request_id      TEXT PRIMARY KEY CHECK (length(request_id) BETWEEN 8 AND 128),
  organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  provider_key    TEXT NOT NULL,
  relay_state     TEXT NOT NULL DEFAULT '' CHECK (length(relay_state) <= 512),
  link_user_id    TEXT REFERENCES users(id) ON DELETE CASCADE,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX saml_sign_in_requests_created ON saml_sign_in_requests(created_at);

-- An assertion is read once. Its id is kept until it could no longer be
-- within its own validity, so the same answer cannot be posted twice.
CREATE TABLE saml_seen_assertions (
  assertion_id TEXT PRIMARY KEY CHECK (length(assertion_id) BETWEEN 1 AND 256),
  seen_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at   TIMESTAMPTZ NOT NULL
);

CREATE INDEX saml_seen_assertions_expiry ON saml_seen_assertions(expires_at);
