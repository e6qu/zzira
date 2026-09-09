CREATE TABLE attachment_blob_deletions (
  blob_ref       TEXT PRIMARY KEY,
  workspace_id   TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  issue_id       TEXT NOT NULL,
  attempts       INTEGER NOT NULL DEFAULT 0,
  available_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  leased_until   TIMESTAMPTZ,
  last_error     TEXT NOT NULL DEFAULT '',
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  completed_at   TIMESTAMPTZ
);

CREATE INDEX idx_attachment_blob_deletions_ready
  ON attachment_blob_deletions (available_at, created_at)
  WHERE completed_at IS NULL;
