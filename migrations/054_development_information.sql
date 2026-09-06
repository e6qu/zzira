CREATE TABLE development_repositories (
  workspace_id       TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  repository_id      TEXT NOT NULL,
  update_sequence_id BIGINT NOT NULL,
  name                TEXT NOT NULL DEFAULT '',
  url                 TEXT NOT NULL DEFAULT '',
  properties          JSONB NOT NULL DEFAULT '{}'::jsonb,
  payload             JSONB NOT NULL,
  updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, repository_id)
);

CREATE TABLE development_entities (
  workspace_id       TEXT NOT NULL,
  repository_id      TEXT NOT NULL,
  entity_type        TEXT NOT NULL CHECK (entity_type IN ('commit','branch','pullrequest')),
  entity_id          TEXT NOT NULL,
  update_sequence_id BIGINT NOT NULL,
  issue_keys         TEXT[] NOT NULL DEFAULT '{}',
  name                TEXT NOT NULL DEFAULT '',
  url                 TEXT NOT NULL DEFAULT '',
  status              TEXT NOT NULL DEFAULT '',
  occurred_at         TIMESTAMPTZ,
  payload             JSONB NOT NULL,
  updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, repository_id, entity_type, entity_id),
  FOREIGN KEY (workspace_id, repository_id)
    REFERENCES development_repositories(workspace_id, repository_id) ON DELETE CASCADE
);

CREATE INDEX idx_development_entities_issue_keys
  ON development_entities USING GIN (issue_keys);
CREATE INDEX idx_development_entities_issue_time
  ON development_entities (workspace_id, entity_type, occurred_at DESC, entity_id);
