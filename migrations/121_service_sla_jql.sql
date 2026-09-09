CREATE SEQUENCE jira_service_sla_pause_id START 1;

CREATE TABLE service_sla_cycle_pauses (
  id         TEXT PRIMARY KEY DEFAULT nextval('jira_service_sla_pause_id')::text,
  cycle_id   TEXT NOT NULL REFERENCES service_sla_cycles(id) ON DELETE CASCADE,
  started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  stopped_at TIMESTAMPTZ,
  reason     TEXT NOT NULL DEFAULT '',
  CHECK(stopped_at IS NULL OR stopped_at >= started_at)
);

CREATE UNIQUE INDEX idx_service_sla_cycle_pauses_open
  ON service_sla_cycle_pauses(cycle_id) WHERE stopped_at IS NULL;
CREATE INDEX idx_service_sla_cycle_pauses_cycle
  ON service_sla_cycle_pauses(cycle_id,started_at,id);

-- Business time is calculated in the calendar's local time zone. Converting
-- each local work-window boundary independently keeps DST transitions exact.
CREATE FUNCTION jira_service_business_millis(
  calendar_key TEXT,
  range_start TIMESTAMPTZ,
  range_end TIMESTAMPTZ
) RETURNS BIGINT LANGUAGE sql STABLE AS $$
  SELECT CASE WHEN range_end <= range_start THEN 0 ELSE COALESCE(sum(
    (extract(epoch FROM (
      LEAST(range_end, (days.day::date + make_interval(mins => calendar.end_minute)) AT TIME ZONE calendar.time_zone)
      - GREATEST(range_start, (days.day::date + make_interval(mins => calendar.start_minute)) AT TIME ZONE calendar.time_zone)
    )) * 1000)::BIGINT
  ), 0) END
  FROM service_calendars calendar
  CROSS JOIN LATERAL generate_series(
    (range_start AT TIME ZONE calendar.time_zone)::date,
    (range_end AT TIME ZONE calendar.time_zone)::date,
    interval '1 day'
  ) AS days(day)
  WHERE calendar.id=calendar_key
    AND extract(isodow FROM days.day)::SMALLINT = ANY(calendar.weekdays)
    AND NOT EXISTS (
      SELECT 1 FROM service_calendar_holidays holiday
      WHERE holiday.calendar_id=calendar.id AND holiday.holiday=days.day::date
    )
    AND LEAST(range_end, (days.day::date + make_interval(mins => calendar.end_minute)) AT TIME ZONE calendar.time_zone)
        > GREATEST(range_start, (days.day::date + make_interval(mins => calendar.start_minute)) AT TIME ZONE calendar.time_zone)
$$;

CREATE FUNCTION jira_service_sla_elapsed_millis(
  cycle_key TEXT,
  evaluated_at TIMESTAMPTZ
) RETURNS BIGINT LANGUAGE sql STABLE AS $$
  SELECT GREATEST(0,
    jira_service_business_millis(metric.calendar_id, cycle.started_at, LEAST(evaluated_at, COALESCE(cycle.stopped_at,evaluated_at)))
    - COALESCE((
      SELECT sum(jira_service_business_millis(
        metric.calendar_id,
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

CREATE FUNCTION jira_service_within_calendar(calendar_key TEXT, evaluated_at TIMESTAMPTZ)
RETURNS BOOLEAN LANGUAGE sql STABLE AS $$
  SELECT COALESCE(
    extract(isodow FROM evaluated_at AT TIME ZONE calendar.time_zone)::SMALLINT = ANY(calendar.weekdays)
    AND NOT EXISTS (
      SELECT 1 FROM service_calendar_holidays holiday
      WHERE holiday.calendar_id=calendar.id
        AND holiday.holiday=(evaluated_at AT TIME ZONE calendar.time_zone)::date
    )
    AND (evaluated_at AT TIME ZONE calendar.time_zone)::time
        >= (time '00:00' + make_interval(mins => calendar.start_minute))
    AND (calendar.end_minute=1440 OR (evaluated_at AT TIME ZONE calendar.time_zone)::time
        < (time '00:00' + make_interval(mins => calendar.end_minute))),
    FALSE
  ) FROM service_calendars calendar WHERE calendar.id=calendar_key
$$;
