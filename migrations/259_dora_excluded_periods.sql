-- A code freeze, a holiday shutdown or an incident drill is not delivery, and
-- Jira lets a team leave such a period out of its DORA metrics. A period
-- belongs to the project whose metrics it changes.
CREATE TABLE project_dora_excluded_periods (
  id           BIGSERIAL PRIMARY KEY,
  project_id   TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  starts_on    DATE NOT NULL,
  ends_on      DATE NOT NULL,
  reason       TEXT NOT NULL DEFAULT '' CHECK (length(reason) <= 255),
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (ends_on >= starts_on)
);

CREATE INDEX project_dora_excluded_periods_project ON project_dora_excluded_periods (project_id, starts_on);
