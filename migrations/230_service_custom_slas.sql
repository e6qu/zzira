-- Service managers add their own SLAs next to time to first response and time
-- to resolution; each desk still has one of each built-in SLA.
ALTER TABLE service_sla_metrics DROP CONSTRAINT service_sla_metrics_kind_check;
ALTER TABLE service_sla_metrics ADD CONSTRAINT service_sla_metrics_kind_check CHECK (kind IN ('first_response','resolution','custom'));
ALTER TABLE service_sla_metrics DROP CONSTRAINT service_sla_metrics_service_desk_id_kind_key;
CREATE UNIQUE INDEX service_sla_metrics_builtin_kind ON service_sla_metrics(service_desk_id,kind) WHERE kind<>'custom';
