ALTER TABLE statuses
  ADD COLUMN project_id TEXT REFERENCES projects(id) ON DELETE CASCADE;

DROP INDEX IF EXISTS statuses_workspace_name;

CREATE UNIQUE INDEX statuses_global_name
  ON statuses (workspace_id, lower(name))
  WHERE workspace_id IS NOT NULL AND project_id IS NULL;

CREATE UNIQUE INDEX statuses_project_name
  ON statuses (project_id, lower(name))
  WHERE project_id IS NOT NULL;

ALTER TABLE statuses ADD CONSTRAINT statuses_project_owner
  CHECK (project_id IS NULL OR workspace_id IS NOT NULL);
