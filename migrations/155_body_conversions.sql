-- Confluence converts a content body from one format to another in the
-- background and keeps the result for five minutes at a result endpoint.
CREATE TABLE wiki_body_conversions (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  status TEXT NOT NULL CHECK (status IN ('QUEUED','WORKING','COMPLETED','FAILED','RERUNNING')),
  representation TEXT NOT NULL,
  value TEXT NOT NULL DEFAULT '',
  error TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  completed_at TIMESTAMPTZ
);
CREATE INDEX wiki_body_conversions_workspace ON wiki_body_conversions(workspace_id, created_at DESC);
