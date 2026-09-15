-- A service desk connects a deployment provider and gates the environment
-- types it chooses: each deployment to one opens a change request whose
-- approval decides the deployment's gating status.
CREATE TABLE service_deployment_gates (
  service_desk_id   TEXT PRIMARY KEY REFERENCES service_desks(id) ON DELETE CASCADE,
  provider_key      TEXT NOT NULL,
  environment_types TEXT[] NOT NULL,
  updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The change request gating each deployment; no request means gating failed.
CREATE TABLE software_deployment_gatings (
  workspace_id               TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  pipeline_id                TEXT NOT NULL,
  environment_id             TEXT NOT NULL,
  deployment_sequence_number BIGINT NOT NULL,
  issue_id                   TEXT REFERENCES issues(id) ON DELETE CASCADE,
  created_at                 TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, pipeline_id, environment_id, deployment_sequence_number)
);
