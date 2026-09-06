CREATE SEQUENCE jira_service_sla_goal_id START 1;

CREATE TABLE service_sla_goals (
  id          TEXT PRIMARY KEY DEFAULT nextval('jira_service_sla_goal_id')::text,
  metric_id   TEXT NOT NULL REFERENCES service_sla_metrics(id) ON DELETE CASCADE,
  name        TEXT NOT NULL,
  jql         TEXT NOT NULL DEFAULT '',
  goal_millis BIGINT NOT NULL CHECK(goal_millis > 0),
  position    INTEGER NOT NULL DEFAULT 0,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE(metric_id,name)
);
CREATE INDEX idx_service_sla_goals_metric ON service_sla_goals(metric_id,position,id);

CREATE FUNCTION seed_service_sla_default_goal() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO service_sla_goals(metric_id,name,jql,goal_millis,position)
  VALUES(NEW.id,'Default goal','',NEW.goal_millis,2147483647);
  RETURN NEW;
END $$;
CREATE TRIGGER seed_service_sla_default_goal_after_insert AFTER INSERT ON service_sla_metrics
FOR EACH ROW EXECUTE FUNCTION seed_service_sla_default_goal();

INSERT INTO service_sla_goals(metric_id,name,jql,goal_millis,position)
SELECT id,'Default goal','',goal_millis,2147483647 FROM service_sla_metrics
ON CONFLICT(metric_id,name) DO NOTHING;

ALTER TABLE service_sla_cycles ADD COLUMN goal_id TEXT REFERENCES service_sla_goals(id) ON DELETE SET NULL;
ALTER TABLE service_sla_cycles ADD COLUMN goal_name TEXT NOT NULL DEFAULT 'Default goal';
ALTER TABLE service_sla_cycles ADD COLUMN goal_millis BIGINT;
UPDATE service_sla_cycles c SET goal_id=g.id,goal_name=g.name,goal_millis=g.goal_millis
FROM service_sla_goals g WHERE g.metric_id=c.metric_id AND g.jql='';
