CREATE SEQUENCE jira_app_installation_id START 1;
CREATE TABLE app_installations (
  id                TEXT PRIMARY KEY DEFAULT nextval('jira_app_installation_id')::text,
  workspace_id      TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  app_key           TEXT NOT NULL,
  name              TEXT NOT NULL,
  base_url          TEXT NOT NULL,
  version           TEXT NOT NULL,
  status            TEXT NOT NULL DEFAULT 'active' CHECK(status IN ('active','suspended','uninstalled')),
  secret_ciphertext BYTEA NOT NULL,
  descriptor        JSONB NOT NULL,
  installed_by      TEXT REFERENCES users(id) ON DELETE SET NULL,
  installed_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE(workspace_id,app_key)
);

CREATE TABLE app_scopes (
  installation_id TEXT NOT NULL REFERENCES app_installations(id) ON DELETE CASCADE,
  scope           TEXT NOT NULL,
  PRIMARY KEY(installation_id,scope)
);

CREATE SEQUENCE jira_app_module_id START 1;
CREATE TABLE app_modules (
  id              TEXT PRIMARY KEY DEFAULT nextval('jira_app_module_id')::text,
  installation_id TEXT NOT NULL REFERENCES app_installations(id) ON DELETE CASCADE,
  module_key      TEXT NOT NULL,
  module_type     TEXT NOT NULL,
  location        TEXT NOT NULL,
  title           TEXT NOT NULL,
  body            TEXT NOT NULL,
  position        INTEGER NOT NULL DEFAULT 0,
  UNIQUE(installation_id,module_key)
);
CREATE INDEX app_modules_location ON app_modules(location,position,id);

CREATE TABLE app_storage (
  installation_id TEXT NOT NULL REFERENCES app_installations(id) ON DELETE CASCADE,
  key             TEXT NOT NULL,
  value           JSONB NOT NULL,
  version         BIGINT NOT NULL DEFAULT 1,
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY(installation_id,key)
);

CREATE SEQUENCE jira_app_lifecycle_event_id START 1;
CREATE TABLE app_lifecycle_events (
  id              TEXT PRIMARY KEY DEFAULT nextval('jira_app_lifecycle_event_id')::text,
  installation_id TEXT NOT NULL REFERENCES app_installations(id) ON DELETE CASCADE,
  event           TEXT NOT NULL,
  payload         JSONB NOT NULL DEFAULT '{}',
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX app_lifecycle_events_installation ON app_lifecycle_events(installation_id,created_at DESC,id DESC);

CREATE TABLE app_signed_requests (
  installation_id TEXT NOT NULL REFERENCES app_installations(id) ON DELETE CASCADE,
  request_id      TEXT NOT NULL,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY(installation_id,request_id)
);
CREATE INDEX app_signed_requests_expiry ON app_signed_requests(created_at);
