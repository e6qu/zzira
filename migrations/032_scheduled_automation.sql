ALTER TABLE workspaces
  ADD COLUMN cloud_id UUID NOT NULL DEFAULT gen_random_uuid();

CREATE UNIQUE INDEX workspaces_cloud_id ON workspaces (cloud_id);

CREATE TABLE automation_rules (
  uuid UUID PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  author_id TEXT NOT NULL REFERENCES users(id),
  actor_id TEXT NOT NULL REFERENCES users(id),
  name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 255),
  description TEXT NOT NULL DEFAULT '',
  labels TEXT[] NOT NULL DEFAULT '{}',
  state TEXT NOT NULL CHECK (state IN ('ENABLED', 'DISABLED')),
  rule_scope_aris TEXT[] NOT NULL DEFAULT '{}',
  payload JSONB NOT NULL,
  connections JSONB NOT NULL DEFAULT '[]',
  interval_minutes INTEGER CHECK (interval_minutes BETWEEN 1 AND 43200),
  schedule_timezone TEXT NOT NULL DEFAULT 'UTC',
  jql TEXT NOT NULL DEFAULT '',
  next_run_at TIMESTAMPTZ,
  consecutive_failures INTEGER NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, name)
);

CREATE INDEX automation_rules_due
  ON automation_rules (workspace_id, next_run_at)
  WHERE state = 'ENABLED' AND interval_minutes IS NOT NULL;

CREATE TABLE automation_runs (
  id UUID PRIMARY KEY,
  rule_uuid UUID NOT NULL REFERENCES automation_rules(uuid) ON DELETE CASCADE,
  scheduled_for TIMESTAMPTZ NOT NULL,
  state TEXT NOT NULL CHECK (state IN ('PENDING', 'RUNNING', 'SUCCESS', 'NO_ACTIONS', 'FAILED')),
  attempts INTEGER NOT NULL DEFAULT 0,
  available_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  claimed_at TIMESTAMPTZ,
  started_at TIMESTAMPTZ,
  completed_at TIMESTAMPTZ,
  matched_count INTEGER NOT NULL DEFAULT 0,
  changed_count INTEGER NOT NULL DEFAULT 0,
  detail TEXT NOT NULL DEFAULT '',
  UNIQUE (rule_uuid, scheduled_for)
);

CREATE INDEX automation_runs_claim
  ON automation_runs (state, available_at, scheduled_for);
