CREATE TABLE software_builds (
  workspace_id           TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  pipeline_id            TEXT NOT NULL,
  build_number           BIGINT NOT NULL,
  update_sequence_number BIGINT NOT NULL,
  issue_keys             TEXT[] NOT NULL DEFAULT '{}',
  display_name           TEXT NOT NULL,
  url                    TEXT NOT NULL,
  state                  TEXT NOT NULL,
  last_updated           TIMESTAMPTZ NOT NULL,
  properties             JSONB NOT NULL DEFAULT '{}'::jsonb,
  payload                JSONB NOT NULL,
  updated_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, pipeline_id, build_number)
);

CREATE INDEX idx_software_builds_issue_keys
  ON software_builds USING GIN (issue_keys);
CREATE INDEX idx_software_builds_timeline
  ON software_builds (workspace_id, last_updated DESC, pipeline_id, build_number DESC);

CREATE TABLE software_deployments (
  workspace_id              TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  pipeline_id               TEXT NOT NULL,
  environment_id            TEXT NOT NULL,
  deployment_sequence_number BIGINT NOT NULL,
  update_sequence_number    BIGINT NOT NULL,
  issue_keys                TEXT[] NOT NULL DEFAULT '{}',
  display_name              TEXT NOT NULL,
  url                       TEXT NOT NULL,
  state                     TEXT NOT NULL,
  environment_name          TEXT NOT NULL,
  environment_type          TEXT NOT NULL,
  last_updated              TIMESTAMPTZ NOT NULL,
  properties                JSONB NOT NULL DEFAULT '{}'::jsonb,
  payload                   JSONB NOT NULL,
  updated_at                TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, pipeline_id, environment_id, deployment_sequence_number)
);

CREATE INDEX idx_software_deployments_issue_keys
  ON software_deployments USING GIN (issue_keys);
CREATE INDEX idx_software_deployments_timeline
  ON software_deployments (workspace_id, last_updated DESC, pipeline_id, environment_id, deployment_sequence_number DESC);
