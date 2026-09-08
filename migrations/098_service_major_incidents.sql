ALTER TABLE service_request_operations
  ADD COLUMN major_incident BOOLEAN NOT NULL DEFAULT FALSE;

CREATE SEQUENCE jira_service_incident_update_id START 1;
CREATE TABLE service_incident_updates (
  id               TEXT PRIMARY KEY DEFAULT nextval('jira_service_incident_update_id')::text,
  request_issue_id TEXT NOT NULL REFERENCES service_requests(issue_id) ON DELETE CASCADE,
  author_id        TEXT NOT NULL REFERENCES users(id),
  audience         TEXT NOT NULL CHECK(audience IN ('public','internal')),
  message          TEXT NOT NULL CHECK(length(message) BETWEEN 1 AND 10000),
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX service_incident_updates_request ON service_incident_updates(request_issue_id,created_at DESC,id DESC);
