ALTER TABLE service_queues DROP CONSTRAINT service_queues_kind_check;
ALTER TABLE service_queues ADD CONSTRAINT service_queues_kind_check
  CHECK(kind IN ('all_open','sla_attention','unassigned','assigned_to_me','custom'));
