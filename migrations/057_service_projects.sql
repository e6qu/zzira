ALTER TABLE projects
  ADD COLUMN project_type_key TEXT NOT NULL DEFAULT 'software'
  CHECK (project_type_key IN ('software','service_desk'));

CREATE SEQUENCE jira_service_desk_id START 1;
CREATE TABLE service_desks (
  id           TEXT PRIMARY KEY DEFAULT nextval('jira_service_desk_id')::text,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  project_id   TEXT NOT NULL UNIQUE REFERENCES projects(id) ON DELETE CASCADE,
  portal_name  TEXT NOT NULL,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_service_desks_workspace ON service_desks(workspace_id,id);

CREATE SEQUENCE jira_request_type_id START 1;
CREATE TABLE service_request_types (
  id              TEXT PRIMARY KEY DEFAULT nextval('jira_request_type_id')::text,
  service_desk_id TEXT NOT NULL REFERENCES service_desks(id) ON DELETE CASCADE,
  name            TEXT NOT NULL,
  description     TEXT NOT NULL DEFAULT '',
  help_text       TEXT NOT NULL DEFAULT '',
  issue_type_id   TEXT NOT NULL REFERENCES issue_types(id),
  group_ids       TEXT[] NOT NULL DEFAULT '{}',
  fields          JSONB NOT NULL DEFAULT '[]'::jsonb,
  properties      JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE(service_desk_id,name)
);
CREATE INDEX idx_service_request_types_desk ON service_request_types(service_desk_id,id);
