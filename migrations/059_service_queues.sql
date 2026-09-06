CREATE SEQUENCE jira_service_queue_id START 1;
CREATE TABLE service_queues (
  id              TEXT PRIMARY KEY DEFAULT nextval('jira_service_queue_id')::text,
  service_desk_id TEXT NOT NULL REFERENCES service_desks(id) ON DELETE CASCADE,
  name            TEXT NOT NULL,
  jql             TEXT NOT NULL,
  kind            TEXT NOT NULL CHECK(kind IN ('all_open','unassigned','assigned_to_me')),
  fields          TEXT[] NOT NULL DEFAULT ARRAY['issuetype','issuekey','summary','created','reporter','assignee'],
  position        INTEGER NOT NULL DEFAULT 0,
  UNIQUE(service_desk_id,name)
);
CREATE INDEX idx_service_queues_desk ON service_queues(service_desk_id,position,id);

INSERT INTO service_queues(service_desk_id,name,jql,kind,position)
SELECT id,'All open requests','resolution = Unresolved ORDER BY created ASC','all_open',0 FROM service_desks
UNION ALL
SELECT id,'Unassigned requests','assignee is EMPTY AND resolution = Unresolved ORDER BY created ASC','unassigned',1 FROM service_desks
UNION ALL
SELECT id,'Assigned to me','assignee = currentUser() AND resolution = Unresolved ORDER BY created ASC','assigned_to_me',2 FROM service_desks;
