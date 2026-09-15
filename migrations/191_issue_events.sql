-- Custom issue events: administrators add events beyond Jira's built-in ones.
-- Notification schemes map them to recipients and workflow transitions fire
-- them through customIssueEventId. Ids start at 10000, above the built-ins.
CREATE SEQUENCE jira_issue_event_id START WITH 10000;

CREATE TABLE issue_events (
  id BIGINT PRIMARY KEY DEFAULT nextval('jira_issue_event_id'),
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 255 AND name = btrim(name)),
  description TEXT NOT NULL DEFAULT '' CHECK (length(description) <= 1000),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX issue_events_workspace_name ON issue_events(workspace_id, lower(name));
