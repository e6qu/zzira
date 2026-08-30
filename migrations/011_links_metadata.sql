CREATE TABLE IF NOT EXISTS issue_link_types (
  id          TEXT PRIMARY KEY,
  name        TEXT NOT NULL,
  inward      TEXT NOT NULL,
  outward     TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS issue_links (
  id          TEXT PRIMARY KEY,
  link_type_id TEXT NOT NULL REFERENCES issue_link_types(id),
  inward_id   TEXT NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
  outward_id  TEXT NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
  workspace_id TEXT NOT NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO issue_link_types (id, name, inward, outward) VALUES
  ('lt_blocks',    'Blocks',    'is blocked by', 'blocks'),
  ('lt_duplicates','Duplicates','is duplicated by', 'duplicates'),
  ('lt_relates',   'Relates',   'relates to', 'relates to')
ON CONFLICT (id) DO NOTHING;
