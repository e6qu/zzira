-- A release is a promise, and some teams want the promise checked before it
-- is made: every approver has approved, and nothing unresolved is still in
-- the version. Jira calls these release conditions; a project that sets none
-- ships whenever somebody says so, which is what every project did until now.
CREATE TABLE project_release_gates (
  project_id        TEXT PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
  require_approvals BOOLEAN NOT NULL DEFAULT FALSE,
  require_resolved  BOOLEAN NOT NULL DEFAULT FALSE,
  updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
