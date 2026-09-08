CREATE SEQUENCE jira_service_calendar_id START 1;
CREATE TABLE service_calendars (
  id              TEXT PRIMARY KEY DEFAULT nextval('jira_service_calendar_id')::text,
  service_desk_id TEXT NOT NULL UNIQUE REFERENCES service_desks(id) ON DELETE CASCADE,
  name            TEXT NOT NULL DEFAULT 'Default calendar',
  time_zone       TEXT NOT NULL DEFAULT 'UTC',
  weekdays        SMALLINT[] NOT NULL DEFAULT ARRAY[1,2,3,4,5]::SMALLINT[],
  start_minute    SMALLINT NOT NULL DEFAULT 540 CHECK(start_minute BETWEEN 0 AND 1439),
  end_minute      SMALLINT NOT NULL DEFAULT 1020 CHECK(end_minute BETWEEN 1 AND 1440),
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK(start_minute < end_minute)
);

CREATE TABLE service_calendar_holidays (
  calendar_id TEXT NOT NULL REFERENCES service_calendars(id) ON DELETE CASCADE,
  holiday     DATE NOT NULL,
  name        TEXT NOT NULL DEFAULT '',
  PRIMARY KEY(calendar_id,holiday)
);

CREATE SEQUENCE jira_service_sla_id START 1;
CREATE TABLE service_sla_metrics (
  id              TEXT PRIMARY KEY DEFAULT nextval('jira_service_sla_id')::text,
  service_desk_id TEXT NOT NULL REFERENCES service_desks(id) ON DELETE CASCADE,
  calendar_id     TEXT NOT NULL REFERENCES service_calendars(id) ON DELETE RESTRICT,
  name            TEXT NOT NULL,
  kind            TEXT NOT NULL CHECK(kind IN ('first_response','resolution')),
  goal_millis     BIGINT NOT NULL CHECK(goal_millis > 0),
  position        INTEGER NOT NULL DEFAULT 0,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE(service_desk_id,kind),
  UNIQUE(service_desk_id,name)
);
CREATE INDEX idx_service_sla_metrics_desk ON service_sla_metrics(service_desk_id,position,id);

CREATE SEQUENCE jira_service_sla_cycle_id START 1;
CREATE TABLE service_sla_cycles (
  id               TEXT PRIMARY KEY DEFAULT nextval('jira_service_sla_cycle_id')::text,
  request_issue_id TEXT NOT NULL REFERENCES service_requests(issue_id) ON DELETE CASCADE,
  metric_id        TEXT NOT NULL REFERENCES service_sla_metrics(id) ON DELETE CASCADE,
  cycle_number     INTEGER NOT NULL DEFAULT 1,
  started_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  stopped_at       TIMESTAMPTZ,
  UNIQUE(request_issue_id,metric_id,cycle_number)
);
CREATE UNIQUE INDEX idx_service_sla_cycles_ongoing ON service_sla_cycles(request_issue_id,metric_id) WHERE stopped_at IS NULL;

CREATE FUNCTION seed_service_desk_slas() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE calendar_key TEXT;
BEGIN
  INSERT INTO service_calendars(service_desk_id) VALUES(NEW.id) RETURNING id INTO calendar_key;
  INSERT INTO service_sla_metrics(service_desk_id,calendar_id,name,kind,goal_millis,position) VALUES
    (NEW.id,calendar_key,'Time to first response','first_response',14400000,0),
    (NEW.id,calendar_key,'Time to resolution','resolution',28800000,1);
  RETURN NEW;
END $$;
CREATE TRIGGER seed_service_desk_slas_after_insert AFTER INSERT ON service_desks
FOR EACH ROW EXECUTE FUNCTION seed_service_desk_slas();

INSERT INTO service_calendars(service_desk_id)
SELECT id FROM service_desks ON CONFLICT(service_desk_id) DO NOTHING;
INSERT INTO service_sla_metrics(service_desk_id,calendar_id,name,kind,goal_millis,position)
SELECT sd.id,c.id,'Time to first response','first_response',14400000,0
FROM service_desks sd JOIN service_calendars c ON c.service_desk_id=sd.id
ON CONFLICT(service_desk_id,kind) DO NOTHING;
INSERT INTO service_sla_metrics(service_desk_id,calendar_id,name,kind,goal_millis,position)
SELECT sd.id,c.id,'Time to resolution','resolution',28800000,1
FROM service_desks sd JOIN service_calendars c ON c.service_desk_id=sd.id
ON CONFLICT(service_desk_id,kind) DO NOTHING;
INSERT INTO service_sla_cycles(request_issue_id,metric_id,started_at)
SELECT sr.issue_id,m.id,sr.created_at
FROM service_requests sr JOIN service_sla_metrics m ON m.service_desk_id=sr.service_desk_id
ON CONFLICT(request_issue_id,metric_id,cycle_number) DO NOTHING;
