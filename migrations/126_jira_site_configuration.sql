CREATE TABLE jira_site_configuration (
  workspace_id TEXT PRIMARY KEY REFERENCES workspaces(id) ON DELETE CASCADE,
  announcement_message TEXT NOT NULL DEFAULT '',
  announcement_enabled BOOLEAN NOT NULL DEFAULT FALSE,
  announcement_dismissible BOOLEAN NOT NULL DEFAULT FALSE,
  announcement_visibility TEXT NOT NULL DEFAULT 'public'
    CHECK (announcement_visibility IN ('public', 'private')),
  attachments_enabled BOOLEAN NOT NULL DEFAULT TRUE,
  issue_linking_enabled BOOLEAN NOT NULL DEFAULT TRUE,
  subtasks_enabled BOOLEAN NOT NULL DEFAULT TRUE,
  time_tracking_enabled BOOLEAN NOT NULL DEFAULT TRUE,
  unassigned_issues_allowed BOOLEAN NOT NULL DEFAULT TRUE,
  voting_enabled BOOLEAN NOT NULL DEFAULT TRUE,
  watching_enabled BOOLEAN NOT NULL DEFAULT TRUE,
  time_tracking_provider TEXT NOT NULL DEFAULT 'Jira',
  working_hours_per_day DOUBLE PRECISION NOT NULL DEFAULT 8
    CHECK (working_hours_per_day > 0 AND working_hours_per_day <= 24),
  working_days_per_week DOUBLE PRECISION NOT NULL DEFAULT 5
    CHECK (working_days_per_week > 0 AND working_days_per_week <= 7),
  time_format TEXT NOT NULL DEFAULT 'pretty' CHECK (time_format IN ('pretty', 'days', 'hours')),
  default_unit TEXT NOT NULL DEFAULT 'minute'
    CHECK (default_unit IN ('minute', 'hour', 'day', 'week')),
  navigator_columns TEXT[] NOT NULL DEFAULT ARRAY['issuekey','summary','priority','status','assignee','updated']::TEXT[],
  application_properties JSONB NOT NULL DEFAULT '{}'::JSONB,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO jira_site_configuration(workspace_id)
SELECT id FROM workspaces
ON CONFLICT DO NOTHING;
