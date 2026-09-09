CREATE TABLE issue_key_aliases (
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  issue_id     TEXT NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
  key          TEXT NOT NULL,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id,key),
  UNIQUE (workspace_id,issue_id,key)
);

CREATE INDEX idx_issue_key_aliases_issue
  ON issue_key_aliases (workspace_id,issue_id);

CREATE TABLE bulk_issue_task_items (
  task_id      TEXT NOT NULL REFERENCES api_tasks(id) ON DELETE CASCADE,
  issue_id     TEXT NOT NULL,
  result       JSONB NOT NULL,
  completed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (task_id,issue_id)
);
