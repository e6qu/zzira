-- A site names its work types, priorities, resolutions and statuses once, and
-- Jira lets an administrator name each of them in every language the site
-- speaks. Custom fields have had this since 257; the built-in metadata a work
-- item carries had one name for everybody.
--
-- The entity id is the site's id for the thing being named: an issue type id,
-- a priority id, a resolution id or a status id. They are kept in one table
-- because they are one feature -- an administrator translates the words on a
-- work item -- and because a site's metadata ids do not collide across kinds
-- once the kind is part of the key.
CREATE TABLE issue_metadata_translations (
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  entity_type  TEXT NOT NULL CHECK (entity_type IN ('issuetype','priority','resolution','status')),
  entity_id    TEXT NOT NULL CHECK (length(entity_id) BETWEEN 1 AND 255),
  -- An IETF language tag, lower-cased: "es", "pt-br".
  locale      TEXT NOT NULL CHECK (locale ~ '^[a-z]{2,3}(-[a-z0-9]{2,8})?$'),
  name        TEXT NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 255),
  description TEXT NOT NULL DEFAULT '' CHECK (length(description) <= 1000),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, entity_type, entity_id, locale)
);
