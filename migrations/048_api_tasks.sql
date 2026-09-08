CREATE TABLE api_tasks (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  submitted_by TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('ENQUEUED','RUNNING','COMPLETE','FAILED','CANCELLED')),
  progress INTEGER NOT NULL CHECK (progress BETWEEN 0 AND 100),
  message TEXT NOT NULL DEFAULT '',
  result JSONB,
  submitted_at TIMESTAMPTZ NOT NULL,
  started_at TIMESTAMPTZ,
  last_update_at TIMESTAMPTZ NOT NULL,
  finished_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX api_tasks_workspace_created ON api_tasks(workspace_id,created_at DESC,id);
