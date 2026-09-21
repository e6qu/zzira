-- SCIM 2.0 provisioning: an identity provider creates, updates and deactivates
-- the people and groups of one directory, and knows each of them by the id it
-- gave them. The people and groups themselves are the ones the rest of the
-- site already has; this records only what SCIM adds.
CREATE TABLE scim_users (
  directory_id UUID NOT NULL REFERENCES directories(id) ON DELETE CASCADE,
  user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  external_id  TEXT NOT NULL DEFAULT '',
  -- The name parts SCIM carries that a display name cannot hold on its own.
  given_name  TEXT NOT NULL DEFAULT '',
  family_name TEXT NOT NULL DEFAULT '',
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (directory_id, user_id)
);
CREATE UNIQUE INDEX scim_users_external ON scim_users (directory_id, external_id) WHERE external_id <> '';

CREATE TABLE scim_groups (
  group_id    UUID PRIMARY KEY REFERENCES groups(id) ON DELETE CASCADE,
  external_id TEXT NOT NULL DEFAULT '',
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
