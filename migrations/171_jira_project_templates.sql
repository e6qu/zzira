-- Custom project templates saved from a project: a LIVE template follows its
-- project's configuration, a SNAPSHOT keeps the configuration it was saved
-- with.
CREATE TABLE project_templates (
  uuid               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id       TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  template_key       TEXT NOT NULL,
  name               TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 50),
  description        TEXT NOT NULL DEFAULT '' CHECK (char_length(description) <= 150),
  type               TEXT NOT NULL CHECK (type IN ('LIVE', 'SNAPSHOT')),
  project_id         TEXT REFERENCES projects(id) ON DELETE CASCADE,
  snapshot           JSONB NOT NULL,
  generation_options JSONB NOT NULL DEFAULT '{}',
  created_by         TEXT NOT NULL,
  created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, template_key)
);

CREATE UNIQUE INDEX project_templates_name ON project_templates(workspace_id, lower(name));
CREATE INDEX project_templates_project ON project_templates(project_id) WHERE project_id IS NOT NULL;
