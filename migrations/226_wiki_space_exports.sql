-- A space administrator's HTML export of a space, kept for the person who
-- asked for it.
CREATE TABLE wiki_space_exports (
  task_id      TEXT PRIMARY KEY REFERENCES api_tasks(id) ON DELETE CASCADE,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  space_id     TEXT NOT NULL,
  requested_by TEXT NOT NULL,
  content      BYTEA NOT NULL,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
