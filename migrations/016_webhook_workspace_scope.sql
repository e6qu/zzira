ALTER TABLE webhooks ADD COLUMN workspace_id TEXT REFERENCES workspaces(id);

UPDATE webhooks
SET workspace_id = (SELECT id FROM workspaces ORDER BY id LIMIT 1)
WHERE workspace_id IS NULL;

ALTER TABLE webhooks ALTER COLUMN workspace_id SET NOT NULL;
CREATE INDEX idx_webhooks_workspace ON webhooks (workspace_id, created_at);
