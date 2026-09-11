-- Jira reports worklogs updated or deleted since a timestamp, which needs an
-- update stamp and a tombstone for the ones that are gone.
ALTER TABLE worklogs ADD COLUMN updated_at TIMESTAMPTZ NOT NULL DEFAULT now();
UPDATE worklogs SET updated_at = created_at;
CREATE INDEX worklogs_workspace_updated ON worklogs(workspace_id, updated_at, id);

CREATE TABLE deleted_worklogs (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  issue_id TEXT NOT NULL,
  author_id TEXT NOT NULL,
  deleted_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX deleted_worklogs_workspace_deleted ON deleted_worklogs(workspace_id, deleted_at, id);
