-- Jira app data and platform operations: app properties, UI modifications,
-- dynamic app webhooks, workflow version history and project data
-- classification.

-- Jira app properties, kept per installation apart from Confluence's.
CREATE TABLE jira_app_properties (
  installation_id TEXT NOT NULL REFERENCES app_installations(id) ON DELETE CASCADE,
  key             TEXT NOT NULL CHECK (char_length(key) BETWEEN 1 AND 127),
  value           JSONB NOT NULL,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (installation_id, key)
);

-- UI modifications an app defines, with the contexts they apply to.
CREATE TABLE ui_modifications (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id    TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  installation_id TEXT NOT NULL REFERENCES app_installations(id) ON DELETE CASCADE,
  name            TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 255),
  description     TEXT NOT NULL DEFAULT '',
  data            TEXT,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX ui_modifications_installation ON ui_modifications(installation_id, created_at, id);

CREATE TABLE ui_modification_contexts (
  id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  ui_modification_id UUID NOT NULL REFERENCES ui_modifications(id) ON DELETE CASCADE,
  position           INTEGER NOT NULL,
  project_id         TEXT,
  issue_type_id      TEXT,
  portal_id          TEXT,
  request_type_id    TEXT,
  view_type          TEXT CHECK (view_type IS NULL OR view_type IN ('GIC','IssueView','IssueTransition','JSMRequestCreate','GICAgentView','IssueViewAgentView','IssueTransitionAgentView'))
);

CREATE INDEX ui_modification_contexts_modification ON ui_modification_contexts(ui_modification_id, position);

-- Dynamic webhooks belong to the app that registered them, carry Jira's
-- numeric id, expire after 30 days and filter by fields and issue properties.
CREATE SEQUENCE jira_webhook_id START 10000;
ALTER TABLE webhooks
  ADD COLUMN jira_id BIGINT NOT NULL DEFAULT nextval('jira_webhook_id'),
  ADD COLUMN installation_id TEXT REFERENCES app_installations(id) ON DELETE CASCADE,
  ADD COLUMN expires_at TIMESTAMPTZ,
  ADD COLUMN field_ids_filter TEXT[],
  ADD COLUMN issue_property_keys_filter TEXT[],
  ADD COLUMN name TEXT NOT NULL DEFAULT '',
  ADD COLUMN exclude_body BOOLEAN NOT NULL DEFAULT FALSE,
  ADD COLUMN updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  ADD COLUMN updated_by TEXT;
ALTER SEQUENCE jira_webhook_id OWNED BY webhooks.jira_id;
CREATE UNIQUE INDEX webhooks_jira_id ON webhooks(jira_id);
CREATE INDEX webhooks_installation ON webhooks(installation_id, jira_id) WHERE installation_id IS NOT NULL;

-- A delivery that exhausts its retries is abandoned and kept for 72 hours as
-- a failed webhook.
ALTER TABLE webhook_deliveries
  ADD COLUMN failed_at TIMESTAMPTZ,
  ADD COLUMN body TEXT;
ALTER TABLE webhook_deliveries DROP CONSTRAINT IF EXISTS webhook_deliveries_state_check;
CREATE INDEX webhook_deliveries_abandoned ON webhook_deliveries(webhook_id, failed_at) WHERE state = 'abandoned';

-- Every published workflow version, for Jira's workflow history.
CREATE TABLE workflow_versions (
  workflow_id  TEXT NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
  version      INTEGER NOT NULL,
  def          JSONB NOT NULL,
  author_id    TEXT,
  intermediate BOOLEAN NOT NULL DEFAULT FALSE,
  written_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (workflow_id, version)
);

INSERT INTO workflow_versions(workflow_id, version, def, written_at)
SELECT id, version, def, published_at FROM workflows
ON CONFLICT DO NOTHING;

-- A project's default data classification level.
ALTER TABLE projects ADD COLUMN default_classification_level TEXT;
