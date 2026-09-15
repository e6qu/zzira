-- A major incident's response team takes named incident roles, and
-- stakeholders outside the request follow stakeholder updates by email.
CREATE TABLE service_incident_roles (
  request_issue_id TEXT NOT NULL REFERENCES service_requests(issue_id) ON DELETE CASCADE,
  role             TEXT NOT NULL CHECK (role IN ('commander','communications','technical')),
  user_id          TEXT NOT NULL REFERENCES users(id),
  assigned_by      TEXT NOT NULL REFERENCES users(id),
  assigned_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (request_issue_id, role)
);

CREATE TABLE service_incident_stakeholders (
  id               BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  request_issue_id TEXT NOT NULL REFERENCES service_requests(issue_id) ON DELETE CASCADE,
  user_id          TEXT REFERENCES users(id),
  email            TEXT CHECK (email IS NULL OR length(email) BETWEEN 3 AND 320),
  added_by         TEXT NOT NULL REFERENCES users(id),
  added_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK ((user_id IS NULL) <> (email IS NULL))
);
CREATE UNIQUE INDEX service_incident_stakeholders_user ON service_incident_stakeholders (request_issue_id, user_id) WHERE user_id IS NOT NULL;
CREATE UNIQUE INDEX service_incident_stakeholders_email ON service_incident_stakeholders (request_issue_id, lower(email)) WHERE email IS NOT NULL;

ALTER TABLE service_incident_updates
  DROP CONSTRAINT service_incident_updates_audience_check,
  ADD CONSTRAINT service_incident_updates_audience_check CHECK (audience IN ('public','internal','stakeholders'));
