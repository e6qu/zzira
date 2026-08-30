CREATE TABLE comments (
  id          TEXT PRIMARY KEY,
  issue_id    TEXT NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
  workspace_id TEXT NOT NULL,
  author_id   TEXT NOT NULL,
  body        JSONB NOT NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_seq BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX idx_comments_issue ON comments (issue_id, created_at);
