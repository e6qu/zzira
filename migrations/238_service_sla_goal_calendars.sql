-- Jira measures each SLA goal in the calendar chosen beside its JQL and time,
-- so a desk keeps several calendars and a goal names the one its clock runs in.
ALTER TABLE service_calendars DROP CONSTRAINT service_calendars_service_desk_id_key;
CREATE INDEX idx_service_calendars_desk ON service_calendars(service_desk_id,lower(name),id);
CREATE UNIQUE INDEX idx_service_calendars_desk_name ON service_calendars(service_desk_id,lower(name));

ALTER TABLE service_sla_goals
  ADD COLUMN calendar_id TEXT REFERENCES service_calendars(id) ON DELETE SET NULL;

-- A cycle keeps the calendar it was measured in, beside the goal snapshot, so
-- changing a goal later does not rewrite the hours a request already spent.
ALTER TABLE service_sla_cycles
  ADD COLUMN calendar_id TEXT REFERENCES service_calendars(id) ON DELETE SET NULL;

-- Elapsed time now prefers the cycle's calendar and falls back to the metric's.
CREATE OR REPLACE FUNCTION jira_service_sla_elapsed_millis(
  cycle_key TEXT,
  evaluated_at TIMESTAMPTZ
) RETURNS BIGINT LANGUAGE sql STABLE AS $$
  SELECT GREATEST(0,
    jira_service_business_millis(COALESCE(cycle.calendar_id,metric.calendar_id), cycle.started_at, LEAST(evaluated_at, COALESCE(cycle.stopped_at,evaluated_at)))
    - COALESCE((
      SELECT sum(jira_service_business_millis(
        COALESCE(cycle.calendar_id,metric.calendar_id),
        GREATEST(pause.started_at,cycle.started_at),
        LEAST(COALESCE(pause.stopped_at,evaluated_at),COALESCE(cycle.stopped_at,evaluated_at),evaluated_at)
      )) FROM service_sla_cycle_pauses pause
      WHERE pause.cycle_id=cycle.id AND pause.started_at < COALESCE(cycle.stopped_at,evaluated_at)
    ),0)
  )
  FROM service_sla_cycles cycle
  JOIN service_sla_metrics metric ON metric.id=cycle.metric_id
  WHERE cycle.id=cycle_key
$$;
