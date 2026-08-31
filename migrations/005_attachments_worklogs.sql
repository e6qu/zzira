CREATE TABLE attachments (
  id          TEXT PRIMARY KEY,
  issue_id    TEXT NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
  workspace_id TEXT NOT NULL,
  filename    TEXT NOT NULL,
  mime_type   TEXT NOT NULL DEFAULT 'application/octet-stream',
  size        BIGINT NOT NULL,
  blob_ref    TEXT NOT NULL,
  author_id   TEXT NOT NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_attachments_issue ON attachments (issue_id);

CREATE TABLE worklogs (
  id          TEXT PRIMARY KEY,
  issue_id    TEXT NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
  workspace_id TEXT NOT NULL,
  author_id   TEXT NOT NULL,
  comment     JSONB,
  time_spent_seconds INT NOT NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_worklogs_issue ON worklogs (issue_id);
