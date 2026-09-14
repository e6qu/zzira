-- Atlassian teams and Jira plans (Advanced planning) with the teams planned in
-- them. Plan, plan-only team and issue source ids are Jira's numbers.

CREATE TABLE atlassian_teams (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  name         TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 255),
  description  TEXT NOT NULL DEFAULT '' CHECK (char_length(description) <= 2000),
  created_by   TEXT NOT NULL,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX atlassian_teams_workspace ON atlassian_teams(workspace_id, lower(name), id);

CREATE TABLE atlassian_team_members (
  team_id  UUID NOT NULL REFERENCES atlassian_teams(id) ON DELETE CASCADE,
  user_id  TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  added_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (team_id, user_id)
);

CREATE SEQUENCE jira_plan_id START 1;
CREATE SEQUENCE jira_plan_scenario_id START 1;
CREATE SEQUENCE jira_plan_issue_source_id START 1;
CREATE SEQUENCE jira_plan_team_id START 1;

CREATE TABLE plans (
  id                     BIGINT PRIMARY KEY DEFAULT nextval('jira_plan_id'),
  workspace_id           TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  name                   TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 255),
  lead_account_id        TEXT REFERENCES users(id) ON DELETE SET NULL,
  scheduling             JSONB NOT NULL,
  exclusion_rules        JSONB NOT NULL,
  custom_fields          JSONB NOT NULL DEFAULT '[]',
  cross_project_releases JSONB NOT NULL DEFAULT '[]',
  permissions            JSONB NOT NULL DEFAULT '[]',
  status                 TEXT NOT NULL DEFAULT 'Active' CHECK (status IN ('Active', 'Trashed', 'Archived')),
  scenario_id            BIGINT NOT NULL DEFAULT nextval('jira_plan_scenario_id'),
  created_by             TEXT NOT NULL,
  created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX plans_workspace ON plans(workspace_id, id);

CREATE TABLE plan_issue_sources (
  id       BIGINT PRIMARY KEY DEFAULT nextval('jira_plan_issue_source_id'),
  plan_id  BIGINT NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
  position INT NOT NULL,
  type     TEXT NOT NULL CHECK (type IN ('Board', 'Project', 'Filter')),
  value    BIGINT NOT NULL,
  UNIQUE (plan_id, type, value)
);

CREATE INDEX plan_issue_sources_plan ON plan_issue_sources(plan_id, position);

CREATE TABLE plan_teams (
  id                 BIGINT PRIMARY KEY DEFAULT nextval('jira_plan_team_id'),
  plan_id            BIGINT NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
  atlassian_team_id  UUID REFERENCES atlassian_teams(id) ON DELETE CASCADE,
  name               TEXT CHECK (name IS NULL OR char_length(name) BETWEEN 1 AND 255),
  planning_style     TEXT NOT NULL CHECK (planning_style IN ('Scrum', 'Kanban')),
  issue_source_id    BIGINT REFERENCES plan_issue_sources(id) ON DELETE SET NULL,
  sprint_length      BIGINT CHECK (sprint_length IS NULL OR sprint_length > 0),
  capacity           DOUBLE PRECISION CHECK (capacity IS NULL OR capacity >= 0),
  member_account_ids TEXT[] NOT NULL DEFAULT '{}',
  created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (plan_id, atlassian_team_id),
  CHECK ((atlassian_team_id IS NULL) <> (name IS NULL))
);

CREATE INDEX plan_teams_plan ON plan_teams(plan_id, id);
