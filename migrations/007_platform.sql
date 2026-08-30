ALTER TABLE issues ADD COLUMN IF NOT EXISTS security_level_id TEXT;

CREATE TABLE security_schemes (
  id         TEXT PRIMARY KEY,
  name       TEXT NOT NULL,
  levels     JSONB NOT NULL, -- [{id, name, members: [accountId]}]
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE projects ADD COLUMN IF NOT EXISTS security_scheme_id TEXT REFERENCES security_schemes(id);

CREATE TABLE custom_fields (
  id          TEXT PRIMARY KEY, -- customfield_NNNNN
  name        TEXT NOT NULL,
  type        TEXT NOT NULL,    -- text | number | datetime
  description TEXT NOT NULL DEFAULT '',
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE field_contexts (
  field_id   TEXT NOT NULL REFERENCES custom_fields(id) ON DELETE CASCADE,
  project_id TEXT,            -- NULL = global context
  PRIMARY KEY (field_id, project_id)
);

CREATE TABLE webhooks (
  id         TEXT PRIMARY KEY,
  url        TEXT NOT NULL,
  events     TEXT[] NOT NULL, -- jira:issue_created, jira:issue_updated, ...
  jql        TEXT NOT NULL DEFAULT '',
  active     BOOLEAN NOT NULL DEFAULT TRUE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE webhook_deliveries (
  webhook_id TEXT NOT NULL,
  seq        BIGINT NOT NULL,
  state      TEXT NOT NULL DEFAULT 'pending', -- pending | delivered | failed
  attempts   INT NOT NULL DEFAULT 0,
  last_error TEXT NOT NULL DEFAULT '',
  claimed_at TIMESTAMPTZ,
  PRIMARY KEY (webhook_id, seq)
);
