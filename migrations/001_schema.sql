CREATE TABLE users (
  id            TEXT PRIMARY KEY,
  email         TEXT UNIQUE NOT NULL,
  password_hash TEXT NOT NULL,
  display_name  TEXT NOT NULL,
  time_zone     TEXT NOT NULL DEFAULT 'UTC',
  active        BOOLEAN NOT NULL DEFAULT TRUE,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE sessions (
  token_hash TEXT PRIMARY KEY,
  user_id    TEXT NOT NULL REFERENCES users(id),
  expires_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE api_tokens (
  id         TEXT PRIMARY KEY,
  user_id    TEXT NOT NULL REFERENCES users(id),
  token_hash TEXT NOT NULL UNIQUE,
  label      TEXT NOT NULL DEFAULT '',
  expires_at TIMESTAMPTZ
);

CREATE TABLE workspaces (
  id   TEXT PRIMARY KEY,
  slug TEXT UNIQUE NOT NULL,
  name TEXT NOT NULL,
  seq  BIGINT NOT NULL DEFAULT 0
);

CREATE TABLE memberships (
  workspace_id TEXT NOT NULL REFERENCES workspaces(id),
  user_id      TEXT NOT NULL REFERENCES users(id),
  role         TEXT NOT NULL,
  PRIMARY KEY (workspace_id, user_id)
);

CREATE TABLE actions (
  workspace_id TEXT NOT NULL,
  seq          BIGINT NOT NULL,
  entity_type  TEXT NOT NULL,
  entity_id    TEXT NOT NULL,
  op           TEXT NOT NULL,
  schema_v     INT  NOT NULL,
  payload      JSONB NOT NULL,
  actor_id     TEXT NOT NULL,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, seq)
);
CREATE INDEX idx_actions_entity ON actions (workspace_id, entity_type, entity_id, seq DESC);

CREATE TABLE statuses (
  id       TEXT PRIMARY KEY,
  name     TEXT NOT NULL,
  category TEXT NOT NULL
);

CREATE TABLE issue_types (
  id      TEXT PRIMARY KEY,
  name    TEXT NOT NULL,
  icon    TEXT NOT NULL DEFAULT '',
  subtask BOOLEAN NOT NULL DEFAULT FALSE
);

CREATE TABLE priorities (
  id       TEXT PRIMARY KEY,
  name     TEXT NOT NULL,
  icon_url TEXT NOT NULL DEFAULT ''
);

CREATE TABLE projects (
  id           TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id),
  key          TEXT NOT NULL,
  name         TEXT NOT NULL,
  issue_seq    BIGINT NOT NULL DEFAULT 0,
  UNIQUE (workspace_id, key)
);

CREATE TABLE issues (
  id           TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  project_id   TEXT NOT NULL REFERENCES projects(id),
  key          TEXT NOT NULL,
  summary      TEXT NOT NULL,
  description  JSONB NOT NULL DEFAULT '{"type":"doc","version":1,"content":[]}',
  status_id    TEXT NOT NULL REFERENCES statuses(id),
  issuetype_id TEXT NOT NULL REFERENCES issue_types(id),
  priority_id  TEXT REFERENCES priorities(id),
  assignee_id  TEXT,
  reporter_id  TEXT,
  updated_seq  BIGINT NOT NULL,
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, key)
);
CREATE INDEX idx_issues_project ON issues (project_id, updated_seq DESC);
