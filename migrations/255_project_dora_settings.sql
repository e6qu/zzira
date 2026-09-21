-- Which deployments count toward a project's DORA metrics. Jira lets a
-- manager choose the environments and pipelines that represent production;
-- until now every project read environment type 'production' and every
-- pipeline, and no one could say otherwise. No row means that same default,
-- so existing projects keep the numbers they had.
CREATE TABLE project_dora_settings (
  project_id        TEXT PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
  workspace_id      TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  environment_types TEXT[] NOT NULL CHECK (
    cardinality(environment_types) > 0
    AND environment_types <@ ARRAY['production','staging','testing','development','unmapped']
  ),
  -- An empty list is every pipeline, which is what a project starts with.
  pipeline_ids TEXT[] NOT NULL DEFAULT '{}',
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX project_dora_settings_workspace ON project_dora_settings (workspace_id);
