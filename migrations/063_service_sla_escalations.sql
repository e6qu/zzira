ALTER TABLE service_queues DROP CONSTRAINT service_queues_kind_check;
ALTER TABLE service_queues ADD CONSTRAINT service_queues_kind_check
  CHECK(kind IN ('all_open','sla_attention','unassigned','assigned_to_me'));
UPDATE service_queues SET position=position+1 WHERE position>=1;
INSERT INTO service_queues(service_desk_id,name,jql,kind,position)
SELECT id,'SLA attention','resolution = Unresolved ORDER BY created ASC','sla_attention',1
FROM service_desks ON CONFLICT(service_desk_id,name) DO NOTHING;

CREATE SEQUENCE jira_service_sla_escalation_id START 1;
CREATE TABLE service_sla_escalations (
  id          TEXT PRIMARY KEY DEFAULT nextval('jira_service_sla_escalation_id')::text,
  cycle_id    TEXT NOT NULL REFERENCES service_sla_cycles(id) ON DELETE CASCADE,
  user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  stage       TEXT NOT NULL CHECK(stage IN ('warning','breached')),
  notified_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE(cycle_id,user_id,stage)
);
CREATE INDEX idx_service_sla_escalations_cycle ON service_sla_escalations(cycle_id,stage);
