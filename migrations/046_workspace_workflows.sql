ALTER TABLE workflows
  ADD COLUMN IF NOT EXISTS workspace_id TEXT REFERENCES workspaces(id) ON DELETE CASCADE;

UPDATE workflows w
SET workspace_id = COALESCE(
  (SELECT p.workspace_id FROM projects p WHERE p.workflow_id=w.id ORDER BY p.id LIMIT 1),
  (SELECT id FROM workspaces ORDER BY (id='ws_default') DESC,id LIMIT 1)
)
WHERE w.id<>'wf_default' AND w.workspace_id IS NULL;

ALTER TABLE workflows DROP CONSTRAINT IF EXISTS workflows_workspace_scope;
ALTER TABLE workflows ADD CONSTRAINT workflows_workspace_scope CHECK (
  (id='wf_default' AND workspace_id IS NULL) OR
  (id<>'wf_default' AND workspace_id IS NOT NULL)
);

