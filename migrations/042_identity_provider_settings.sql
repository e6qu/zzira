CREATE TABLE identity_provider_settings (
  organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  provider_key TEXT NOT NULL CHECK (provider_key IN ('shauth','google','microsoft','atlassian')),
  enabled BOOLEAN NOT NULL DEFAULT TRUE,
  updated_by TEXT REFERENCES users(id) ON DELETE SET NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (organization_id, provider_key)
);
