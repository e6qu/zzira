ALTER TABLE workflows
  ADD COLUMN project_id TEXT REFERENCES projects(id) ON DELETE CASCADE;

CREATE INDEX workflows_project
  ON workflows (project_id)
  WHERE project_id IS NOT NULL;

ALTER TABLE workflows ADD CONSTRAINT workflows_project_owner
  CHECK (project_id IS NULL OR workspace_id IS NOT NULL);
