-- Migration 121 was corrected before fresh installations shipped, but sites
-- that had already applied its original expression retained the old function.
-- Replacing it here makes each generated day a local calendar date before its
-- work-window boundaries are converted to UTC, including across DST changes.
CREATE OR REPLACE FUNCTION jira_service_business_millis(
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
