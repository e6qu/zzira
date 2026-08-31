ALTER TABLE filters ADD COLUMN workspace_id TEXT REFERENCES workspaces(id);

UPDATE filters
SET workspace_id = (SELECT id FROM workspaces ORDER BY id LIMIT 1)
WHERE workspace_id IS NULL;

ALTER TABLE filters ALTER COLUMN workspace_id SET NOT NULL;
CREATE INDEX idx_filters_workspace ON filters (workspace_id, name);

CREATE TABLE filter_favourites (
  filter_id TEXT NOT NULL REFERENCES filters(id) ON DELETE CASCADE,
  user_id   TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  PRIMARY KEY (filter_id, user_id)
);
