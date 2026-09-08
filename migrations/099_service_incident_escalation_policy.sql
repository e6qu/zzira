ALTER TABLE service_request_operations
  ADD COLUMN major_incident_declared_at TIMESTAMPTZ,
  ADD COLUMN major_incident_generation INTEGER NOT NULL DEFAULT 0;

UPDATE service_request_operations
SET major_incident_declared_at=updated_at,
    major_incident_generation=1
WHERE major_incident;

CREATE SEQUENCE jira_service_escalation_step_id START 1;
CREATE TABLE service_escalation_steps (
  id              TEXT PRIMARY KEY DEFAULT nextval('jira_service_escalation_step_id')::text,
  service_desk_id TEXT NOT NULL REFERENCES service_desks(id) ON DELETE CASCADE,
  position        INTEGER NOT NULL CHECK(position >= 0),
  delay_minutes   INTEGER NOT NULL CHECK(delay_minutes BETWEEN 1 AND 10080),
  target_user_id  TEXT NOT NULL REFERENCES users(id),
  UNIQUE(service_desk_id,position)
);

CREATE SEQUENCE jira_service_incident_escalation_id START 1;
CREATE TABLE service_incident_escalations (
  id                  TEXT PRIMARY KEY DEFAULT nextval('jira_service_incident_escalation_id')::text,
  request_issue_id    TEXT NOT NULL REFERENCES service_requests(issue_id) ON DELETE CASCADE,
  incident_generation INTEGER NOT NULL,
  step_id             TEXT NOT NULL,
  target_user_id      TEXT NOT NULL REFERENCES users(id),
  delay_minutes       INTEGER NOT NULL,
  created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE(request_issue_id,incident_generation,step_id)
);
CREATE INDEX service_incident_escalations_request
  ON service_incident_escalations(request_issue_id,incident_generation,created_at);
