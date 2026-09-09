CREATE SEQUENCE jira_component_id START 10000;

CREATE TABLE project_components (
  id              TEXT PRIMARY KEY DEFAULT nextval('jira_component_id')::text,
  project_id      TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  name            TEXT NOT NULL,
  description     TEXT NOT NULL DEFAULT '',
  lead_account_id TEXT REFERENCES users(id) ON DELETE SET NULL,
  assignee_type   TEXT NOT NULL DEFAULT 'PROJECT_DEFAULT'
                  CHECK (assignee_type IN ('PROJECT_DEFAULT','COMPONENT_LEAD','PROJECT_LEAD','UNASSIGNED')),
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX project_components_project_name
  ON project_components(project_id,lower(name));
CREATE INDEX project_components_project ON project_components(project_id,name,id);
