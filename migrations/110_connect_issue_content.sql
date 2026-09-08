CREATE TABLE app_issue_content_instances (
  workspace_id    TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  issue_id        TEXT NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
  installation_id TEXT NOT NULL REFERENCES app_installations(id) ON DELETE CASCADE,
  module_key      TEXT NOT NULL,
  created_by      TEXT REFERENCES users(id) ON DELETE SET NULL,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY(issue_id, installation_id, module_key)
);

CREATE INDEX app_issue_content_workspace_idx
  ON app_issue_content_instances(workspace_id, issue_id, created_at);
