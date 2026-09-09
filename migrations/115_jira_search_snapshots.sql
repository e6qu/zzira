CREATE TABLE jira_search_snapshots (
  id           TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  query_hash   TEXT NOT NULL,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at   TIMESTAMPTZ NOT NULL
);

CREATE TABLE jira_search_snapshot_items (
  snapshot_id TEXT NOT NULL REFERENCES jira_search_snapshots(id) ON DELETE CASCADE,
  position    BIGINT NOT NULL,
  issue_id    TEXT NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
  PRIMARY KEY (snapshot_id, position),
  UNIQUE (snapshot_id, issue_id)
);

CREATE INDEX idx_jira_search_snapshots_expiry
  ON jira_search_snapshots (expires_at);
