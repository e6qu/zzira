-- Jira lets an administrator name a field in each language a site speaks, and
-- shows each person the name in theirs. Until now a field had one name, and
-- the REST translations echoed it.
CREATE TABLE custom_field_translations (
  field_id     TEXT NOT NULL REFERENCES custom_fields(id) ON DELETE CASCADE,
  workspace_id TEXT REFERENCES workspaces(id) ON DELETE CASCADE,
  -- An IETF language tag, lower-cased: "es", "pt-br".
  locale      TEXT NOT NULL CHECK (locale ~ '^[a-z]{2,3}(-[a-z0-9]{2,8})?$'),
  name        TEXT NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 255),
  description TEXT NOT NULL DEFAULT '',
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (field_id, locale)
);
