CREATE SEQUENCE jira_project_category_id START 10000;

CREATE TABLE project_categories (
  id           TEXT PRIMARY KEY DEFAULT nextval('jira_project_category_id')::text,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  name         TEXT NOT NULL CHECK (char_length(btrim(name)) BETWEEN 1 AND 255),
  description  TEXT NOT NULL DEFAULT '',
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX project_categories_workspace_name_unique
  ON project_categories (workspace_id, lower(name));

ALTER TABLE projects
  ADD COLUMN category_id TEXT REFERENCES project_categories(id) ON DELETE SET NULL,
  ADD COLUMN sender_email TEXT NOT NULL DEFAULT '';

CREATE INDEX projects_category_idx ON projects (category_id);

CREATE TABLE project_properties (
  project_id   TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  property_key TEXT NOT NULL CHECK (char_length(property_key) BETWEEN 1 AND 255),
  value        JSONB NOT NULL,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (project_id, property_key)
);

CREATE TABLE project_features (
  project_id  TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  feature_key TEXT NOT NULL,
  state       TEXT NOT NULL CHECK (state IN ('ENABLED', 'DISABLED')),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (project_id, feature_key)
);
