CREATE TABLE software_delivery_facts (
  workspace_id           TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  fact_type              TEXT NOT NULL CHECK (fact_type IN ('build','deployment')),
  pipeline_id            TEXT NOT NULL,
  environment_id         TEXT NOT NULL DEFAULT '',
  entity_sequence_number BIGINT NOT NULL,
  update_sequence_number BIGINT NOT NULL,
  issue_keys             TEXT[] NOT NULL DEFAULT '{}',
  state                  TEXT NOT NULL,
  environment_type       TEXT NOT NULL DEFAULT '',
  occurred_at            TIMESTAMPTZ NOT NULL,
  payload                JSONB NOT NULL,
  recorded_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id,fact_type,pipeline_id,environment_id,entity_sequence_number,update_sequence_number)
);

CREATE INDEX idx_software_delivery_facts_timeline
  ON software_delivery_facts (workspace_id,fact_type,occurred_at DESC);
CREATE INDEX idx_software_delivery_facts_issue_keys
  ON software_delivery_facts USING GIN (issue_keys);

INSERT INTO software_delivery_facts(
  workspace_id,fact_type,pipeline_id,entity_sequence_number,update_sequence_number,
  issue_keys,state,occurred_at,payload)
SELECT workspace_id,'build',pipeline_id,build_number,update_sequence_number,
       issue_keys,state,last_updated,payload
FROM software_builds
ON CONFLICT DO NOTHING;

INSERT INTO software_delivery_facts(
  workspace_id,fact_type,pipeline_id,environment_id,entity_sequence_number,
  update_sequence_number,issue_keys,state,environment_type,occurred_at,payload)
SELECT workspace_id,'deployment',pipeline_id,environment_id,deployment_sequence_number,
       update_sequence_number,issue_keys,state,environment_type,last_updated,payload
FROM software_deployments
ON CONFLICT DO NOTHING;
