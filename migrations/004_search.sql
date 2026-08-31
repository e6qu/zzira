ALTER TABLE issues ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ NOT NULL DEFAULT now();

CREATE TABLE IF NOT EXISTS filters (
  id          TEXT PRIMARY KEY,
  name        TEXT NOT NULL,
  jql         TEXT NOT NULL DEFAULT '',
  description TEXT NOT NULL DEFAULT '',
  owner_id    TEXT,
  favourite   BOOLEAN NOT NULL DEFAULT FALSE
);

INSERT INTO filters (id, name, jql, description, owner_id, favourite)
VALUES ('flt_all', 'All issues', '', 'Every issue you can see', NULL, TRUE)
ON CONFLICT (id) DO NOTHING;
